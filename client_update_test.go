package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brotherlogic/bandcamplib"
	bcpb "github.com/brotherlogic/bandcamplib/proto"
	pb "github.com/brotherlogic/bandcampserver/proto"
	rcgd "github.com/brotherlogic/godiscogs/proto"
	rcpb "github.com/brotherlogic/recordcollection/proto"
)

type clientUpdateFakeRCClient struct {
	mu           sync.Mutex
	getRecordErr error
	folderID     int32
	hasFileFmt   bool
	queryResp    *rcpb.QueryRecordsResponse
	queryErr     error
	addCalls     int
}

func (f *clientUpdateFakeRCClient) GetRecord(ctx context.Context, req *rcpb.GetRecordRequest) (*rcpb.GetRecordResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getRecordErr != nil {
		return nil, f.getRecordErr
	}
	var formats []*rcgd.Format
	if f.hasFileFmt {
		formats = append(formats, &rcgd.Format{Name: "File"})
	}
	return &rcpb.GetRecordResponse{
		Record: &rcpb.Record{
			Release: &rcgd.Release{
				Id:       1001,
				FolderId: f.folderID,
				Formats:  formats,
			},
			Metadata: &rcpb.ReleaseMetadata{
				GoalFolder: f.folderID,
			},
		},
	}, nil
}

func (f *clientUpdateFakeRCClient) QueryRecords(ctx context.Context, req *rcpb.QueryRecordsRequest) (*rcpb.QueryRecordsResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.queryErr != nil {
		return nil, f.queryErr
	}
	if f.queryResp != nil {
		return f.queryResp, nil
	}
	return &rcpb.QueryRecordsResponse{}, nil
}

func (f *clientUpdateFakeRCClient) AddRecord(ctx context.Context, req *rcpb.AddRecordRequest) (*rcpb.AddRecordResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addCalls++
	return &rcpb.AddRecordResponse{}, nil
}

type clientUpdateFakeBandcampClient struct {
	identityCookie string
	onRefresh      func(string)
	retrieveFn     func(ctx context.Context, onRefresh func(string)) ([]*bcpb.Item, error)
	downloadFn     func(ctx context.Context, redownloadURL string, format bandcamplib.Format, onRefresh func(string)) (string, error)
}

func (c *clientUpdateFakeBandcampClient) RetrieveItems(ctx context.Context) ([]*bcpb.Item, error) {
	if c.retrieveFn == nil {
		return nil, nil
	}
	return c.retrieveFn(ctx, c.onRefresh)
}

func (c *clientUpdateFakeBandcampClient) GetDownloadURLForRedownloadURL(ctx context.Context, redownloadURL string, format bandcamplib.Format) (string, error) {
	if c.downloadFn == nil {
		return "", nil
	}
	return c.downloadFn(ctx, redownloadURL, format, c.onRefresh)
}

func (c *clientUpdateFakeBandcampClient) IdentityCookie() string {
	return c.identityCookie
}

func newDefaultClientUpdateRC() *clientUpdateFakeRCClient {
	return &clientUpdateFakeRCClient{
		folderID:   1782105,
		hasFileFmt: true,
		queryResp:  &rcpb.QueryRecordsResponse{InstanceIds: []int32{1}},
	}
}

func TestClientUpdate_WAVLinkIssueCreation_AlbumAndSingleTrack(t *testing.T) {
	fixedNow := time.Unix(1700010000, 0)

	tests := []struct {
		name          string
		item          *bcpb.Item
		wantWAVURL    string
	}{
		{
			name: "multi-track album",
			item: &bcpb.Item{
				AlbumId:       101,
				BandName:      "Album Artist",
				AlbumTitle:    "Full Album Title",
				RedownloadUrl: "https://bandcamp.com/download/album?id=101",
			},
			wantWAVURL: "https://p4.bcbits.com/download/album/101.zip?format=wav&sig=abc",
		},
		{
			name: "single track",
			item: &bcpb.Item{
				AlbumId:       202,
				BandName:      "Track Artist",
				AlbumTitle:    "Single Track Title",
				RedownloadUrl: "https://bandcamp.com/download/track?id=202",
			},
			wantWAVURL: "https://p4.bcbits.com/download/track/202.wav?format=wav&sig=xyz",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newAPITestConfigStore(t, &pb.Config{
				Token:                "tok-1",
				IdentityCookie:       "valid-cookie-secret",
				LastProcess:          fixedNow.Add(-10 * time.Minute).Unix(),
				Items:                []*bcpb.Item{tc.item},
				DownloadLinkFailures: map[int64]int32{tc.item.GetAlbumId(): 2},
				AuthAlertIssueId:     99,
			})
			issues := &apiTestIssueManager{nextIssueID: 501}
			logger := &apiTestLogger{}
			var gotFormat bandcamplib.Format
			var gotRedownloadURL string

			s := &Server{
				store:  store,
				issues: issues,
				rc:     newDefaultClientUpdateRC(),
				bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
					return &clientUpdateFakeBandcampClient{
						identityCookie: cfg.GetIdentityCookie(),
						onRefresh:      onRefresh,
						downloadFn: func(ctx context.Context, redownloadURL string, format bandcamplib.Format, _ func(string)) (string, error) {
							gotRedownloadURL = redownloadURL
							gotFormat = format
							return tc.wantWAVURL, nil
						},
					}, nil
				},
				now:     func() time.Time { return fixedNow },
				logFunc: logger.log,
			}

			_, err := s.ClientUpdate(context.Background(), &rcpb.ClientUpdateRequest{InstanceId: 1})
			if err != nil {
				t.Fatalf("ClientUpdate failed: %v", err)
			}

			if gotFormat != bandcamplib.FormatWAV {
				t.Errorf("requested download format = %q, want %q", gotFormat, bandcamplib.FormatWAV)
			}
			if gotRedownloadURL != tc.item.GetRedownloadUrl() {
				t.Errorf("redownloadURL = %q, want %q", gotRedownloadURL, tc.item.GetRedownloadUrl())
			}

			calls := issues.immediateSnapshot()
			if len(calls) != 1 {
				t.Fatalf("expected 1 ImmediateIssue call, got %d (%+v)", len(calls), calls)
			}
			wantTitle := fmt.Sprintf("Bandcamp entry for %v is missing mapping [%v]", tc.item.GetBandName(), tc.item.GetAlbumId())
			if calls[0].title != wantTitle {
				t.Errorf("issue title = %q, want %q", calls[0].title, wantTitle)
			}
			for _, wantSubstr := range []string{
				tc.item.GetBandName(),
				tc.item.GetAlbumTitle(),
				fmt.Sprintf("%d", tc.item.GetAlbumId()),
				tc.wantWAVURL,
			} {
				if !strings.Contains(calls[0].body, wantSubstr) {
					t.Errorf("issue body missing %q:\n%s", wantSubstr, calls[0].body)
				}
			}
			lowerBody := strings.ToLower(calls[0].body)
			if !strings.Contains(lowerBody, "expire") && !strings.Contains(lowerBody, "time-limited") {
				t.Errorf("issue body missing expiry note:\n%s", calls[0].body)
			}

			deletes := issues.deleteSnapshot()
			if len(deletes) != 1 || deletes[0] != 99 {
				t.Errorf("DeleteIssue calls = %v, want [99]", deletes)
			}

			saved := store.currentConfig(t)
			if saved.GetIssueIds()[tc.item.GetAlbumId()] != 501 {
				t.Errorf("IssueIds[%d] = %d, want 501", tc.item.GetAlbumId(), saved.GetIssueIds()[tc.item.GetAlbumId()])
			}
			if _, exists := saved.GetDownloadLinkFailures()[tc.item.GetAlbumId()]; exists {
				t.Errorf("expected DownloadLinkFailures[%d] to be deleted, got %v", tc.item.GetAlbumId(), saved.GetDownloadLinkFailures())
			}
			if saved.GetAuthAlertIssueId() != 0 {
				t.Errorf("AuthAlertIssueId = %d, want 0", saved.GetAuthAlertIssueId())
			}
		})
	}
}

