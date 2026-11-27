package main

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	bcpb "github.com/brotherlogic/bandcamplib/proto"
	rcpb "github.com/brotherlogic/recordcollection/proto"
)

// discogsLookupTimeout bounds the total duration of Discogs candidate lookup;
// declared as a package var so unit tests can override it.
var discogsLookupTimeout = 10 * time.Second

const (
	discogsSearchPerPage        int32 = 10
	maxDiscogsCandidates              = 3
	maxSearchFieldRunes               = 500
	maxRenderedFieldRunes             = 200
	discogsReleaseBaseURL             = "https://www.discogs.com/release/"
	maxBandcampURLBytes               = 2048
	bandcampLinkUnavailableText       = "Bandcamp: Link unavailable"
)

type bandcampURLOutcome string

const (
	bandcampURLAvailable   bandcampURLOutcome = "available"
	bandcampURLUnavailable bandcampURLOutcome = "unavailable"
	bandcampURLInvalid     bandcampURLOutcome = "invalid"
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

func isPrivateDownloadPath(p string) bool {
	lp := strings.ToLower(p)
	return lp == "/download" || strings.HasPrefix(lp, "/download/")
}

func isPrivateDownloadQueryKey(k string) bool {
	switch strings.ToLower(k) {
	case "sig", "sitem_id", "payment_id":
		return true
	default:
		return false
	}
}

func hasPrivateDownloadQueryKey(u *url.URL) bool {
	for k := range u.Query() {
		if isPrivateDownloadQueryKey(k) {
			return true
		}
	}
	// Also scan RawQuery directly so malformed percent-escapes in another pair
	// cannot cause url.Query() to skip or miss a forbidden signature parameter.
	for _, part := range strings.FieldsFunc(u.RawQuery, func(r rune) bool { return r == '&' || r == ';' }) {
		rawKey := part
		if idx := strings.IndexByte(part, '='); idx >= 0 {
			rawKey = part[:idx]
		}
		if isPrivateDownloadQueryKey(rawKey) {
			return true
		}
		if unescaped, err := url.QueryUnescape(rawKey); err == nil && isPrivateDownloadQueryKey(unescaped) {
			return true
		}
	}
	return false
}

// sanitizeBandcampURL validates and sanitizes a public Bandcamp release URL for
// safe Markdown rendering in missing-mapping GitHub issues.
func sanitizeBandcampURL(raw string) (string, bandcampURLOutcome) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", bandcampURLUnavailable
	}
	if len(trimmed) > maxBandcampURLBytes || !utf8.ValidString(trimmed) {
		return "", bandcampURLInvalid
	}

	for _, r := range trimmed {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return "", bandcampURLInvalid
		}
		switch r {
		case '<', '>', '`', '[', ']', '"', '\\':
			return "", bandcampURLInvalid
		}
	}

	u, err := url.Parse(trimmed)
	if err != nil {
		return "", bandcampURLInvalid
	}
	scheme := strings.ToLower(u.Scheme)
	if (scheme != "http" && scheme != "https") || u.Host == "" || u.Hostname() == "" || u.User != nil {
		return "", bandcampURLInvalid
	}

	if isPrivateDownloadPath(u.Path) || isPrivateDownloadPath(u.EscapedPath()) ||
		(u.Path != "" && isPrivateDownloadPath(path.Clean(u.Path))) || hasPrivateDownloadQueryKey(u) {
		return "", bandcampURLInvalid
	}

	// Percent-encode parentheses so release slugs with parens do not break GitHub autolinks.
	rendered := strings.NewReplacer("(", "%28", ")", "%29").Replace(u.String())
	if len(rendered) > maxBandcampURLBytes {
		return "", bandcampURLInvalid
	}
	return rendered, bandcampURLAvailable
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

// lookupDiscogsCandidates performs a bounded, two-stage Discogs search (digital File
// primary followed by optional non-digital fallback) and records Prometheus outcome metrics.
func (s *Server) lookupDiscogsCandidates(ctx context.Context, rc rcClient, item *bcpb.Item, cookies ...string) discogsCandidateResult {
	if item == nil || rc == nil {
		discogsCandidateLookupTotal.WithLabelValues(string(outcomeSkipped)).Inc()
		return discogsCandidateResult{Outcome: outcomeSkipped}
	}

	artist := truncateRunes(item.GetBandName(), maxSearchFieldRunes)
	albumTitle := truncateRunes(item.GetAlbumTitle(), maxSearchFieldRunes)
	if artist == "" && albumTitle == "" {
		discogsCandidateLookupTotal.WithLabelValues(string(outcomeSkipped)).Inc()
		return discogsCandidateResult{Outcome: outcomeSkipped}
	}

	lookupCtx, cancel := context.WithTimeout(ctx, discogsLookupTimeout)
	defer cancel()

	resp, err := rc.SearchReleases(lookupCtx, &rcpb.SearchReleasesRequest{
		Artist:       artist,
		ReleaseTitle: albumTitle,
		Format:       "File",
		PerPage:      discogsSearchPerPage,
	})
	if err != nil {
		if s != nil {
			s.log(ctx, fmt.Sprintf("Discogs digital candidate lookup failed for album %v: %v", item.GetAlbumId(), sanitizeError(err, cookies...)))
		}
		discogsCandidateLookupTotal.WithLabelValues(string(outcomeError)).Inc()
		return discogsCandidateResult{Outcome: outcomeError}
	}
	if candidates := normalizeDiscogsCandidates(resp.GetResults(), maxDiscogsCandidates); len(candidates) > 0 {
		discogsCandidateLookupTotal.WithLabelValues(string(outcomeDigital)).Inc()
		return discogsCandidateResult{Outcome: outcomeDigital, Candidates: candidates}
	}

	fallbackResp, err := rc.SearchReleases(lookupCtx, &rcpb.SearchReleasesRequest{
		Artist:       artist,
		ReleaseTitle: albumTitle,
		PerPage:      discogsSearchPerPage,
	})
	if err != nil {
		if s != nil {
			s.log(ctx, fmt.Sprintf("Discogs fallback candidate lookup failed for album %v: %v", item.GetAlbumId(), sanitizeError(err, cookies...)))
		}
		discogsCandidateLookupTotal.WithLabelValues(string(outcomeError)).Inc()
		return discogsCandidateResult{Outcome: outcomeError}
	}
	if candidates := normalizeDiscogsCandidates(fallbackResp.GetResults(), maxDiscogsCandidates); len(candidates) > 0 {
		discogsCandidateLookupTotal.WithLabelValues(string(outcomeFallback)).Inc()
		return discogsCandidateResult{Outcome: outcomeFallback, Candidates: candidates}
	}

	discogsCandidateLookupTotal.WithLabelValues(string(outcomeNone)).Inc()
	return discogsCandidateResult{Outcome: outcomeNone}
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

func buildMissingMappingIssueBody(item *bcpb.Item, res discogsCandidateResult, cookies ...string) string {
	body := fmt.Sprintf(
		"%v - %v (%v) is missing a mapping",
		item.GetBandName(),
		item.GetAlbumTitle(),
		item.GetAlbumId(),
	)

	if section := formatDiscogsCandidatesSection(item.GetAlbumId(), res); section != "" {
		body += "\n\n" + section
	}
	return redactCookie(body, cookies...)
}
