package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	bcpb "github.com/brotherlogic/bandcamplib/proto"
	rcpb "github.com/brotherlogic/recordcollection/proto"
	"github.com/prometheus/client_golang/prometheus/testutil"
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

type lookupTestRecorder struct {
	mu   sync.Mutex
	logs []string
}

func (r *lookupTestRecorder) log(_ context.Context, msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logs = append(r.logs, msg)
}

func (r *lookupTestRecorder) messages() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.logs))
	copy(out, r.logs)
	return out
}

func newLookupTestServer() (*Server, *lookupTestRecorder) {
	rec := &lookupTestRecorder{}
	return &Server{logFunc: rec.log}, rec
}

func snapshotDiscogsLookupCounters() map[discogsLookupOutcome]float64 {
	outcomes := []discogsLookupOutcome{
		outcomeDigital,
		outcomeFallback,
		outcomeNone,
		outcomeSkipped,
		outcomeError,
	}
	snap := make(map[discogsLookupOutcome]float64, len(outcomes))
	for _, o := range outcomes {
		snap[o] = testutil.ToFloat64(discogsCandidateLookupTotal.WithLabelValues(string(o)))
	}
	return snap
}

func assertDiscogsLookupCounterDelta(t *testing.T, before map[discogsLookupOutcome]float64, wantOutcome discogsLookupOutcome) {
	t.Helper()
	after := snapshotDiscogsLookupCounters()
	for outcome, prev := range before {
		wantDelta := 0.0
		if outcome == wantOutcome {
			wantDelta = 1.0
		}
		if gotDelta := after[outcome] - prev; gotDelta != wantDelta {
			t.Fatalf("discogsCandidateLookupTotal[%q] delta = %v, want %v", outcome, gotDelta, wantDelta)
		}
	}
}

func TestLookupDiscogsCandidates_PrimaryDigitalHit(t *testing.T) {
	before := snapshotDiscogsLookupCounters()
	s, rec := newLookupTestServer()
	rc := &clientUpdateFakeRCClient{
		searchFn: func(ctx context.Context, req *rcpb.SearchReleasesRequest) (*rcpb.SearchReleasesResponse, error) {
			return &rcpb.SearchReleasesResponse{
				Results: []*rcpb.SearchReleaseResult{
					{ReleaseId: 101, Title: "Digital 1", Year: "2024", Formats: []string{"File", "WAV"}},
					{ReleaseId: 101, Title: "Duplicate 101"},
					{ReleaseId: 202, Title: "Digital 2", Year: "2024", Formats: []string{"File"}},
					{ReleaseId: 303, Title: "Digital 3", Year: "2024", Formats: []string{"File"}},
					{ReleaseId: 404, Title: "Digital 4 Should Be Capped"},
				},
			}, nil
		},
	}
	item := &bcpb.Item{AlbumId: 9001, BandName: "Test Band", AlbumTitle: "Test Album"}

	res := s.lookupDiscogsCandidates(context.Background(), rc, item)
	if res.Outcome != outcomeDigital {
		t.Fatalf("Outcome = %q, want %q", res.Outcome, outcomeDigital)
	}
	if len(res.Candidates) != 3 {
		t.Fatalf("len(Candidates) = %d, want 3", len(res.Candidates))
	}
	if len(rc.searchCalls) != 1 {
		t.Fatalf("expected exactly 1 SearchReleases call (no fallback), got %d", len(rc.searchCalls))
	}
	gotReq := rc.searchCalls[0]
	if gotReq.GetArtist() != "Test Band" || gotReq.GetReleaseTitle() != "Test Album" || gotReq.GetFormat() != "File" || gotReq.GetPerPage() != 10 {
		t.Fatalf("unexpected primary SearchReleasesRequest: %+v", gotReq)
	}
	if len(rec.messages()) != 0 {
		t.Fatalf("expected no error logs, got %v", rec.messages())
	}
	assertDiscogsLookupCounterDelta(t, before, outcomeDigital)
}

