package main

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	bcpb "github.com/brotherlogic/bandcamplib/proto"
	rcpb "github.com/brotherlogic/recordcollection/proto"
)

// discogsLookupTimeout bounds the total duration of Discogs candidate lookup;
// declared as a package var so unit tests can override it.
var discogsLookupTimeout = 10 * time.Second

const (
	discogsSearchPerPage  int32 = 10
	maxDiscogsCandidates        = 3
	maxSearchFieldRunes         = 500
	maxRenderedFieldRunes       = 200
	discogsReleaseBaseURL       = "https://www.discogs.com/release/"
)

type discogsLookupOutcome string

const (
	outcomeDigital  discogsLookupOutcome = "digital"
	outcomeFallback discogsLookupOutcome = "fallback"
	outcomeNone     discogsLookupOutcome = "none"
	outcomeSkipped  discogsLookupOutcome = "skipped"
	outcomeError    discogsLookupOutcome = "error"
)

type discogsCandidate struct {
	ReleaseID int32
	Title     string
	Year      string
	Formats   []string
	URL       string
}

type discogsCandidateResult struct {
	Outcome    discogsLookupOutcome
	Candidates []discogsCandidate
}

// truncateRunes trims surrounding whitespace and truncates s cleanly on UTF-8 rune boundaries.
func truncateRunes(s string, max int) string {
	trimmed := strings.TrimSpace(s)
	if max <= 0 || trimmed == "" {
		return ""
	}
	runes := []rune(trimmed)
	if len(runes) <= max {
		return trimmed
	}
	return strings.TrimSpace(string(runes[:max]))
}

// sanitizeMarkdownInline collapses control/whitespace runs into single spaces,
// escapes Markdown-significant characters, and truncates cleanly within maxRunes.
func sanitizeMarkdownInline(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}

	var collapsed strings.Builder
	collapsed.Grow(len(s))
	prevSpace := true // trim leading whitespace naturally
	for _, r := range s {
		if r == '\r' || r == '\n' || r == '\t' || unicode.IsControl(r) || unicode.IsSpace(r) {
			if !prevSpace {
				collapsed.WriteByte(' ')
				prevSpace = true
			}
			continue
		}
		collapsed.WriteRune(r)
		prevSpace = false
	}
	clean := strings.TrimSpace(collapsed.String())
	if clean == "" {
		return ""
	}

	var out strings.Builder
	out.Grow(len(clean))
	runeCount := 0
	for _, r := range clean {
		needsEscape := r == '\\' || r == '`' || r == '[' || r == ']' || r == '*' || r == '_'
		cost := 1
		if needsEscape {
			cost = 2
		}
		// Never split an escape sequence across the maxRunes boundary.
		if runeCount+cost > maxRunes {
			break
		}
		if needsEscape {
			out.WriteByte('\\')
		}
		out.WriteRune(r)
		runeCount += cost
	}
	return strings.TrimSpace(out.String())
}

func sanitizeCandidateURL(raw string, releaseID int32) string {
	var b strings.Builder
	b.Grow(len(raw))
	for _, r := range raw {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			continue
		}
		b.WriteRune(r)
	}
	if cleaned := b.String(); cleaned != "" {
		return cleaned
	}
	if releaseID > 0 {
		return fmt.Sprintf("%s%d", discogsReleaseBaseURL, releaseID)
	}
	return ""
}

// normalizeDiscogsCandidates filters invalid/duplicate results, sanitizes fields,
// and caps the returned slice at limit.
func normalizeDiscogsCandidates(results []*rcpb.SearchReleaseResult, limit int) []discogsCandidate {
	if limit <= 0 {
		return nil
	}

	seen := make(map[int32]struct{}, limit)
	candidates := make([]discogsCandidate, 0, limit)
	for _, res := range results {
		if res == nil {
			continue
		}
		id := res.GetReleaseId()
		if id <= 0 {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}

		title := sanitizeMarkdownInline(res.GetTitle(), maxRenderedFieldRunes)
		if title == "" {
			title = fmt.Sprintf("Release #%d", id)
		}
		year := sanitizeMarkdownInline(res.GetYear(), maxRenderedFieldRunes)

		var formats []string
		for _, f := range res.GetFormats() {
			if cleaned := sanitizeMarkdownInline(f, maxRenderedFieldRunes); cleaned != "" {
				formats = append(formats, cleaned)
			}
		}

		candidates = append(candidates, discogsCandidate{
			ReleaseID: id,
			Title:     title,
			Year:      year,
			Formats:   formats,
			URL:       sanitizeCandidateURL(res.GetUrl(), id),
		})
		if len(candidates) >= limit {
			break
		}
	}
	return candidates
}

