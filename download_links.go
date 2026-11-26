package main

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/brotherlogic/bandcamplib"
	bcpb "github.com/brotherlogic/bandcamplib/proto"
	pb "github.com/brotherlogic/bandcampserver/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const bandcampUserID int32 = 873256

type redactedError struct {
	msg string
	err error
}

func (e *redactedError) Error() string {
	return e.msg
}

func (e *redactedError) Unwrap() error {
	return e.err
}

func collectCookieSecrets(cookies ...string) []string {
	seen := make(map[string]struct{}, len(cookies))
	var secrets []string
	for _, c := range cookies {
		trimmed := strings.TrimSpace(c)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; !ok {
			seen[trimmed] = struct{}{}
			secrets = append(secrets, trimmed)
		}
	}
	return secrets
}

func redactCookie(msg string, cookies ...string) string {
	out := msg
	for _, secret := range collectCookieSecrets(cookies...) {
		out = strings.ReplaceAll(out, secret, "[REDACTED]")
	}
	return out
}

func sanitizeError(err error, cookies ...string) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	redacted := redactCookie(msg, cookies...)
	if redacted == msg {
		return err
	}
	return &redactedError{
		msg: redacted,
		err: err,
	}
}

// normalizeCookieInput trims whitespace, strips an optional case-insensitive
// leading "identity=" prefix, and rejects empty results with InvalidArgument.
func normalizeCookieInput(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	const prefix = "identity="
	if len(trimmed) >= len(prefix) && strings.EqualFold(trimmed[:len(prefix)], prefix) {
		trimmed = strings.TrimSpace(trimmed[len(prefix):])
	}
	if trimmed == "" {
		return "", status.Error(codes.InvalidArgument, "identity cookie must not be empty")
	}
	return trimmed, nil
}

func (s *Server) ensureAuthAlert(ctx context.Context, config *pb.Config, reason string) error {
	if config == nil {
		return status.Error(codes.InvalidArgument, "config must not be nil")
	}
	if config.GetAuthAlertIssueId() > 0 {
		return nil
	}

	safeReason := strings.TrimSpace(redactCookie(reason, config.GetIdentityCookie()))
	if safeReason == "" {
		safeReason = "Bandcamp session cookie is missing or expired."
	}

	body := fmt.Sprintf(
		"%s\n\nTo restore Bandcamp downloads:\n"+
			"1. Open https://bandcamp.com in Chrome while signed in to your Bandcamp account.\n"+
			"2. Open Chrome DevTools (F12 or Cmd+Opt+I / Ctrl+Shift+I) -> Application -> Storage -> Cookies -> https://bandcamp.com.\n"+
			"3. Copy the Value of the `identity` cookie.\n"+
			"4. Run:\n\n```sh\nbandcampserver_cli cookie -cookie '<value>'\n```\n",
		safeReason,
	)

	issue, err := s.getIssueManager().ImmediateIssue(ctx, "Bandcamp session cookie missing or expired", body, false, false)
	if err != nil {
		return fmt.Errorf("create Bandcamp auth alert issue: %w", sanitizeError(err, config.GetIdentityCookie()))
	}

	config.AuthAlertIssueId = issue.GetNumber()
	updateAuthMetrics(config)

	if err := s.saveConfig(ctx, config); err != nil {
		return fmt.Errorf("persist Bandcamp auth alert issue ID: %w", sanitizeError(err, config.GetIdentityCookie()))
	}
	return nil
}

func isIssueAlreadyClosedOrNotFound(err error) bool {
	if err == nil {
		return false
	}
	if status.Code(err) == codes.NotFound {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "not found") ||
		strings.Contains(msg, "already closed") ||
		strings.Contains(msg, "404") ||
		strings.Contains(msg, "does not exist")
}

func (s *Server) clearAuthAlert(ctx context.Context, config *pb.Config) error {
	if config == nil || config.GetAuthAlertIssueId() <= 0 {
		return nil
	}

	err := s.getIssueManager().DeleteIssue(ctx, config.GetAuthAlertIssueId())
	if err != nil && !isIssueAlreadyClosedOrNotFound(err) {
		return fmt.Errorf("delete Bandcamp auth alert issue: %w", sanitizeError(err, config.GetIdentityCookie()))
	}

	config.AuthAlertIssueId = 0
	updateAuthMetrics(config)
	return nil
}

func (s *Server) getBandcampClientFactory() bandcampClientFactory {
	if s.bcFactory != nil {
		return s.bcFactory
	}
	return defaultBandcampClientFactory
}

type bandcampCookieTracker struct {
	mu             sync.Mutex
	initialCookie  string
	callbackCookie string
	client         bandcampClient
}

func (t *bandcampCookieTracker) onRefresh(newCookie string) {
	trimmed := strings.TrimSpace(newCookie)
	if trimmed == "" {
		return
	}
	t.mu.Lock()
	t.callbackCookie = trimmed
	t.mu.Unlock()
}