func TestClientUpdate_ExistingOpenIssueUntouched(t *testing.T) {
	fixedNow := time.Unix(1700010000, 0)
	store := newAPITestConfigStore(t, &pb.Config{
		Token:          "tok-1",
		IdentityCookie: "valid-cookie",
		LastProcess:    fixedNow.Add(-10 * time.Minute).Unix(),
		Items: []*bcpb.Item{
			{
				AlbumId:       303,
				BandName:      "Existing Artist",
				AlbumTitle:    "Existing Album",
				RedownloadUrl: "https://bandcamp.com/download/album?id=303",
			},
		},
		IssueIds: map[int64]int32{303: 55},
	})
	issues := &apiTestIssueManager{}
	var bcCalls int

	s := &Server{
		store:  store,
		issues: issues,
		rc:     newDefaultClientUpdateRC(),
		bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
			bcCalls++
			return &clientUpdateFakeBandcampClient{identityCookie: cfg.GetIdentityCookie()}, nil
		},
		now:     func() time.Time { return fixedNow },
		logFunc: (&apiTestLogger{}).log,
	}

	_, err := s.ClientUpdate(context.Background(), &rcpb.ClientUpdateRequest{InstanceId: 1})
	if err != nil {
		t.Fatalf("ClientUpdate failed: %v", err)
	}
	if bcCalls != 0 {
		t.Errorf("expected 0 bandcamplib calls, got %d", bcCalls)
	}
	if calls := issues.immediateSnapshot(); len(calls) != 0 {
		t.Errorf("expected 0 ImmediateIssue calls, got %d", len(calls))
	}
	if saved := store.currentConfig(t); saved.GetIssueIds()[303] != 55 {
		t.Errorf("IssueIds[303] = %d, want 55", saved.GetIssueIds()[303])
	}
}

func TestClientUpdate_PacingPreserved(t *testing.T) {
	fixedNow := time.Unix(1700010000, 0)
	item := &bcpb.Item{
		AlbumId:       404,
		BandName:      "Paced Artist",
		AlbumTitle:    "Paced Album",
		RedownloadUrl: "https://bandcamp.com/download/album?id=404",
	}

	t.Run("cooldown under 2m", func(t *testing.T) {
		store := newAPITestConfigStore(t, &pb.Config{
			IdentityCookie: "valid-cookie",
			LastProcess:    fixedNow.Add(-1 * time.Minute).Unix(),
			Items:          []*bcpb.Item{item},
		})
		issues := &apiTestIssueManager{}
		var bcCalls int

		s := &Server{
			store:  store,
			issues: issues,
			rc:     newDefaultClientUpdateRC(),
			bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
				bcCalls++
				return &clientUpdateFakeBandcampClient{}, nil
			},
			now:     func() time.Time { return fixedNow },
			logFunc: (&apiTestLogger{}).log,
		}

		_, err := s.ClientUpdate(context.Background(), &rcpb.ClientUpdateRequest{InstanceId: 1})
		if err != nil {
			t.Fatalf("ClientUpdate failed: %v", err)
		}
		if bcCalls != 0 {
			t.Errorf("expected 0 bandcamplib calls under cooldown, got %d", bcCalls)
		}
		if len(issues.immediateSnapshot()) != 0 {
			t.Errorf("expected 0 issues under cooldown")
		}
	})

	t.Run("daily limit 30", func(t *testing.T) {
		added := make(map[int32]int64, 30)
		for i := int32(1); i <= 30; i++ {
			added[i] = fixedNow.Add(-1 * time.Hour).Unix()
		}
		store := newAPITestConfigStore(t, &pb.Config{
			IdentityCookie: "valid-cookie",
			LastProcess:    fixedNow.Add(-10 * time.Minute).Unix(),
			AddedDate:      added,
			Items:          []*bcpb.Item{item},
		})
		issues := &apiTestIssueManager{}
		var bcCalls int

		s := &Server{
			store:  store,
			issues: issues,
			rc:     newDefaultClientUpdateRC(),
			bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
				bcCalls++
				return &clientUpdateFakeBandcampClient{}, nil
			},
			now:     func() time.Time { return fixedNow },
			logFunc: (&apiTestLogger{}).log,
		}

		_, err := s.ClientUpdate(context.Background(), &rcpb.ClientUpdateRequest{InstanceId: 1})
		if err != nil {
			t.Fatalf("ClientUpdate failed: %v", err)
		}
		if bcCalls != 0 {
			t.Errorf("expected 0 bandcamplib calls at daily limit, got %d", bcCalls)
		}
		if len(issues.immediateSnapshot()) != 0 {
			t.Errorf("expected 0 issues at daily limit")
		}
	})

	t.Run("failed link does not populate IssueIds", func(t *testing.T) {
		store := newAPITestConfigStore(t, &pb.Config{
			IdentityCookie: "valid-cookie",
			LastProcess:    fixedNow.Add(-10 * time.Minute).Unix(),
			Items:          []*bcpb.Item{item},
		})
		issues := &apiTestIssueManager{}

		s := &Server{
			store:  store,
			issues: issues,
			rc:     newDefaultClientUpdateRC(),
			bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
				return &clientUpdateFakeBandcampClient{
					identityCookie: cfg.GetIdentityCookie(),
					downloadFn: func(ctx context.Context, redownloadURL string, format bandcamplib.Format, _ func(string)) (string, error) {
						return "", bandcamplib.ErrDownloadTimeout
					},
				}, nil
			},
			now:     func() time.Time { return fixedNow },
			logFunc: (&apiTestLogger{}).log,
		}

		_, err := s.ClientUpdate(context.Background(), &rcpb.ClientUpdateRequest{InstanceId: 1})
		if err == nil {
			t.Fatal("expected error when link generation fails, got nil")
		}
		saved := store.currentConfig(t)
		if saved.GetIssueIds()[item.GetAlbumId()] != 0 {
			t.Errorf("IssueIds[%d] = %d, want 0", item.GetAlbumId(), saved.GetIssueIds()[item.GetAlbumId()])
		}
		if saved.GetDownloadLinkFailures()[item.GetAlbumId()] != 1 {
			t.Errorf("DownloadLinkFailures[%d] = %d, want 1", item.GetAlbumId(), saved.GetDownloadLinkFailures()[item.GetAlbumId()])
		}
		if len(issues.immediateSnapshot()) != 0 {
			t.Errorf("expected 0 issues created on failure 1")
		}
	})
}