func formatCandidateMetadata(c discogsCandidate) string {
	year := strings.TrimSpace(c.Year)
	var cleanFormats []string
	for _, f := range c.Formats {
		if trimmed := strings.TrimSpace(f); trimmed != "" {
			cleanFormats = append(cleanFormats, trimmed)
		}
	}
	formats := strings.Join(cleanFormats, ", ")

	switch {
	case year != "" && formats != "":
		return fmt.Sprintf(" (%s — %s)", year, formats)
	case year != "":
		return fmt.Sprintf(" (%s)", year)
	case formats != "":
		return fmt.Sprintf(" (%s)", formats)
	default:
		return ""
	}
}

func formatCandidateLine(c discogsCandidate) string {
	title := c.Title
	if title == "" && c.ReleaseID > 0 {
		title = fmt.Sprintf("Release #%d", c.ReleaseID)
	}
	url := c.URL
	if url == "" && c.ReleaseID > 0 {
		url = fmt.Sprintf("%s%d", discogsReleaseBaseURL, c.ReleaseID)
	}
	meta := formatCandidateMetadata(c)
	if url != "" {
		return fmt.Sprintf("- **%s**%s — %s", title, meta, url)
	}
	return fmt.Sprintf("- **%s**%s", title, meta)
}

func formatDiscogsCandidatesSection(albumID int64, res discogsCandidateResult) string {
	const noCandidatesText = "### Discogs Candidate Releases\nNo candidate Discogs releases were found — a new Discogs submission is likely needed."

	switch res.Outcome {
	case outcomeDigital:
		if len(res.Candidates) == 0 {
			return noCandidatesText
		}
		var lines []string
		lines = append(lines, "### Candidate Discogs Digital Releases")
		for _, c := range res.Candidates {
			lines = append(lines, formatCandidateLine(c))
			if c.ReleaseID > 0 {
				lines = append(lines, fmt.Sprintf("  ```sh\n  bandcampserver_cli mapping -bandcamp %d -discogs %d\n  ```", albumID, c.ReleaseID))
			}
		}
		return strings.Join(lines, "\n")
	case outcomeFallback:
		if len(res.Candidates) == 0 {
			return noCandidatesText
		}
		var lines []string
		lines = append(lines,
			"### Discogs Candidate Releases (Reference Only)",
			"No digital (`File`) Discogs release was found — a new digital Discogs submission is likely needed. Existing non-digital releases for reference:",
		)
		for _, c := range res.Candidates {
			lines = append(lines, formatCandidateLine(c))
		}
		return strings.Join(lines, "\n")
	case outcomeNone:
		return noCandidatesText
	case outcomeError:
		return "Note: Discogs candidate lookup failed; please search Discogs manually."
	case outcomeSkipped:
		return "Note: Discogs candidate lookup was skipped because Bandcamp artist and album metadata were empty."
	default:
		return ""
	}
}

func buildMissingMappingIssueBody(item *bcpb.Item, wavURL string, maxFailures int32, res discogsCandidateResult, cookies ...string) string {
	var body string
	if wavURL != "" {
		body = fmt.Sprintf(
			"%v - %v (%v) is missing a mapping\n\nDirect WAV download link: %s\n\nNote: Bandcamp direct download links are time-limited and may expire over time.",
			item.GetBandName(),
			item.GetAlbumTitle(),
			item.GetAlbumId(),
			wavURL,
		)
	} else {
		body = fmt.Sprintf(
			"%v - %v (%v) is missing a mapping\n\nNote: WAV download link generation failed after %d attempts.",
			item.GetBandName(),
			item.GetAlbumTitle(),
			item.GetAlbumId(),
			maxFailures,
		)
	}

	if section := formatDiscogsCandidatesSection(item.GetAlbumId(), res); section != "" {
		body += "\n\n" + section
	}
	return redactCookie(body, cookies...)
}