func TestLookupDiscogsCandidates_NonDigitalFallbackHit(t *testing.T) {
	cases := []struct {
		name           string
		primaryResults []*rcpb.SearchReleaseResult
	}{
		{
			name:           "primary returns empty results",
			primaryResults: nil,
		},
		{
			name: "primary returns only invalid release IDs",
			primaryResults: []*rcpb.SearchReleaseResult{
				nil,
				{ReleaseId: 0, Title: "Zero"},
				{ReleaseId: -5, Title: "Negative"},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := snapshotDiscogsLookupCounters()
			s, rec := newLookupTestServer()
			callIdx := 0
			rc := &clientUpdateFakeRCClient{
				searchFn: func(ctx context.Context, req *rcpb.SearchReleasesRequest) (*rcpb.SearchReleasesResponse, error) {
					callIdx++
					if callIdx == 1 {
						return &rcpb.SearchReleasesResponse{Results: tc.primaryResults}, nil
					}
					return &rcpb.SearchReleasesResponse{
						Results: []*rcpb.SearchReleaseResult{
							{ReleaseId: 501, Title: "Vinyl 1", Year: "2020", Formats: []string{"Vinyl"}},
							{ReleaseId: 502, Title: "CD 2", Year: "2021", Formats: []string{"CD"}},
							{ReleaseId: 503, Title: "Cassette 3", Year: "2022", Formats: []string{"Cassette"}},
							{ReleaseId: 504, Title: "Extra 4"},
						},
					}, nil
				},
			}
			item := &bcpb.Item{AlbumId: 9002, BandName: "Fallback Band", AlbumTitle: "Fallback Album"}

			res := s.lookupDiscogsCandidates(context.Background(), rc, item)
			if res.Outcome != outcomeFallback {
				t.Fatalf("Outcome = %q, want %q", res.Outcome, outcomeFallback)
			}
			if len(res.Candidates) != 3 {
				t.Fatalf("len(Candidates) = %d, want 3", len(res.Candidates))
			}
			if len(rc.searchCalls) != 2 {
				t.Fatalf("expected 2 SearchReleases calls, got %d", len(rc.searchCalls))
			}
			if rc.searchCalls[0].GetFormat() != "File" || rc.searchCalls[0].GetPerPage() != 10 {
				t.Fatalf("unexpected primary request: %+v", rc.searchCalls[0])
			}
			if rc.searchCalls[1].GetFormat() != "" || rc.searchCalls[1].GetPerPage() != 10 {
				t.Fatalf("unexpected fallback request: %+v", rc.searchCalls[1])
			}
			if len(rec.messages()) != 0 {
				t.Fatalf("expected no error logs, got %v", rec.messages())
			}
			assertDiscogsLookupCounterDelta(t, before, outcomeFallback)
		})
	}
}

func TestLookupDiscogsCandidates_NoCandidatesFound(t *testing.T) {
	before := snapshotDiscogsLookupCounters()
	s, rec := newLookupTestServer()
	rc := &clientUpdateFakeRCClient{
		searchFn: func(ctx context.Context, req *rcpb.SearchReleasesRequest) (*rcpb.SearchReleasesResponse, error) {
			return &rcpb.SearchReleasesResponse{}, nil
		},
	}
	item := &bcpb.Item{AlbumId: 9003, BandName: "Unknown Artist", AlbumTitle: "Unknown Release"}

	res := s.lookupDiscogsCandidates(context.Background(), rc, item)
	if res.Outcome != outcomeNone {
		t.Fatalf("Outcome = %q, want %q", res.Outcome, outcomeNone)
	}
	if len(res.Candidates) != 0 {
		t.Fatalf("expected 0 candidates, got %+v", res.Candidates)
	}
	if len(rc.searchCalls) != 2 {
		t.Fatalf("expected 2 SearchReleases calls, got %d", len(rc.searchCalls))
	}
	if len(rec.messages()) != 0 {
		t.Fatalf("expected no error logs, got %v", rec.messages())
	}
	assertDiscogsLookupCounterDelta(t, before, outcomeNone)
}