func countLogsContaining(entries []string, substrs ...string) int {
	count := 0
	for _, entry := range entries {
		matched := true
		for _, s := range substrs {
			if !strings.Contains(entry, s) {
				matched = false
				break
			}
		}
		if matched {
			count++
		}
	}
	return count
}

func TestClientUpdate_AuthErrorsDeduplicationAndRecovery(t *testing.T) {
	fixedNow := time.Unix(1700020000, 0)

	t.Run("missing cookie", func(t *testing.T) {
		item := &bcpb.Item{
			AlbumId:    501,
			BandName:   "Auth Missing Artist",
			AlbumTitle: "Auth Missing Album",
		}
		store := newAPITestConfigStore(t, &pb.Config{
			Token:          "tok-1",
			IdentityCookie: "",
			LastProcess:    fixedNow.Add(-10 * time.Minute).Unix(),
			Items:          []*bcpb.Item{item},
		})
		issues := &apiTestIssueManager{nextIssueID: 700}
		logger := &apiTestLogger{}

		s := &Server{
			store:   store,
			issues:  issues,
			rc:      newDefaultClientUpdateRC(),
			now:     func() time.Time { return fixedNow },
			logFunc: logger.log,
		}

		for cycle := 1; cycle <= 2; cycle++ {
			_, err := s.ClientUpdate(context.Background(), &rcpb.ClientUpdateRequest{InstanceId: 1})
			if !errors.Is(err, bandcamplib.ErrAuthenticationRequired) {
				t.Fatalf("cycle %d: err = %v, want ErrAuthenticationRequired", cycle, err)
			}
		}

		if got := countLogsContaining(logger.entries(), "501", bandcamplib.ErrAuthenticationRequired.Error()); got < 2 {
			t.Errorf("expected at least 2 log entries containing album 501 and %q, got %d (%v)", bandcamplib.ErrAuthenticationRequired.Error(), got, logger.entries())
		}

		calls := issues.immediateSnapshot()
		if len(calls) != 1 {
			t.Fatalf("expected exactly 1 deduplicated auth alert issue, got %d (%+v)", len(calls), calls)
		}
		if calls[0].title != "Bandcamp session cookie missing or expired" {
			t.Errorf("auth alert title = %q", calls[0].title)
		}
		saved := store.currentConfig(t)
		if saved.GetAuthAlertIssueId() != 700 {
			t.Errorf("AuthAlertIssueId = %d, want 700", saved.GetAuthAlertIssueId())
		}
		if len(saved.GetDownloadLinkFailures()) != 0 {
			t.Errorf("expected empty DownloadLinkFailures, got %v", saved.GetDownloadLinkFailures())
		}
		if saved.GetIssueIds()[501] != 0 {
			t.Errorf("expected IssueIds[501] == 0, got %d", saved.GetIssueIds()[501])
		}
	})

	t.Run("expired cookie recovery via SetCookie", func(t *testing.T) {
		const expiredCookie = "expired-cookie-secret"
		const freshCookie = "fresh-cookie-secret"
		const expectedWAV = "https://p4.bcbits.com/download/recovered/502.wav"

		itemWithoutLink := &bcpb.Item{
			AlbumId:       502,
			BandName:      "Recovery Artist",
			AlbumTitle:    "Recovery Album",
			RedownloadUrl: "https://bandcamp.com/download/album?id=502",
		}
		store := newAPITestConfigStore(t, &pb.Config{
			Token:          "tok-1",
			IdentityCookie: expiredCookie,
			LastProcess:    fixedNow.Add(-10 * time.Minute).Unix(),
			Items:          []*bcpb.Item{itemWithoutLink},
		})
		issues := &apiTestIssueManager{nextIssueID: 710}
		logger := &apiTestLogger{}

		s := &Server{
			store:  store,
			issues: issues,
			rc:     newDefaultClientUpdateRC(),
			bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
				return &clientUpdateFakeBandcampClient{
					identityCookie: cfg.GetIdentityCookie(),
					onRefresh:      onRefresh,
					retrieveFn: func(ctx context.Context, _ func(string)) ([]*bcpb.Item, error) {
						if cfg.GetIdentityCookie() != freshCookie {
							return nil, bandcamplib.ErrAuthenticationFailed
						}
						return []*bcpb.Item{{
							AlbumId:       502,
							BandName:      "Recovery Artist",
							AlbumTitle:    "Recovery Album",
							RedownloadUrl: "https://bandcamp.com/download/album?id=502&auth=fresh",
						}}, nil
					},
					downloadFn: func(ctx context.Context, redownloadURL string, format bandcamplib.Format, _ func(string)) (string, error) {
						if cfg.GetIdentityCookie() != freshCookie {
							return "", bandcamplib.ErrAuthenticationFailed
						}
						return expectedWAV, nil
					},
				}, nil
			},
			now:     func() time.Time { return fixedNow },
			logFunc: logger.log,
		}

		for cycle := 1; cycle <= 2; cycle++ {
			_, err := s.ClientUpdate(context.Background(), &rcpb.ClientUpdateRequest{InstanceId: 1})
			if !errors.Is(err, bandcamplib.ErrAuthenticationFailed) {
				t.Fatalf("cycle %d: err = %v, want ErrAuthenticationFailed", cycle, err)
			}
		}

		if got := countLogsContaining(logger.entries(), "502", bandcamplib.ErrAuthenticationFailed.Error()); got < 2 {
			t.Errorf("expected at least 2 log entries containing album 502 and %q, got %d (%v)", bandcamplib.ErrAuthenticationFailed.Error(), got, logger.entries())
		}
		for _, entry := range logger.entries() {
			if strings.Contains(entry, expiredCookie) || strings.Contains(entry, freshCookie) {
				t.Errorf("log entry leaked cookie secret: %q", entry)
			}
		}

		if calls := issues.immediateSnapshot(); len(calls) != 1 {
			t.Fatalf("expected 1 deduplicated auth alert before recovery, got %d", len(calls))
		}
		if saved := store.currentConfig(t); len(saved.GetDownloadLinkFailures()) != 0 {
			t.Fatalf("expected no DownloadLinkFailures on auth errors, got %v", saved.GetDownloadLinkFailures())
		}

		if _, err := s.SetCookie(context.Background(), &pb.SetCookieRequest{Cookie: freshCookie}); err != nil {
			t.Fatalf("SetCookie failed: %v", err)
		}

		deletes := issues.deleteSnapshot()
		if len(deletes) != 1 || deletes[0] != 710 {
			t.Errorf("DeleteIssue calls = %v, want [710]", deletes)
		}
		afterCookie := store.currentConfig(t)
		if afterCookie.GetAuthAlertIssueId() != 0 {
			t.Errorf("AuthAlertIssueId after SetCookie = %d, want 0", afterCookie.GetAuthAlertIssueId())
		}
		if len(afterCookie.GetItems()) != 1 || afterCookie.GetItems()[0].GetRedownloadUrl() != "https://bandcamp.com/download/album?id=502&auth=fresh" {
			t.Errorf("unexpected Items after SetCookie: %+v", afterCookie.GetItems())
		}

		if _, err := s.ClientUpdate(context.Background(), &rcpb.ClientUpdateRequest{InstanceId: 1}); err != nil {
			t.Fatalf("ClientUpdate after recovery failed: %v", err)
		}

		calls := issues.immediateSnapshot()
		if len(calls) != 2 {
			t.Fatalf("expected 2 total ImmediateIssue calls (1 auth alert + 1 missing mapping), got %d", len(calls))
		}
		if !strings.Contains(calls[1].body, expectedWAV) {
			t.Errorf("recovered missing-mapping issue body missing WAV URL %q:\n%s", expectedWAV, calls[1].body)
		}
		if !strings.Contains(strings.ToLower(calls[1].body), "expire") {
			t.Errorf("recovered missing-mapping issue body missing expiry note:\n%s", calls[1].body)
		}
		finalCfg := store.currentConfig(t)
		if finalCfg.GetIssueIds()[502] != 711 {
			t.Errorf("IssueIds[502] = %d, want 711", finalCfg.GetIssueIds()[502])
		}
	})
}