func (t *bandcampCookieTracker) detectRefreshedCookie(current string) string {
	t.mu.Lock()
	cb := t.callbackCookie
	t.callbackCookie = ""
	t.mu.Unlock()

	if cb != "" && cb != current {
		return cb
	}

	if t.client != nil {
		clientCookie := strings.TrimSpace(t.client.IdentityCookie())
		if clientCookie != "" && clientCookie != current && clientCookie != t.initialCookie {
			return clientCookie
		}
	}
	return ""
}

func (s *Server) newTrackedBandcampClient(config *pb.Config) (*bandcampCookieTracker, error) {
	initial := strings.TrimSpace(config.GetIdentityCookie())
	tracker := &bandcampCookieTracker{
		initialCookie: initial,
	}
	client, err := s.getBandcampClientFactory()(&bcpb.Config{
		User:           bandcampUserID,
		Token:          config.GetToken(),
		IdentityCookie: config.GetIdentityCookie(),
	}, tracker.onRefresh)
	if err != nil {
		return nil, sanitizeError(err, config.GetIdentityCookie())
	}
	tracker.client = client
	return tracker, nil
}

func (s *Server) persistRefreshedCookie(ctx context.Context, config *pb.Config, tracker *bandcampCookieTracker) error {
	if config == nil || tracker == nil {
		return nil
	}
	previousCookie := config.GetIdentityCookie()
	refreshed := tracker.detectRefreshedCookie(previousCookie)
	if refreshed == "" || refreshed == previousCookie {
		return nil
	}

	config.IdentityCookie = refreshed
	config.LastCookieRefresh = s.nowTime().Unix()
	updateAuthMetrics(config)

	if err := s.saveConfig(ctx, config); err != nil {
		return fmt.Errorf("persist refreshed Bandcamp cookie: %w", sanitizeError(err, previousCookie, refreshed))
	}
	return nil
}

func (s *Server) runBandcampCall(ctx context.Context, config *pb.Config, tracker *bandcampCookieTracker, fn func(client bandcampClient) error) error {
	previousCookie := config.GetIdentityCookie()
	callErr := fn(tracker.client)
	persistErr := s.persistRefreshedCookie(ctx, config, tracker)
	currentCookie := config.GetIdentityCookie()

	sanitizedCallErr := sanitizeError(callErr, previousCookie, currentCookie)
	if persistErr != nil {
		if sanitizedCallErr != nil {
			return fmt.Errorf("%w (after Bandcamp call error: %w)", persistErr, sanitizedCallErr)
		}
		return persistErr
	}
	return sanitizedCallErr
}

func (s *Server) withBandcampClient(ctx context.Context, config *pb.Config, fn func(client bandcampClient) error) error {
	tracker, err := s.newTrackedBandcampClient(config)
	if err != nil {
		return err
	}
	return s.runBandcampCall(ctx, config, tracker, fn)
}

func (s *Server) resolveWAVLinkForItem(ctx context.Context, config *pb.Config, item *bcpb.Item) (string, error) {
	if config == nil || strings.TrimSpace(config.GetIdentityCookie()) == "" {
		return "", bandcamplib.ErrAuthenticationRequired
	}
	if item == nil {
		return "", bandcamplib.ErrDownloadUnavailable
	}

	tracker, err := s.newTrackedBandcampClient(config)
	if err != nil {
		return "", err
	}

	redownloadURL := strings.TrimSpace(item.GetRedownloadUrl())
	if redownloadURL == "" {
		var refreshedItems []*bcpb.Item
		err := s.runBandcampCall(ctx, config, tracker, func(client bandcampClient) error {
			var retrieveErr error
			refreshedItems, retrieveErr = client.RetrieveItems(ctx)
			return retrieveErr
		})
		if err != nil {
			return "", err
		}

		config.Items = refreshedItems
		if err := s.saveConfig(ctx, config); err != nil {
			return "", fmt.Errorf("persist refreshed Bandcamp items: %w", sanitizeError(err, config.GetIdentityCookie()))
		}

		for _, refreshed := range refreshedItems {
			if refreshed.GetAlbumId() == item.GetAlbumId() {
				redownloadURL = strings.TrimSpace(refreshed.GetRedownloadUrl())
				break
			}
		}
		if redownloadURL == "" {
			return "", bandcamplib.ErrDownloadUnavailable
		}
	}

	var wavURL string
	err = s.runBandcampCall(ctx, config, tracker, func(client bandcampClient) error {
		var dlErr error
		wavURL, dlErr = client.GetDownloadURLForRedownloadURL(ctx, redownloadURL, bandcamplib.FormatWAV)
		return dlErr
	})
	if err != nil {
		return "", err
	}

	wavURL = strings.TrimSpace(wavURL)
	if wavURL == "" {
		return "", bandcamplib.ErrDownloadUnavailable
	}
	return wavURL, nil
}