func TestLookupDiscogsCandidates_ErrorsAndCookieSanitization(t *testing.T) {
	const (
		cookie1 = "secret-cookie-primary"
		cookie2 = "secret-cookie-fallback"
	)

	t.Run("primary error stops immediately without running fallback and sanitizes log", func(t *testing.T) {
		before := snapshotDiscogsLookupCounters()
		s, rec := newLookupTestServer()
		rc := &clientUpdateFakeRCClient{
			searchFn: func(ctx context.Context, req *rcpb.SearchReleasesRequest) (*rcpb.SearchReleasesResponse, error) {
				return nil, fmt.Errorf("upstream failure with %s and %s", cookie1, cookie2)
			},
		}
		item := &bcpb.Item{AlbumId: 9004, BandName: "Band", AlbumTitle: "Album"}

		res := s.lookupDiscogsCandidates(context.Background(), rc, item, cookie1, cookie2)
		if res.Outcome != outcomeError {
			t.Fatalf("Outcome = %q, want %q", res.Outcome, outcomeError)
		}
		if len(res.Candidates) != 0 {
			t.Fatalf("expected 0 candidates, got %+v", res.Candidates)
		}
		if len(rc.searchCalls) != 1 {
			t.Fatalf("expected exactly 1 SearchReleases call (fallback must not run), got %d", len(rc.searchCalls))
		}
		logs := rec.messages()
		if len(logs) != 1 {
			t.Fatalf("expected 1 sanitized error log, got %d: %v", len(logs), logs)
		}
		if strings.Contains(logs[0], cookie1) || strings.Contains(logs[0], cookie2) {
			t.Fatalf("raw cookie leaked in log: %q", logs[0])
		}
		if !strings.Contains(logs[0], "[REDACTED]") {
			t.Fatalf("expected [REDACTED] in sanitized log: %q", logs[0])
		}
		assertDiscogsLookupCounterDelta(t, before, outcomeError)
	})

	t.Run("primary empty plus fallback error returns outcomeError and sanitizes log", func(t *testing.T) {
		before := snapshotDiscogsLookupCounters()
		s, rec := newLookupTestServer()
		callIdx := 0
		rc := &clientUpdateFakeRCClient{
			searchFn: func(ctx context.Context, req *rcpb.SearchReleasesRequest) (*rcpb.SearchReleasesResponse, error) {
				callIdx++
				if callIdx == 1 {
					return &rcpb.SearchReleasesResponse{}, nil
				}
				return nil, fmt.Errorf("fallback failure %s", cookie2)
			},
		}
		item := &bcpb.Item{AlbumId: 9005, BandName: "Band", AlbumTitle: "Album"}

		res := s.lookupDiscogsCandidates(context.Background(), rc, item, cookie1, cookie2)
		if res.Outcome != outcomeError {
			t.Fatalf("Outcome = %q, want %q", res.Outcome, outcomeError)
		}
		if len(rc.searchCalls) != 2 {
			t.Fatalf("expected 2 SearchReleases calls, got %d", len(rc.searchCalls))
		}
		logs := rec.messages()
		if len(logs) != 1 {
			t.Fatalf("expected 1 sanitized error log, got %d: %v", len(logs), logs)
		}
		if strings.Contains(logs[0], cookie2) || !strings.Contains(logs[0], "[REDACTED]") {
			t.Fatalf("expected sanitized cookie in log, got %q", logs[0])
		}
		assertDiscogsLookupCounterDelta(t, before, outcomeError)
	})
}