func TestClientUpdate_ThreeRetryFallbackAndRestartPersistence(t *testing.T) {
	fixedNow := time.Unix(1700030000, 0)

	tests := []struct {
		name   string
		dlErr  error
	}{
		{name: "ErrDownloadUnavailable", dlErr: bandcamplib.ErrDownloadUnavailable},
		{name: "ErrFormatUnavailable", dlErr: bandcamplib.ErrFormatUnavailable},
		{name: "ErrDownloadTimeout", dlErr: bandcamplib.ErrDownloadTimeout},
		{name: "ErrNotFound", dlErr: bandcamplib.ErrNotFound},
		{name: "transient HTTP 503", dlErr: errors.New("HTTP 503 service unavailable")},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			const albumID int64 = 601
			store := newAPITestConfigStore(t, &pb.Config{
				Token:          "tok-1",
				IdentityCookie: "valid-cookie",
				LastProcess:    fixedNow.Add(-10 * time.Minute).Unix(),
				Items: []*bcpb.Item{
					{
						AlbumId:       albumID,
						BandName:      "Retry Artist",
						AlbumTitle:    "Retry Album",
						RedownloadUrl: "https://bandcamp.com/download/album?id=601",
					},
				},
			})
			issues := &apiTestIssueManager{nextIssueID: 800}
			logger := &apiTestLogger{}

			newServer := func() *Server {
				return &Server{
					store:  store,
					issues: issues,
					rc:     newDefaultClientUpdateRC(),
					bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
						return &clientUpdateFakeBandcampClient{
							identityCookie: cfg.GetIdentityCookie(),
							downloadFn: func(ctx context.Context, redownloadURL string, format bandcamplib.Format, _ func(string)) (string, error) {
								return "", tc.dlErr
							},
						}, nil
					},
					now:     func() time.Time { return fixedNow },
					logFunc: logger.log,
				}
			}

			// Cycle 1 (after simulated restart 1)
			if _, err := newServer().ClientUpdate(context.Background(), &rcpb.ClientUpdateRequest{InstanceId: 1}); !errors.Is(err, tc.dlErr) {
				t.Fatalf("cycle 1 err = %v, want %v", err, tc.dlErr)
			}
			if got := countLogsContaining(logger.entries(), "601", "1/3", tc.dlErr.Error()); got < 1 {
				t.Errorf("cycle 1 expected log containing 601, 1/3, and %q; logs=%v", tc.dlErr.Error(), logger.entries())
			}
			cfg1 := store.currentConfig(t)
			if cfg1.GetDownloadLinkFailures()[albumID] != 1 {
				t.Errorf("cycle 1 failures = %d, want 1", cfg1.GetDownloadLinkFailures()[albumID])
			}
			if cfg1.GetIssueIds()[albumID] != 0 {
				t.Errorf("cycle 1 IssueIds[%d] = %d, want 0", albumID, cfg1.GetIssueIds()[albumID])
			}
			if len(issues.immediateSnapshot()) != 0 {
				t.Errorf("cycle 1 should not create issues")
			}

			// Cycle 2 (after simulated restart 2)
			if _, err := newServer().ClientUpdate(context.Background(), &rcpb.ClientUpdateRequest{InstanceId: 1}); !errors.Is(err, tc.dlErr) {
				t.Fatalf("cycle 2 err = %v, want %v", err, tc.dlErr)
			}
			if got := countLogsContaining(logger.entries(), "601", "2/3", tc.dlErr.Error()); got < 1 {
				t.Errorf("cycle 2 expected log containing 601, 2/3, and %q; logs=%v", tc.dlErr.Error(), logger.entries())
			}
			cfg2 := store.currentConfig(t)
			if cfg2.GetDownloadLinkFailures()[albumID] != 2 {
				t.Errorf("cycle 2 failures = %d, want 2", cfg2.GetDownloadLinkFailures()[albumID])
			}
			if cfg2.GetIssueIds()[albumID] != 0 {
				t.Errorf("cycle 2 IssueIds[%d] = %d, want 0", albumID, cfg2.GetIssueIds()[albumID])
			}
			if len(issues.immediateSnapshot()) != 0 {
				t.Errorf("cycle 2 should not create issues")
			}

			// Cycle 3 (after simulated restart 3) -> fallback linkless issue
			if _, err := newServer().ClientUpdate(context.Background(), &rcpb.ClientUpdateRequest{InstanceId: 1}); err != nil {
				t.Fatalf("cycle 3 failed: %v", err)
			}
			if got := countLogsContaining(logger.entries(), "601", "3/3", tc.dlErr.Error()); got < 1 {
				t.Errorf("cycle 3 expected log containing 601, 3/3, and %q; logs=%v", tc.dlErr.Error(), logger.entries())
			}
			calls := issues.immediateSnapshot()
			if len(calls) != 1 {
				t.Fatalf("expected 1 fallback issue on cycle 3, got %d", len(calls))
			}
			if strings.Contains(calls[0].body, "https://") || strings.Contains(calls[0].body, "http://") {
				t.Errorf("fallback issue body should not contain a download link:\n%s", calls[0].body)
			}
			lowerBody := strings.ToLower(calls[0].body)
			if !strings.Contains(lowerBody, "failed") || !strings.Contains(lowerBody, "3") {
				t.Errorf("fallback issue body should mention failure after 3 attempts:\n%s", calls[0].body)
			}

			cfg3 := store.currentConfig(t)
			if cfg3.GetIssueIds()[albumID] != 800 {
				t.Errorf("cycle 3 IssueIds[%d] = %d, want 800", albumID, cfg3.GetIssueIds()[albumID])
			}
			if _, exists := cfg3.GetDownloadLinkFailures()[albumID]; exists {
				t.Errorf("expected DownloadLinkFailures[%d] deleted after fallback, got %v", albumID, cfg3.GetDownloadLinkFailures())
			}
		})
	}
}

