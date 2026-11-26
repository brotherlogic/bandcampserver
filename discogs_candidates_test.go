package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	bcpb "github.com/brotherlogic/bandcamplib/proto"
	rcpb "github.com/brotherlogic/recordcollection/proto"
)

func TestDiscogsCandidateConstants(t *testing.T) {
	if discogsLookupTimeout != 10*time.Second {
		t.Fatalf("expected discogsLookupTimeout=10s, got %v", discogsLookupTimeout)
	}
	if discogsSearchPerPage != 10 {
		t.Fatalf("expected discogsSearchPerPage=10, got %d", discogsSearchPerPage)
	}
	if maxDiscogsCandidates != 3 {
		t.Fatalf("expected maxDiscogsCandidates=3, got %d", maxDiscogsCandidates)
	}
	if maxSearchFieldRunes != 500 {
		t.Fatalf("expected maxSearchFieldRunes=500, got %d", maxSearchFieldRunes)
	}
	if maxRenderedFieldRunes != 200 {
		t.Fatalf("expected maxRenderedFieldRunes=200, got %d", maxRenderedFieldRunes)
	}
	if discogsReleaseBaseURL != "https://www.discogs.com/release/" {
		t.Fatalf("expected discogsReleaseBaseURL=https://www.discogs.com/release/, got %q", discogsReleaseBaseURL)
	}
}

func TestTruncateRunes(t *testing.T) {
	tests := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{
			name: "trims surrounding whitespace",
			in:   "  hello world  ",
			max:  20,
			want: "hello world",
		},
		{
			name: "zero or negative max returns empty",
			in:   "hello",
			max:  0,
			want: "",
		},
		{
			name: "negative max returns empty",
			in:   "hello",
			max:  -5,
			want: "",
		},
		{
			name: "truncates ascii and trims trailing space at boundary",
			in:   "hello world",
			max:  6,
			want: "hello",
		},
		{
			name: "truncates multibyte UTF-8 cleanly on rune boundary",
			in:   "  日本語テスト🎵🎶  ",
			max:  5,
			want: "日本語テス",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := truncateRunes(tc.in, tc.max)
			if got != tc.want {
				t.Fatalf("truncateRunes(%q, %d) = %q, want %q", tc.in, tc.max, got, tc.want)
			}
			if !utf8.ValidString(got) {
				t.Fatalf("truncateRunes(%q, %d) produced invalid UTF-8: %q", tc.in, tc.max, got)
			}
		})
	}
}