func TestLookupDiscogsCandidates_SharedTimeoutBudget(t *testing.T) {
	origTimeout := discogsLookupTimeout
	discogsLookupTimeout = 50 * time.Millisecond
	t.Cleanup(func() {
		discogsLookupTimeout = origTimeout
	})

	t.Run("hanging primary respects timeout and does not run fallback", func(t *testing.T) {
		before := snapshotDiscogsLookupCounters()
		s, rec := newLookupTestServer()
		rc := &clientUpdateFakeRCClient{
			searchFn: func(ctx context.Context, req *rcpb.SearchReleasesRequest) (*rcpb.SearchReleasesResponse, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			},
		}
		item := &bcpb.Item{AlbumId: 9006, BandName: "Slow", AlbumTitle: "Primary"}

		start := time.Now()
		res := s.lookupDiscogsCandidates(context.Background(), rc, item)
		elapsed := time.Since(start)

		if res.Outcome != outcomeError {
			t.Fatalf("Outcome = %q, want %q", res.Outcome, outcomeError)
		}
		if elapsed >= 500*time.Millisecond {
			t.Fatalf("lookup took too long (%v), expected bounded ~50ms timeout", elapsed)
		}
		if len(rc.searchCalls) != 1 {
			t.Fatalf("expected 1 SearchReleases call, got %d", len(rc.searchCalls))
		}
		if len(rec.messages()) != 1 {
			t.Fatalf("expected 1 error log, got %v", rec.messages())
		}
		assertDiscogsLookupCounterDelta(t, before, outcomeError)
	})

	t.Run("slow primary plus hanging fallback share single deadline", func(t *testing.T) {
		before := snapshotDiscogsLookupCounters()
		s, rec := newLookupTestServer()
		var deadlines []time.Time
		callIdx := 0
		rc := &clientUpdateFakeRCClient{
			searchFn: func(ctx context.Context, req *rcpb.SearchReleasesRequest) (*rcpb.SearchReleasesResponse, error) {
				callIdx++
				dl, ok := ctx.Deadline()
				if !ok {
					t.Fatal("expected lookupCtx to have a deadline")
				}
				deadlines = append(deadlines, dl)
				if callIdx == 1 {
					select {
					case <-time.After(25 * time.Millisecond):
						return &rcpb.SearchReleasesResponse{}, nil
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				}
				<-ctx.Done()
				return nil, ctx.Err()
			},
		}
		item := &bcpb.Item{AlbumId: 9007, BandName: "Slow", AlbumTitle: "Fallback"}

		start := time.Now()
		res := s.lookupDiscogsCandidates(context.Background(), rc, item)
		elapsed := time.Since(start)

		if res.Outcome != outcomeError {
			t.Fatalf("Outcome = %q, want %q", res.Outcome, outcomeError)
		}
		if elapsed >= 500*time.Millisecond {
			t.Fatalf("lookup took too long (%v), expected shared ~50ms deadline", elapsed)
		}
		if len(rc.searchCalls) != 2 {
			t.Fatalf("expected 2 SearchReleases calls, got %d", len(rc.searchCalls))
		}
		if len(deadlines) != 2 || !deadlines[0].Equal(deadlines[1]) {
			t.Fatalf("expected identical shared deadline across primary and fallback, got %v", deadlines)
		}
		if len(rec.messages()) != 1 {
			t.Fatalf("expected 1 error log, got %v", rec.messages())
		}
		assertDiscogsLookupCounterDelta(t, before, outcomeError)
	})
}

func TestLookupDiscogsCandidates_MetadataEdgeCases(t *testing.T) {
	t.Run("nil item, nil rc, or empty metadata skips with 0 RPC calls", func(t *testing.T) {
		s, _ := newLookupTestServer()
		cases := []struct {
			name string
			rc   rcClient
			item *bcpb.Item
		}{
			{
				name: "nil item",
				rc:   &clientUpdateFakeRCClient{},
				item: nil,
			},
			{
				name: "nil rc",
				rc:   nil,
				item: &bcpb.Item{AlbumId: 1, BandName: "Artist", AlbumTitle: "Album"},
			},
			{
				name: "empty and whitespace metadata",
				rc:   &clientUpdateFakeRCClient{},
				item: &bcpb.Item{AlbumId: 2, BandName: " \t\n ", AlbumTitle: "   "},
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				before := snapshotDiscogsLookupCounters()
				res := s.lookupDiscogsCandidates(context.Background(), tc.rc, tc.item)
				if res.Outcome != outcomeSkipped {
					t.Fatalf("Outcome = %q, want %q", res.Outcome, outcomeSkipped)
				}
				if fake, ok := tc.rc.(*clientUpdateFakeRCClient); ok && len(fake.searchCalls) != 0 {
					t.Fatalf("expected 0 RPC calls, got %d", len(fake.searchCalls))
				}
				assertDiscogsLookupCounterDelta(t, before, outcomeSkipped)
			})
		}
	})

	t.Run("only BandName populated invokes SearchReleases cleanly", func(t *testing.T) {
		before := snapshotDiscogsLookupCounters()
		s, _ := newLookupTestServer()
		rc := &clientUpdateFakeRCClient{
			searchFn: func(ctx context.Context, req *rcpb.SearchReleasesRequest) (*rcpb.SearchReleasesResponse, error) {
				return &rcpb.SearchReleasesResponse{
					Results: []*rcpb.SearchReleaseResult{{ReleaseId: 801, Title: "Only Artist Match"}},
				}, nil
			},
		}
		item := &bcpb.Item{AlbumId: 9008, BandName: "  Solo Artist  ", AlbumTitle: "   "}

		res := s.lookupDiscogsCandidates(context.Background(), rc, item)
		if res.Outcome != outcomeDigital {
			t.Fatalf("Outcome = %q, want %q", res.Outcome, outcomeDigital)
		}
		if len(rc.searchCalls) != 1 {
			t.Fatalf("expected 1 RPC call, got %d", len(rc.searchCalls))
		}
		if rc.searchCalls[0].GetArtist() != "Solo Artist" || rc.searchCalls[0].GetReleaseTitle() != "" {
			t.Fatalf("unexpected request fields: %+v", rc.searchCalls[0])
		}
		assertDiscogsLookupCounterDelta(t, before, outcomeDigital)
	})

	t.Run("only AlbumTitle populated invokes SearchReleases cleanly", func(t *testing.T) {
		before := snapshotDiscogsLookupCounters()
		s, _ := newLookupTestServer()
		rc := &clientUpdateFakeRCClient{
			searchFn: func(ctx context.Context, req *rcpb.SearchReleasesRequest) (*rcpb.SearchReleasesResponse, error) {
				return &rcpb.SearchReleasesResponse{
					Results: []*rcpb.SearchReleaseResult{{ReleaseId: 802, Title: "Only Album Match"}},
				}, nil
			},
		}
		item := &bcpb.Item{AlbumId: 9009, BandName: "", AlbumTitle: "  Solo Album  "}

		res := s.lookupDiscogsCandidates(context.Background(), rc, item)
		if res.Outcome != outcomeDigital {
			t.Fatalf("Outcome = %q, want %q", res.Outcome, outcomeDigital)
		}
		if len(rc.searchCalls) != 1 {
			t.Fatalf("expected 1 RPC call, got %d", len(rc.searchCalls))
		}
		if rc.searchCalls[0].GetArtist() != "" || rc.searchCalls[0].GetReleaseTitle() != "Solo Album" {
			t.Fatalf("unexpected request fields: %+v", rc.searchCalls[0])
		}
		assertDiscogsLookupCounterDelta(t, before, outcomeDigital)
	})

	t.Run("metadata longer than 500 runes is truncated on UTF-8 rune boundaries", func(t *testing.T) {
		before := snapshotDiscogsLookupCounters()
		s, _ := newLookupTestServer()
		rc := &clientUpdateFakeRCClient{
			searchFn: func(ctx context.Context, req *rcpb.SearchReleasesRequest) (*rcpb.SearchReleasesResponse, error) {
				return &rcpb.SearchReleasesResponse{}, nil
			},
		}
		item := &bcpb.Item{
			AlbumId:    9010,
			BandName:   strings.Repeat("é", 600),
			AlbumTitle: strings.Repeat("🎵", 600),
		}

		res := s.lookupDiscogsCandidates(context.Background(), rc, item)
		if res.Outcome != outcomeNone {
			t.Fatalf("Outcome = %q, want %q", res.Outcome, outcomeNone)
		}
		if len(rc.searchCalls) != 2 {
			t.Fatalf("expected 2 RPC calls, got %d", len(rc.searchCalls))
		}
		for i, req := range rc.searchCalls {
			if rcArtist := utf8.RuneCountInString(req.GetArtist()); rcArtist != maxSearchFieldRunes || !utf8.ValidString(req.GetArtist()) {
				t.Fatalf("call %d Artist runeCount=%d valid=%v", i, rcArtist, utf8.ValidString(req.GetArtist()))
			}
			if rcTitle := utf8.RuneCountInString(req.GetReleaseTitle()); rcTitle != maxSearchFieldRunes || !utf8.ValidString(req.GetReleaseTitle()) {
				t.Fatalf("call %d ReleaseTitle runeCount=%d valid=%v", i, rcTitle, utf8.ValidString(req.GetReleaseTitle()))
			}
		}
		assertDiscogsLookupCounterDelta(t, before, outcomeNone)
	})
}