func TestClientUpdate_StaleUnauthenticatedMetadataRefresh(t *testing.T) {
	fixedNow := time.Unix(1700040000, 0)
	staleItem := &bcpb.Item{
		AlbumId:       701,
		BandName:      "Stale Artist",
		AlbumTitle:    "Stale Album",
		RedownloadUrl: "",
	}

	t.Run("refresh succeeds", func(t *testing.T) {
		const refreshedRedownload = "https://bandcamp.com/download/album?id=701&refreshed=1"
		const expectedWAV = "https://p4.bcbits.com/download/701.wav"

		store := newAPITestConfigStore(t, &pb.Config{
			Token:          "tok-1",
			IdentityCookie: "valid-cookie",
			LastProcess:    fixedNow.Add(-10 * time.Minute).Unix(),
			Items:          []*bcpb.Item{staleItem},
		})
		issues := &apiTestIssueManager{nextIssueID: 901}

		s := &Server{
			store:  store,
			issues: issues,
			rc:     newDefaultClientUpdateRC(),
			bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
				return &clientUpdateFakeBandcampClient{
					identityCookie: cfg.GetIdentityCookie(),
					retrieveFn: func(ctx context.Context, _ func(string)) ([]*bcpb.Item, error) {
						return []*bcpb.Item{{
							AlbumId:       701,
							BandName:      "Stale Artist",
							AlbumTitle:    "Stale Album",
							RedownloadUrl: refreshedRedownload,
						}}, nil
					},
					downloadFn: func(ctx context.Context, redownloadURL string, format bandcamplib.Format, _ func(string)) (string, error) {
						if redownloadURL != refreshedRedownload {
							return "", fmt.Errorf("unexpected redownloadURL: %s", redownloadURL)
						}
						return expectedWAV, nil
					},
				}, nil
			},
			now:     func() time.Time { return fixedNow },
			logFunc: (&apiTestLogger{}).log,
		}

		if _, err := s.ClientUpdate(context.Background(), &rcpb.ClientUpdateRequest{InstanceId: 1}); err != nil {
			t.Fatalf("ClientUpdate failed: %v", err)
		}

		calls := issues.immediateSnapshot()
		if len(calls) != 1 {
			t.Fatalf("expected 1 issue call, got %d", len(calls))
		}
		if !strings.Contains(calls[0].body, expectedWAV) {
			t.Errorf("issue body missing WAV URL %q:\n%s", expectedWAV, calls[0].body)
		}
		saved := store.currentConfig(t)
		if len(saved.GetItems()) != 1 || saved.GetItems()[0].GetRedownloadUrl() != refreshedRedownload {
			t.Errorf("expected refreshed items persisted, got %+v", saved.GetItems())
		}
		if saved.GetIssueIds()[701] != 901 {
			t.Errorf("IssueIds[701] = %d, want 901", saved.GetIssueIds()[701])
		}
	})

	t.Run("refresh auth failed", func(t *testing.T) {
		store := newAPITestConfigStore(t, &pb.Config{
			Token:          "tok-1",
			IdentityCookie: "expired-cookie",
			LastProcess:    fixedNow.Add(-10 * time.Minute).Unix(),
			Items:          []*bcpb.Item{staleItem},
		})
		issues := &apiTestIssueManager{nextIssueID: 902}

		s := &Server{
			store:  store,
			issues: issues,
			rc:     newDefaultClientUpdateRC(),
			bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
				return &clientUpdateFakeBandcampClient{
					identityCookie: cfg.GetIdentityCookie(),
					retrieveFn: func(ctx context.Context, _ func(string)) ([]*bcpb.Item, error) {
						return nil, bandcamplib.ErrAuthenticationFailed
					},
				}, nil
			},
			now:     func() time.Time { return fixedNow },
			logFunc: (&apiTestLogger{}).log,
		}

		_, err := s.ClientUpdate(context.Background(), &rcpb.ClientUpdateRequest{InstanceId: 1})
		if !errors.Is(err, bandcamplib.ErrAuthenticationFailed) {
			t.Fatalf("err = %v, want ErrAuthenticationFailed", err)
		}
		saved := store.currentConfig(t)
		if saved.GetAuthAlertIssueId() != 902 {
			t.Errorf("AuthAlertIssueId = %d, want 902", saved.GetAuthAlertIssueId())
		}
		if len(saved.GetDownloadLinkFailures()) != 0 {
			t.Errorf("expected empty DownloadLinkFailures, got %v", saved.GetDownloadLinkFailures())
		}
	})

	t.Run("refresh still missing redownload url", func(t *testing.T) {
		store := newAPITestConfigStore(t, &pb.Config{
			Token:          "tok-1",
			IdentityCookie: "valid-cookie",
			LastProcess:    fixedNow.Add(-10 * time.Minute).Unix(),
			Items:          []*bcpb.Item{staleItem},
		})
		issues := &apiTestIssueManager{}

		s := &Server{
			store:  store,
			issues: issues,
			rc:     newDefaultClientUpdateRC(),
			bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
				return &clientUpdateFakeBandcampClient{
					identityCookie: cfg.GetIdentityCookie(),
					retrieveFn: func(ctx context.Context, _ func(string)) ([]*bcpb.Item, error) {
						return []*bcpb.Item{staleItem}, nil
					},
				}, nil
			},
			now:     func() time.Time { return fixedNow },
			logFunc: (&apiTestLogger{}).log,
		}

		_, err := s.ClientUpdate(context.Background(), &rcpb.ClientUpdateRequest{InstanceId: 1})
		if !errors.Is(err, bandcamplib.ErrDownloadUnavailable) {
			t.Fatalf("err = %v, want ErrDownloadUnavailable", err)
		}
		saved := store.currentConfig(t)
		if saved.GetDownloadLinkFailures()[701] != 1 {
			t.Errorf("DownloadLinkFailures[701] = %d, want 1", saved.GetDownloadLinkFailures()[701])
		}
	})
}