func TestSanitizeMarkdownInline(t *testing.T) {
	t.Run("collapses control chars and escapes markdown characters", func(t *testing.T) {
		in := " \tLine1\r\nLine2\x00\x1f[link](*bold* _italic_ `code` \\slash)\x7f "
		got := sanitizeMarkdownInline(in, 200)
		want := `Line1 Line2 \[link\](\*bold\* \_italic\_ \` + "`" + `code\` + "`" + ` \\slash)`
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("truncates at maxRunes without invalid UTF-8 or dangling backslash", func(t *testing.T) {
		// Repeating multibyte + escapable chars >200 runes
		in := strings.Repeat("音*_", 120)
		got := sanitizeMarkdownInline(in, maxRenderedFieldRunes)
		if !utf8.ValidString(got) {
			t.Fatalf("output is not valid UTF-8: %q", got)
		}
		if rc := utf8.RuneCountInString(got); rc > maxRenderedFieldRunes {
			t.Fatalf("rune count %d exceeds max %d", rc, maxRenderedFieldRunes)
		}

		// Boundary that would otherwise split an escape pair (e.g., max=2 on "*a" -> "" or max=3 -> "\*a")
		gotBoundary := sanitizeMarkdownInline("a*b", 2)
		if gotBoundary != "a" {
			t.Fatalf("expected no dangling escape backslash at maxRunes=2, got %q", gotBoundary)
		}
		if strings.HasSuffix(gotBoundary, `\`) {
			t.Fatalf("unexpected dangling backslash in %q", gotBoundary)
		}
	})

	t.Run("non-positive maxRunes returns empty", func(t *testing.T) {
		if got := sanitizeMarkdownInline("hello", 0); got != "" {
			t.Fatalf("expected empty string, got %q", got)
		}
	})
}

func TestNormalizeDiscogsCandidates(t *testing.T) {
	longTitle := strings.Repeat("漢", 195) + "[*test*]\n\r\t`more`"
	results := []*rcpb.SearchReleaseResult{
		nil,
		{ReleaseId: 0, Title: "Zero ID"},
		{ReleaseId: -42, Title: "Negative ID"},
		{
			ReleaseId: 101,
			Title:     "  First [Album]\n*Special* _Edition_ `01` \\Mix  ",
			Year:      " 2024 ",
			Formats:   []string{"File", " ", "\tWAV\n", "FLAC"},
			Url:       " https://www.discogs.com/release/101-first_album \n\t",
		},
		{
			ReleaseId: 101,
			Title:     "Duplicate First Album Should Be Ignored",
			Year:      "2025",
		},
		{
			ReleaseId: 202,
			Title:     "  \n\t\x00  ",
			Year:      "",
			Formats:   []string{"", "   "},
			Url:       "   ",
		},
		{
			ReleaseId: 303,
			Title:     longTitle,
			Year:      "2023",
			Formats:   []string{"Vinyl"},
			Url:       "",
		},
		{
			ReleaseId: 404,
			Title:     "Fourth Candidate Must Be Capped",
			Year:      "2022",
			Formats:   []string{"CD"},
		},
	}

	got := normalizeDiscogsCandidates(results, maxDiscogsCandidates)
	if len(got) != 3 {
		t.Fatalf("expected 3 candidates, got %d: %+v", len(got), got)
	}

	// Candidate 1: sanitized title, preserved URL without escaping underscores, cleaned formats.
	if got[0].ReleaseID != 101 {
		t.Fatalf("candidate[0].ReleaseID = %d, want 101", got[0].ReleaseID)
	}
	wantTitle0 := `First \[Album\] \*Special\* \_Edition\_ \` + "`" + `01\` + "`" + ` \\Mix`
	if got[0].Title != wantTitle0 {
		t.Fatalf("candidate[0].Title = %q, want %q", got[0].Title, wantTitle0)
	}
	if got[0].Year != "2024" {
		t.Fatalf("candidate[0].Year = %q, want %q", got[0].Year, "2024")
	}
	if strings.Join(got[0].Formats, ",") != "File,WAV,FLAC" {
		t.Fatalf("candidate[0].Formats = %v, want [File WAV FLAC]", got[0].Formats)
	}
	if got[0].URL != "https://www.discogs.com/release/101-first_album" {
		t.Fatalf("candidate[0].URL = %q", got[0].URL)
	}

	// Candidate 2: blank title fallback, empty formats omitted, blank URL fallback.
	if got[1].ReleaseID != 202 {
		t.Fatalf("candidate[1].ReleaseID = %d, want 202", got[1].ReleaseID)
	}
	if got[1].Title != "Release #202" {
		t.Fatalf("candidate[1].Title = %q, want %q", got[1].Title, "Release #202")
	}
	if got[1].Year != "" || len(got[1].Formats) != 0 {
		t.Fatalf("expected empty year and formats, got year=%q formats=%v", got[1].Year, got[1].Formats)
	}
	if got[1].URL != "https://www.discogs.com/release/202" {
		t.Fatalf("candidate[1].URL = %q, want default release URL", got[1].URL)
	}

	// Candidate 3: >200 rune title cleanly bounded on UTF-8 rune boundaries.
	if got[2].ReleaseID != 303 {
		t.Fatalf("candidate[2].ReleaseID = %d, want 303", got[2].ReleaseID)
	}
	if !utf8.ValidString(got[2].Title) {
		t.Fatalf("candidate[2].Title invalid UTF-8: %q", got[2].Title)
	}
	if rc := utf8.RuneCountInString(got[2].Title); rc > maxRenderedFieldRunes {
		t.Fatalf("candidate[2].Title rune count %d > %d", rc, maxRenderedFieldRunes)
	}
	if got[2].URL != "https://www.discogs.com/release/303" {
		t.Fatalf("candidate[2].URL = %q", got[2].URL)
	}

	if empty := normalizeDiscogsCandidates(results, 0); len(empty) != 0 {
		t.Fatalf("expected 0 candidates for limit=0, got %d", len(empty))
	}
}

func TestFormatDiscogsCandidatesSection(t *testing.T) {
	const albumID int64 = 987654321

	t.Run("outcomeDigital includes header, metadata, URL, and CLI mapping commands", func(t *testing.T) {
		res := discogsCandidateResult{
			Outcome: outcomeDigital,
			Candidates: []discogsCandidate{
				{
					ReleaseID: 111,
					Title:     "Digital Album",
					Year:      "2024",
					Formats:   []string{"File", "WAV"},
					URL:       "https://www.discogs.com/release/111",
				},
				{
					ReleaseID: 222,
					Title:     "Digital Single",
					URL:       "https://www.discogs.com/release/222",
				},
			},
		}

		got := formatDiscogsCandidatesSection(albumID, res)
		for _, wantSub := range []string{
			"### Candidate Discogs Digital Releases",
			"- **Digital Album** (2024 — File, WAV) — https://www.discogs.com/release/111",
			"bandcampserver_cli mapping -bandcamp 987654321 -discogs 111",
			"- **Digital Single** — https://www.discogs.com/release/222",
			"bandcampserver_cli mapping -bandcamp 987654321 -discogs 222",
		} {
			if !strings.Contains(got, wantSub) {
				t.Fatalf("missing %q in:\n%s", wantSub, got)
			}
		}
	})

	t.Run("outcomeFallback explicitly notes no digital release and omits CLI mapping commands", func(t *testing.T) {
		res := discogsCandidateResult{
			Outcome: outcomeFallback,
			Candidates: []discogsCandidate{
				{
					ReleaseID: 333,
					Title:     "Vinyl Release",
					Year:      "2021",
					Formats:   []string{"Vinyl", "LP"},
					URL:       "https://www.discogs.com/release/333",
				},
			},
		}

		got := formatDiscogsCandidatesSection(albumID, res)
		for _, wantSub := range []string{
			"### Discogs Candidate Releases (Reference Only)",
			"No digital (`File`) Discogs release was found — a new digital Discogs submission is likely needed. Existing non-digital releases for reference:",
			"- **Vinyl Release** (2021 — Vinyl, LP) — https://www.discogs.com/release/333",
		} {
			if !strings.Contains(got, wantSub) {
				t.Fatalf("missing %q in:\n%s", wantSub, got)
			}
		}
		if strings.Contains(got, "bandcampserver_cli mapping") {
			t.Fatalf("outcomeFallback must NOT include CLI mapping commands, got:\n%s", got)
		}
	})

	t.Run("outcomeNone, outcomeError, outcomeSkipped", func(t *testing.T) {
		wantNone := "### Discogs Candidate Releases\nNo candidate Discogs releases were found — a new Discogs submission is likely needed."
		if got := formatDiscogsCandidatesSection(albumID, discogsCandidateResult{Outcome: outcomeNone}); got != wantNone {
			t.Fatalf("outcomeNone = %q, want %q", got, wantNone)
		}

		wantErr := "Note: Discogs candidate lookup failed; please search Discogs manually."
		if got := formatDiscogsCandidatesSection(albumID, discogsCandidateResult{Outcome: outcomeError}); got != wantErr {
			t.Fatalf("outcomeError = %q, want %q", got, wantErr)
		}

		wantSkipped := "Note: Discogs candidate lookup was skipped because Bandcamp artist and album metadata were empty."
		if got := formatDiscogsCandidatesSection(albumID, discogsCandidateResult{Outcome: outcomeSkipped}); got != wantSkipped {
			t.Fatalf("outcomeSkipped = %q, want %q", got, wantSkipped)
		}
	})

	t.Run("metadata parenthetical variations never leave empty parens or dangling separators", func(t *testing.T) {
		cases := []struct {
			name string
			cand discogsCandidate
			want string
		}{
			{
				name: "year and formats",
				cand: discogsCandidate{ReleaseID: 1, Title: "A", Year: "2020", Formats: []string{"File", "FLAC"}, URL: "https://www.discogs.com/release/1"},
				want: "- **A** (2020 — File, FLAC) — https://www.discogs.com/release/1",
			},
			{
				name: "year only",
				cand: discogsCandidate{ReleaseID: 2, Title: "B", Year: "2020", URL: "https://www.discogs.com/release/2"},
				want: "- **B** (2020) — https://www.discogs.com/release/2",
			},
			{
				name: "formats only",
				cand: discogsCandidate{ReleaseID: 3, Title: "C", Formats: []string{"CD"}, URL: "https://www.discogs.com/release/3"},
				want: "- **C** (CD) — https://www.discogs.com/release/3",
			},
			{
				name: "neither year nor formats",
				cand: discogsCandidate{ReleaseID: 4, Title: "D", URL: "https://www.discogs.com/release/4"},
				want: "- **D** — https://www.discogs.com/release/4",
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				got := formatDiscogsCandidatesSection(albumID, discogsCandidateResult{
					Outcome:    outcomeFallback,
					Candidates: []discogsCandidate{tc.cand},
				})
				if !strings.Contains(got, tc.want) {
					t.Fatalf("expected %q in:\n%s", tc.want, got)
				}
				for _, bad := range []string{"()", "( —", "— )"} {
					if strings.Contains(got, bad) {
						t.Fatalf("unexpected malformed separator %q in:\n%s", bad, got)
					}
				}
			})
		}
	})
}

func TestBuildMissingMappingIssueBody(t *testing.T) {
	const (
		secret1 = "super-secret-cookie-1"
		secret2 = "refreshed-secret-cookie-2"
	)

	t.Run("WAV-link path preserves base text, appends candidates, and redacts cookies everywhere", func(t *testing.T) {
		item := &bcpb.Item{
			AlbumId:    12345,
			BandName:   fmt.Sprintf("Artist %s", secret1),
			AlbumTitle: fmt.Sprintf("Album %s", secret2),
		}
		wavURL := fmt.Sprintf("https://p4.bcbits.com/download/album?sig=%s", secret1)
		res := discogsCandidateResult{
			Outcome: outcomeDigital,
			Candidates: []discogsCandidate{
				{
					ReleaseID: 555,
					Title:     fmt.Sprintf("Match %s", secret2),
					Year:      "2024",
					Formats:   []string{"File", secret1},
					URL:       fmt.Sprintf("https://www.discogs.com/release/555?c=%s", secret2),
				},
			},
		}

		body := buildMissingMappingIssueBody(item, wavURL, 3, res, secret1, secret2)
		if strings.Contains(body, secret1) || strings.Contains(body, secret2) {
			t.Fatalf("cookie secret leaked in WAV issue body:\n%s", body)
		}
		if !strings.Contains(body, "[REDACTED]") {
			t.Fatalf("expected [REDACTED] in WAV issue body:\n%s", body)
		}
		for _, want := range []string{
			"Artist [REDACTED] - Album [REDACTED] (12345) is missing a mapping",
			"Direct WAV download link: https://p4.bcbits.com/download/album?sig=[REDACTED]",
			"Note: Bandcamp direct download links are time-limited and may expire over time.",
			"### Candidate Discogs Digital Releases",
			"- **Match [REDACTED]** (2024 — File, [REDACTED]) — https://www.discogs.com/release/555?c=[REDACTED]",
			"bandcampserver_cli mapping -bandcamp 12345 -discogs 555",
		} {
			if !strings.Contains(body, want) {
				t.Fatalf("missing %q in WAV issue body:\n%s", want, body)
			}
		}
	})

	t.Run("linkless fallback path preserves base text, appends candidates, and redacts cookies everywhere", func(t *testing.T) {
		item := &bcpb.Item{
			AlbumId:    67890,
			BandName:   fmt.Sprintf("Band %s", secret1),
			AlbumTitle: "Clean Title",
		}
		res := discogsCandidateResult{
			Outcome: outcomeFallback,
			Candidates: []discogsCandidate{
				{
					ReleaseID: 777,
					Title:     fmt.Sprintf("Ref %s", secret2),
					Year:      "2022",
					Formats:   []string{"Vinyl"},
					URL:       fmt.Sprintf("https://www.discogs.com/release/777?t=%s", secret1),
				},
			},
		}

		body := buildMissingMappingIssueBody(item, "", 3, res, secret1, secret2)
		if strings.Contains(body, secret1) || strings.Contains(body, secret2) {
			t.Fatalf("cookie secret leaked in linkless fallback body:\n%s", body)
		}
		for _, want := range []string{
			"Band [REDACTED] - Clean Title (67890) is missing a mapping",
			"Note: WAV download link generation failed after 3 attempts.",
			"### Discogs Candidate Releases (Reference Only)",
			"- **Ref [REDACTED]** (2022 — Vinyl) — https://www.discogs.com/release/777?t=[REDACTED]",
		} {
			if !strings.Contains(body, want) {
				t.Fatalf("missing %q in linkless fallback body:\n%s", want, body)
			}
		}
		if strings.Contains(body, "bandcampserver_cli mapping") {
			t.Fatalf("linkless fallback with outcomeFallback must not emit mapping command:\n%s", body)
		}
	})
}