func TestClientUpdate_ConcurrentSerialization(t *testing.T) {
	fixedNow := time.Unix(1700050000, 0)
	var currentUnix atomic.Int64
	currentUnix.Store(fixedNow.Unix())
	const rotatedCookie = "rotated-cookie-during-client-update"
	const setCookieVal = "explicit-set-cookie-val"

	store := newAPITestConfigStore(t, &pb.Config{
		Token:          "tok-1",
		IdentityCookie: "initial-cookie",
		LastProcess:    fixedNow.Add(-10 * time.Minute).Unix(),
		Items: []*bcpb.Item{
			{
				AlbumId:       801,
				BandName:      "Concurrent Artist 1",
				AlbumTitle:    "Concurrent Album 1",
				RedownloadUrl: "https://bandcamp.com/download/album?id=801",
			},
			{
				AlbumId:       802,
				BandName:      "Concurrent Artist 2",
				AlbumTitle:    "Concurrent Album 2",
				RedownloadUrl: "https://bandcamp.com/download/album?id=802",
			},
		},
		DownloadLinkFailures: map[int64]int32{802: 2},
	})
	issues := &apiTestIssueManager{nextIssueID: 1000}
	var refreshedOnce int32

	s := &Server{
		store:  store,
		issues: issues,
		rc:     newDefaultClientUpdateRC(),
		bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
			return &clientUpdateFakeBandcampClient{
				identityCookie: cfg.GetIdentityCookie(),
				onRefresh:      onRefresh,
				retrieveFn: func(ctx context.Context, _ func(string)) ([]*bcpb.Item, error) {
					return []*bcpb.Item{
						{
							AlbumId:       801,
							BandName:      "Concurrent Artist 1",
							AlbumTitle:    "Concurrent Album 1",
							RedownloadUrl: "https://bandcamp.com/download/album?id=801",
						},
						{
							AlbumId:       802,
							BandName:      "Concurrent Artist 2",
							AlbumTitle:    "Concurrent Album 2",
							RedownloadUrl: "https://bandcamp.com/download/album?id=802",
						},
					}, nil
				},
				downloadFn: func(ctx context.Context, redownloadURL string, format bandcamplib.Format, cb func(string)) (string, error) {
					if atomic.CompareAndSwapInt32(&refreshedOnce, 0, 1) && cb != nil {
						cb(rotatedCookie)
					}
					return "https://p4.bcbits.com/download/801.wav", nil
				},
			}, nil
		},
		now:     func() time.Time { return time.Unix(currentUnix.Load(), 0) },
		logFunc: (&apiTestLogger{}).log,
	}

	var wg sync.WaitGroup
	errs := make(chan error, 12)

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.ClientUpdate(context.Background(), &rcpb.ClientUpdateRequest{InstanceId: 1}); err != nil {
				errs <- fmt.Errorf("ClientUpdate: %w", err)
			}
		}()
	}

	wg.Add(2)
	go func() {
		defer wg.Done()
		if _, err := s.SetCookie(context.Background(), &pb.SetCookieRequest{Cookie: setCookieVal}); err != nil {
			errs <- fmt.Errorf("SetCookie: %w", err)
		}
	}()
	go func() {
		defer wg.Done()
		if _, err := s.AddMapping(context.Background(), &pb.AddMappingRequest{BandcampId: 802, DiscogsId: 4242}); err != nil {
			errs <- fmt.Errorf("AddMapping: %w", err)
		}
	}()

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Error(err)
	}

	// Advance clock past the 2-minute AddMapping cooldown and run one more
	// ClientUpdate so album 801 is guaranteed to be processed even if AddMapping
	// ran before the concurrent ClientUpdate goroutines.
	currentUnix.Store(fixedNow.Add(5 * time.Minute).Unix())
	if _, err := s.ClientUpdate(context.Background(), &rcpb.ClientUpdateRequest{InstanceId: 1}); err != nil {
		t.Fatalf("post-cooldown ClientUpdate failed: %v", err)
	}

	saved := store.currentConfig(t)
	if saved.GetMapping()[802] != 4242 {
		t.Errorf("Mapping[802] = %d, want 4242", saved.GetMapping()[802])
	}
	if _, exists := saved.GetDownloadLinkFailures()[802]; exists {
		t.Errorf("expected DownloadLinkFailures[802] cleared by AddMapping, got %v", saved.GetDownloadLinkFailures())
	}
	if saved.GetIdentityCookie() != rotatedCookie && saved.GetIdentityCookie() != setCookieVal {
		t.Errorf("unexpected IdentityCookie = %q, want %q or %q", saved.GetIdentityCookie(), rotatedCookie, setCookieVal)
	}

	// Ensure no duplicate missing-mapping issues were created for album 801.
	album801Issues := 0
	for _, call := range issues.immediateSnapshot() {
		if strings.Contains(call.title, "[801]") {
			album801Issues++
		}
	}
	if album801Issues != 1 {
		t.Errorf("expected exactly 1 issue for album 801, got %d", album801Issues)
	}
}

func TestClientUpdate_SecretRedaction(t *testing.T) {
	fixedNow := time.Unix(1700060000, 0)
	const initialCookie = "ultra-secret-initial-cookie-AAA"
	const rotatedCookie = "ultra-secret-rotated-cookie-BBB"

	assertNoSecretLeak := func(t *testing.T, logger *apiTestLogger, issues *apiTestIssueManager, err error) {
		t.Helper()
		for _, secret := range []string{initialCookie, rotatedCookie} {
			if err != nil && strings.Contains(err.Error(), secret) {
				t.Errorf("returned error leaked secret %q: %v", secret, err)
			}
			for _, entry := range logger.entries() {
				if strings.Contains(entry, secret) {
					t.Errorf("log entry leaked secret %q: %q", secret, entry)
				}
			}
			for _, call := range issues.immediateSnapshot() {
				if strings.Contains(call.title, secret) || strings.Contains(call.body, secret) {
					t.Errorf("GitHub issue leaked secret %q: title=%q body=%q", secret, call.title, call.body)
				}
			}
		}
	}

	t.Run("auth failure redacts secrets", func(t *testing.T) {
		store := newAPITestConfigStore(t, &pb.Config{
			IdentityCookie: initialCookie,
			LastProcess:    fixedNow.Add(-10 * time.Minute).Unix(),
			Items: []*bcpb.Item{
				{AlbumId: 901, BandName: "Secret Artist", AlbumTitle: "Secret Album", RedownloadUrl: "https://bandcamp.com/dl/901"},
			},
		})
		issues := &apiTestIssueManager{}
		logger := &apiTestLogger{}
		s := &Server{
			store:  store,
			issues: issues,
			rc:     newDefaultClientUpdateRC(),
			bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
				return &clientUpdateFakeBandcampClient{
					identityCookie: cfg.GetIdentityCookie(),
					onRefresh:      onRefresh,
					downloadFn: func(ctx context.Context, redownloadURL string, format bandcamplib.Format, cb func(string)) (string, error) {
						cb(rotatedCookie)
						return "", fmt.Errorf("%w: bad cookies %s and %s", bandcamplib.ErrAuthenticationFailed, initialCookie, rotatedCookie)
					},
				}, nil
			},
			now:     func() time.Time { return fixedNow },
			logFunc: logger.log,
		}

		_, err := s.ClientUpdate(context.Background(), &rcpb.ClientUpdateRequest{InstanceId: 1})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		assertNoSecretLeak(t, logger, issues, err)
		if got := countLogsContaining(logger.entries(), "901", bandcamplib.ErrAuthenticationFailed.Error(), "[REDACTED]"); got < 1 {
			t.Errorf("expected log entry containing 901, %q, and [REDACTED]; logs=%v", bandcamplib.ErrAuthenticationFailed.Error(), logger.entries())
		}
	})

	t.Run("non-auth failure and 3-retry fallback redact secrets", func(t *testing.T) {
		store := newAPITestConfigStore(t, &pb.Config{
			IdentityCookie:       initialCookie,
			LastProcess:          fixedNow.Add(-10 * time.Minute).Unix(),
			DownloadLinkFailures: map[int64]int32{902: 2},
			Items: []*bcpb.Item{
				{AlbumId: 902, BandName: "Secret Artist 2", AlbumTitle: "Secret Album 2", RedownloadUrl: "https://bandcamp.com/dl/902"},
			},
		})
		issues := &apiTestIssueManager{}
		logger := &apiTestLogger{}
		s := &Server{
			store:  store,
			issues: issues,
			rc:     newDefaultClientUpdateRC(),
			bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
				return &clientUpdateFakeBandcampClient{
					identityCookie: cfg.GetIdentityCookie(),
					onRefresh:      onRefresh,
					downloadFn: func(ctx context.Context, redownloadURL string, format bandcamplib.Format, cb func(string)) (string, error) {
						cb(rotatedCookie)
						return "", fmt.Errorf("upstream failure with %s and %s", initialCookie, rotatedCookie)
					},
				}, nil
			},
			now:     func() time.Time { return fixedNow },
			logFunc: logger.log,
		}

		_, err := s.ClientUpdate(context.Background(), &rcpb.ClientUpdateRequest{InstanceId: 1})
		if err != nil {
			t.Fatalf("unexpected error on 3rd failure fallback: %v", err)
		}
		assertNoSecretLeak(t, logger, issues, err)
		if got := countLogsContaining(logger.entries(), "902", "3/3", "upstream failure with", "[REDACTED]"); got < 1 {
			t.Errorf("expected log entry containing 902, 3/3, upstream failure with, and [REDACTED]; logs=%v", logger.entries())
		}
	})

	t.Run("non-auth retry attempts 1-2 redact secrets", func(t *testing.T) {
		store := newAPITestConfigStore(t, &pb.Config{
			IdentityCookie: initialCookie,
			LastProcess:    fixedNow.Add(-10 * time.Minute).Unix(),
			Items: []*bcpb.Item{
				{AlbumId: 905, BandName: "Secret Artist 5", AlbumTitle: "Secret Album 5", RedownloadUrl: "https://bandcamp.com/dl/905"},
			},
		})
		issues := &apiTestIssueManager{}
		logger := &apiTestLogger{}
		s := &Server{
			store:  store,
			issues: issues,
			rc:     newDefaultClientUpdateRC(),
			bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
				return &clientUpdateFakeBandcampClient{
					identityCookie: cfg.GetIdentityCookie(),
					onRefresh:      onRefresh,
					downloadFn: func(ctx context.Context, redownloadURL string, format bandcamplib.Format, cb func(string)) (string, error) {
						cb(rotatedCookie)
						return "", fmt.Errorf("retry upstream failure with %s and %s", cfg.GetIdentityCookie(), rotatedCookie)
					},
				}, nil
			},
			now:     func() time.Time { return fixedNow },
			logFunc: logger.log,
		}

		_, err := s.ClientUpdate(context.Background(), &rcpb.ClientUpdateRequest{InstanceId: 1})
		if err == nil {
			t.Fatal("attempt 1: expected error, got nil")
		}
		assertNoSecretLeak(t, logger, issues, err)
		if got := countLogsContaining(logger.entries(), "905", "1/3", "retry upstream failure with", "[REDACTED]"); got < 1 {
			t.Errorf("attempt 1: expected log entry containing 905, 1/3, retry upstream failure with, and [REDACTED]; logs=%v", logger.entries())
		}

		_, err = s.ClientUpdate(context.Background(), &rcpb.ClientUpdateRequest{InstanceId: 1})
		if err == nil {
			t.Fatal("attempt 2: expected error, got nil")
		}
		assertNoSecretLeak(t, logger, issues, err)
		if got := countLogsContaining(logger.entries(), "905", "2/3", "retry upstream failure with", "[REDACTED]"); got < 1 {
			t.Errorf("attempt 2: expected log entry containing 905, 2/3, retry upstream failure with, and [REDACTED]; logs=%v", logger.entries())
		}
	})

	t.Run("ImmediateIssue failure redacts secrets", func(t *testing.T) {
		store := newAPITestConfigStore(t, &pb.Config{
			IdentityCookie: initialCookie,
			LastProcess:    fixedNow.Add(-10 * time.Minute).Unix(),
			Items: []*bcpb.Item{
				{AlbumId: 903, BandName: "Secret Artist 3", AlbumTitle: "Secret Album 3", RedownloadUrl: "https://bandcamp.com/dl/903"},
			},
		})
		issues := &apiTestIssueManager{
			immediateErr: fmt.Errorf("githubcard failed with %s and %s", initialCookie, rotatedCookie),
		}
		logger := &apiTestLogger{}
		s := &Server{
			store:  store,
			issues: issues,
			rc:     newDefaultClientUpdateRC(),
			bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
				return &clientUpdateFakeBandcampClient{
					identityCookie: cfg.GetIdentityCookie(),
					onRefresh:      onRefresh,
					downloadFn: func(ctx context.Context, redownloadURL string, format bandcamplib.Format, cb func(string)) (string, error) {
						cb(rotatedCookie)
						return "https://p4.bcbits.com/download/903.wav", nil
					},
				}, nil
			},
			now:     func() time.Time { return fixedNow },
			logFunc: logger.log,
		}

		_, err := s.ClientUpdate(context.Background(), &rcpb.ClientUpdateRequest{InstanceId: 1})
		if err == nil {
			t.Fatal("expected ImmediateIssue error, got nil")
		}
		assertNoSecretLeak(t, logger, issues, err)
	})

	t.Run("saveConfig failure redacts secrets", func(t *testing.T) {
		store := newAPITestConfigStore(t, &pb.Config{
			IdentityCookie: initialCookie,
			LastProcess:    fixedNow.Add(-10 * time.Minute).Unix(),
			Items: []*bcpb.Item{
				{AlbumId: 904, BandName: "Secret Artist 4", AlbumTitle: "Secret Album 4", RedownloadUrl: "https://bandcamp.com/dl/904"},
			},
		})
		store.saveErr = fmt.Errorf("dstore failed with %s and %s", initialCookie, rotatedCookie)
		issues := &apiTestIssueManager{}
		logger := &apiTestLogger{}
		s := &Server{
			store:  store,
			issues: issues,
			rc:     newDefaultClientUpdateRC(),
			bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
				return &clientUpdateFakeBandcampClient{
					identityCookie: cfg.GetIdentityCookie(),
					onRefresh:      onRefresh,
					downloadFn: func(ctx context.Context, redownloadURL string, format bandcamplib.Format, cb func(string)) (string, error) {
						cb(rotatedCookie)
						return "https://p4.bcbits.com/download/904.wav", nil
					},
				}, nil
			},
			now:     func() time.Time { return fixedNow },
			logFunc: logger.log,
		}

		_, err := s.ClientUpdate(context.Background(), &rcpb.ClientUpdateRequest{InstanceId: 1})
		if err == nil {
			t.Fatal("expected saveConfig error, got nil")
		}
		assertNoSecretLeak(t, logger, issues, err)
	})
}

func TestClientUpdate_EmptyCollectionAndAllMapped(t *testing.T) {
	fixedNow := time.Unix(1700070000, 0)

	tests := []struct {
		name string
		cfg  *pb.Config
	}{
		{
			name: "empty collection",
			cfg: &pb.Config{
				IdentityCookie: "valid-cookie",
				LastProcess:    fixedNow.Add(-10 * time.Minute).Unix(),
				Items:          nil,
			},
		},
		{
			name: "all mapped",
			cfg: &pb.Config{
				IdentityCookie: "valid-cookie",
				LastProcess:    fixedNow.Add(-10 * time.Minute).Unix(),
				Items: []*bcpb.Item{
					{AlbumId: 1, BandName: "A", AlbumTitle: "B", RedownloadUrl: "https://bandcamp.com/dl/1"},
				},
				Mapping:   map[int64]int32{1: 1001},
				AddedDate: map[int32]int64{1001: fixedNow.Add(-48 * time.Hour).Unix()},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newAPITestConfigStore(t, tc.cfg)
			issues := &apiTestIssueManager{}
			var bcCalls int

			s := &Server{
				store:  store,
				issues: issues,
				rc:     newDefaultClientUpdateRC(),
				bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
					bcCalls++
					return &clientUpdateFakeBandcampClient{}, nil
				},
				now:     func() time.Time { return fixedNow },
				logFunc: (&apiTestLogger{}).log,
			}

			if _, err := s.ClientUpdate(context.Background(), &rcpb.ClientUpdateRequest{InstanceId: 1}); err != nil {
				t.Fatalf("ClientUpdate failed: %v", err)
			}
			if bcCalls != 0 {
				t.Errorf("expected 0 bandcamplib calls, got %d", bcCalls)
			}
			if len(issues.immediateSnapshot()) != 0 {
				t.Errorf("expected 0 ImmediateIssue calls")
			}
		})
	}
}
