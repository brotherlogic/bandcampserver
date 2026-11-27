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
	searchFn     func(ctx context.Context, req *rcpb.SearchReleasesRequest) (*rcpb.SearchReleasesResponse, error)
	searchCalls  []*rcpb.SearchReleasesRequest
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

func (f *clientUpdateFakeRCClient) SearchReleases(ctx context.Context, req *rcpb.SearchReleasesRequest) (*rcpb.SearchReleasesResponse, error) {
	f.mu.Lock()
	f.searchCalls = append(f.searchCalls, req)
	searchFn := f.searchFn
	f.mu.Unlock()
	if searchFn != nil {
		return searchFn(ctx, req)
	}
	return &rcpb.SearchReleasesResponse{}, nil
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
		queryResp:  &rcpb.QueryRecordsResponse{InstanceIds: []int64{1}},
	}
}

func TestClientUpdate_MissingMappingIssueCreation_NoWAVLinkResolution(t *testing.T) {
	fixedNow := time.Unix(1700010000, 0)

	tests := []struct {
		name string
		item *bcpb.Item
	}{
		{
			name: "multi-track album",
			item: &bcpb.Item{
				AlbumId:       101,
				BandName:      "Album Artist",
				AlbumTitle:    "Full Album Title",
				RedownloadUrl: "https://bandcamp.com/download/album?id=101",
			},
		},
		{
			name: "single track",
			item: &bcpb.Item{
				AlbumId:       202,
				BandName:      "Track Artist",
				AlbumTitle:    "Single Track Title",
				RedownloadUrl: "https://bandcamp.com/download/track?id=202",
			},
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
			var bcCalls int

			s := &Server{
				store:  store,
				issues: issues,
				rc:     newDefaultClientUpdateRC(),
				bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
					bcCalls++
					return nil, errors.New("bandcamp client must not be invoked when opening missing-mapping issues")
				},
				now:     func() time.Time { return fixedNow },
				logFunc: logger.log,
			}

			_, err := s.ClientUpdate(context.Background(), &rcpb.ClientUpdateRequest{InstanceId: 1})
			if err != nil {
				t.Fatalf("ClientUpdate failed: %v", err)
			}

			if bcCalls != 0 {
				t.Errorf("expected 0 Bandcamp client calls, got %d", bcCalls)
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
				fmt.Sprintf("%v - %v (%v) is missing a mapping", tc.item.GetBandName(), tc.item.GetAlbumTitle(), tc.item.GetAlbumId()),
				"### Discogs Candidate Releases",
			} {
				if !strings.Contains(calls[0].body, wantSubstr) {
					t.Errorf("issue body missing %q:\n%s", wantSubstr, calls[0].body)
				}
			}
			for _, forbidden := range []string{
				"Direct WAV download link",
				"p4.bcbits.com",
				"time-limited",
				"expire",
				"WAV download link generation failed",
			} {
				if strings.Contains(strings.ToLower(calls[0].body), strings.ToLower(forbidden)) {
					t.Errorf("issue body must not contain %q:\n%s", forbidden, calls[0].body)
				}
			}

			if deletes := issues.deleteSnapshot(); len(deletes) != 0 {
				t.Errorf("expected 0 DeleteIssue calls, got %v", deletes)
			}

			saved := store.currentConfig(t)
			if saved.GetIssueIds()[tc.item.GetAlbumId()] != 501 {
				t.Errorf("IssueIds[%d] = %d, want 501", tc.item.GetAlbumId(), saved.GetIssueIds()[tc.item.GetAlbumId()])
			}
			if saved.GetAuthAlertIssueId() != 99 {
				t.Errorf("AuthAlertIssueId = %d, want 99", saved.GetAuthAlertIssueId())
			}
			if saved.GetDownloadLinkFailures()[tc.item.GetAlbumId()] != 2 {
				t.Errorf("expected DownloadLinkFailures[%d] untouched (2), got %v", tc.item.GetAlbumId(), saved.GetDownloadLinkFailures())
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

func TestClientUpdate_ExistingOpenIssueDoesNotStarveSubsequentItems(t *testing.T) {
	fixedNow := time.Unix(1700015000, 0)

	t.Run("does not starve subsequent unmapped item while preserving one issue per tick", func(t *testing.T) {
		item1 := &bcpb.Item{
			AlbumId:    301,
			BandName:   "Already Tracked Artist",
			AlbumTitle: "Already Tracked Album",
		}
		item2 := &bcpb.Item{
			AlbumId:    302,
			BandName:   "Second Artist",
			AlbumTitle: "Second Album",
		}
		item3 := &bcpb.Item{
			AlbumId:    303,
			BandName:   "Third Artist",
			AlbumTitle: "Third Album",
		}

		store := newAPITestConfigStore(t, &pb.Config{
			Token:          "tok-1",
			IdentityCookie: "valid-cookie",
			LastProcess:    fixedNow.Add(-10 * time.Minute).Unix(),
			Items:          []*bcpb.Item{item1, item2, item3},
			IssueIds:       map[int64]int32{301: 55},
		})
		issues := &apiTestIssueManager{nextIssueID: 600}
		rc := newDefaultClientUpdateRC()
		rc.searchFn = func(ctx context.Context, req *rcpb.SearchReleasesRequest) (*rcpb.SearchReleasesResponse, error) {
			return &rcpb.SearchReleasesResponse{
				Results: []*rcpb.SearchReleaseResult{
					{
						ReleaseId: 444001,
						Title:     "Second Artist - Second Album",
						Year:      "2025",
						Formats:   []string{"File", "WAV"},
						Url:       "https://www.discogs.com/release/444001",
					},
				},
			}, nil
		}
		var bcCalls int

		s := &Server{
			store:  store,
			issues: issues,
			rc:     rc,
			bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
				bcCalls++
				return nil, errors.New("bandcamp client must not be called")
			},
			now:     func() time.Time { return fixedNow },
			logFunc: (&apiTestLogger{}).log,
		}

		if _, err := s.ClientUpdate(context.Background(), &rcpb.ClientUpdateRequest{InstanceId: 1}); err != nil {
			t.Fatalf("ClientUpdate failed: %v", err)
		}
		if bcCalls != 0 {
			t.Errorf("expected 0 Bandcamp client calls, got %d", bcCalls)
		}

		calls := issues.immediateSnapshot()
		if len(calls) != 1 {
			t.Fatalf("expected 1 ImmediateIssue call, got %d (%+v)", len(calls), calls)
		}
		if calls[0].title != "Bandcamp entry for Second Artist is missing mapping [302]" {
			t.Errorf("unexpected issue title: %q", calls[0].title)
		}
		if !strings.Contains(calls[0].body, "https://www.discogs.com/release/444001") {
			t.Errorf("expected Discogs candidate in issue body:\n%s", calls[0].body)
		}
		if strings.Contains(strings.ToLower(calls[0].body), "wav download link") {
			t.Errorf("issue body must not contain WAV link text:\n%s", calls[0].body)
		}

		saved := store.currentConfig(t)
		if saved.GetIssueIds()[301] != 55 {
			t.Errorf("IssueIds[301] = %d, want 55", saved.GetIssueIds()[301])
		}
		if saved.GetIssueIds()[302] != 600 {
			t.Errorf("IssueIds[302] = %d, want 600", saved.GetIssueIds()[302])
		}
		if saved.GetIssueIds()[303] != 0 {
			t.Errorf("IssueIds[303] = %d, want 0 (one missing-mapping issue per tick)", saved.GetIssueIds()[303])
		}
	})

	t.Run("does not starve subsequent mapped item AddRecord", func(t *testing.T) {
		unmappedItem := &bcpb.Item{
			AlbumId:    301,
			BandName:   "Already Tracked Artist",
			AlbumTitle: "Already Tracked Album",
		}
		mappedItem := &bcpb.Item{
			AlbumId:    304,
			BandName:   "Mapped Artist",
			AlbumTitle: "Mapped Album",
		}

		store := newAPITestConfigStore(t, &pb.Config{
			Token:            "tok-1",
			IdentityCookie:   "valid-cookie",
			LastProcess:      fixedNow.Add(-10 * time.Minute).Unix(),
			Items:            []*bcpb.Item{unmappedItem, mappedItem},
			IssueIds:         map[int64]int32{301: 55},
			Mapping:          map[int64]int32{304: 9001},
			LastValidateDate: map[int64]int64{304: fixedNow.Unix()},
		})
		issues := &apiTestIssueManager{}
		rc := newDefaultClientUpdateRC()
		rc.queryResp = &rcpb.QueryRecordsResponse{}
		var bcCalls int

		s := &Server{
			store:  store,
			issues: issues,
			rc:     rc,
			bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
				bcCalls++
				return nil, errors.New("bandcamp client must not be called")
			},
			now:     func() time.Time { return fixedNow },
			logFunc: (&apiTestLogger{}).log,
		}

		if _, err := s.ClientUpdate(context.Background(), &rcpb.ClientUpdateRequest{InstanceId: 1}); err != nil {
			t.Fatalf("ClientUpdate failed: %v", err)
		}
		if bcCalls != 0 {
			t.Errorf("expected 0 Bandcamp client calls, got %d", bcCalls)
		}
		if calls := issues.immediateSnapshot(); len(calls) != 0 {
			t.Errorf("expected 0 ImmediateIssue calls, got %d", len(calls))
		}
		rc.mu.Lock()
		addCalls := rc.addCalls
		rc.mu.Unlock()
		if addCalls != 1 {
			t.Errorf("expected 1 AddRecord call, got %d", addCalls)
		}
		saved := store.currentConfig(t)
		if saved.GetAddedDate()[9001] != fixedNow.Unix() {
			t.Errorf("AddedDate[9001] = %d, want %d", saved.GetAddedDate()[9001], fixedNow.Unix())
		}
	})
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

	t.Run("ImmediateIssue failure does not populate IssueIds", func(t *testing.T) {
		store := newAPITestConfigStore(t, &pb.Config{
			IdentityCookie: "valid-cookie",
			LastProcess:    fixedNow.Add(-10 * time.Minute).Unix(),
			Items:          []*bcpb.Item{item},
		})
		issues := &apiTestIssueManager{
			immediateErr: errors.New("github unavailable"),
		}
		var bcCalls int

		s := &Server{
			store:  store,
			issues: issues,
			rc:     newDefaultClientUpdateRC(),
			bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
				bcCalls++
				return nil, errors.New("bandcamp client must not be called")
			},
			now:     func() time.Time { return fixedNow },
			logFunc: (&apiTestLogger{}).log,
		}

		_, err := s.ClientUpdate(context.Background(), &rcpb.ClientUpdateRequest{InstanceId: 1})
		if err == nil {
			t.Fatal("expected error when ImmediateIssue fails, got nil")
		}
		if bcCalls != 0 {
			t.Errorf("expected 0 Bandcamp client calls, got %d", bcCalls)
		}
		saved := store.currentConfig(t)
		if saved.GetIssueIds()[item.GetAlbumId()] != 0 {
			t.Errorf("IssueIds[%d] = %d, want 0", item.GetAlbumId(), saved.GetIssueIds()[item.GetAlbumId()])
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

func TestClientUpdate_MissingMappingWorksWithoutBandcampCookie(t *testing.T) {
	fixedNow := time.Unix(1700020000, 0)
	item := &bcpb.Item{
		AlbumId:    501,
		BandName:   "No Cookie Artist",
		AlbumTitle: "No Cookie Album",
	}
	store := newAPITestConfigStore(t, &pb.Config{
		Token:          "tok-1",
		IdentityCookie: "",
		LastProcess:    fixedNow.Add(-10 * time.Minute).Unix(),
		Items:          []*bcpb.Item{item},
	})
	issues := &apiTestIssueManager{nextIssueID: 700}
	var bcCalls int

	s := &Server{
		store:  store,
		issues: issues,
		rc:     newDefaultClientUpdateRC(),
		bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
			bcCalls++
			return nil, errors.New("bandcamp client must not be invoked")
		},
		now:     func() time.Time { return fixedNow },
		logFunc: (&apiTestLogger{}).log,
	}

	if _, err := s.ClientUpdate(context.Background(), &rcpb.ClientUpdateRequest{InstanceId: 1}); err != nil {
		t.Fatalf("ClientUpdate failed without cookie: %v", err)
	}
	if bcCalls != 0 {
		t.Errorf("expected 0 Bandcamp client calls, got %d", bcCalls)
	}

	calls := issues.immediateSnapshot()
	if len(calls) != 1 {
		t.Fatalf("expected 1 missing-mapping issue, got %d (%+v)", len(calls), calls)
	}
	if calls[0].title != "Bandcamp entry for No Cookie Artist is missing mapping [501]" {
		t.Errorf("unexpected issue title: %q", calls[0].title)
	}
	saved := store.currentConfig(t)
	if saved.GetAuthAlertIssueId() != 0 {
		t.Errorf("AuthAlertIssueId = %d, want 0", saved.GetAuthAlertIssueId())
	}
	if saved.GetIssueIds()[501] != 700 {
		t.Errorf("IssueIds[501] = %d, want 700", saved.GetIssueIds()[501])
	}
}

func TestClientUpdate_ConcurrentSerialization(t *testing.T) {
	fixedNow := time.Unix(1700050000, 0)
	var currentUnix atomic.Int64
	currentUnix.Store(fixedNow.Unix())
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
	var downloadCalls atomic.Int32

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
					downloadCalls.Add(1)
					return "", errors.New("ClientUpdate must not call Bandcamp download resolution")
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

	if got := downloadCalls.Load(); got != 0 {
		t.Errorf("expected 0 Bandcamp download resolution calls, got %d", got)
	}

	saved := store.currentConfig(t)
	if saved.GetMapping()[802] != 4242 {
		t.Errorf("Mapping[802] = %d, want 4242", saved.GetMapping()[802])
	}
	if _, exists := saved.GetDownloadLinkFailures()[802]; exists {
		t.Errorf("expected DownloadLinkFailures[802] cleared by AddMapping, got %v", saved.GetDownloadLinkFailures())
	}
	if saved.GetIdentityCookie() != setCookieVal {
		t.Errorf("unexpected IdentityCookie = %q, want %q", saved.GetIdentityCookie(), setCookieVal)
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

	assertNoSecretLeak := func(t *testing.T, logger *apiTestLogger, issues *apiTestIssueManager, err error) {
		t.Helper()
		if err != nil && strings.Contains(err.Error(), initialCookie) {
			t.Errorf("returned error leaked secret %q: %v", initialCookie, err)
		}
		for _, entry := range logger.entries() {
			if strings.Contains(entry, initialCookie) {
				t.Errorf("log entry leaked secret %q: %q", initialCookie, entry)
			}
		}
		for _, call := range issues.immediateSnapshot() {
			if strings.Contains(call.title, initialCookie) || strings.Contains(call.body, initialCookie) {
				t.Errorf("GitHub issue leaked secret %q: title=%q body=%q", initialCookie, call.title, call.body)
			}
		}
	}

	t.Run("Discogs search error redacts cookie", func(t *testing.T) {
		store := newAPITestConfigStore(t, &pb.Config{
			IdentityCookie: initialCookie,
			LastProcess:    fixedNow.Add(-10 * time.Minute).Unix(),
			Items: []*bcpb.Item{
				{AlbumId: 901, BandName: "Secret Artist 1", AlbumTitle: "Secret Album 1"},
			},
		})
		issues := &apiTestIssueManager{nextIssueID: 950}
		logger := &apiTestLogger{}
		rc := newDefaultClientUpdateRC()
		rc.searchFn = func(ctx context.Context, req *rcpb.SearchReleasesRequest) (*rcpb.SearchReleasesResponse, error) {
			return nil, fmt.Errorf("search failed cookie=%s", initialCookie)
		}
		var bcCalls int
		s := &Server{
			store:  store,
			issues: issues,
			rc:     rc,
			bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
				bcCalls++
				return nil, errors.New("bandcamp client must not be called")
			},
			now:     func() time.Time { return fixedNow },
			logFunc: logger.log,
		}

		_, err := s.ClientUpdate(context.Background(), &rcpb.ClientUpdateRequest{InstanceId: 1})
		if err != nil {
			t.Fatalf("unexpected ClientUpdate error: %v", err)
		}
		if bcCalls != 0 {
			t.Errorf("expected 0 Bandcamp client calls, got %d", bcCalls)
		}
		assertNoSecretLeak(t, logger, issues, err)
		if got := countLogsContaining(logger.entries(), "901", "[REDACTED]"); got < 1 {
			t.Errorf("expected log entry containing 901 and [REDACTED]; logs=%v", logger.entries())
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
			immediateErr: fmt.Errorf("githubcard failed with %s", initialCookie),
		}
		logger := &apiTestLogger{}
		var bcCalls int
		s := &Server{
			store:  store,
			issues: issues,
			rc:     newDefaultClientUpdateRC(),
			bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
				bcCalls++
				return nil, errors.New("bandcamp client must not be called")
			},
			now:     func() time.Time { return fixedNow },
			logFunc: logger.log,
		}

		_, err := s.ClientUpdate(context.Background(), &rcpb.ClientUpdateRequest{InstanceId: 1})
		if err == nil {
			t.Fatal("expected ImmediateIssue error, got nil")
		}
		if bcCalls != 0 {
			t.Errorf("expected 0 Bandcamp client calls, got %d", bcCalls)
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
		store.saveErr = fmt.Errorf("dstore failed with %s", initialCookie)
		issues := &apiTestIssueManager{}
		logger := &apiTestLogger{}
		var bcCalls int
		s := &Server{
			store:  store,
			issues: issues,
			rc:     newDefaultClientUpdateRC(),
			bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
				bcCalls++
				return nil, errors.New("bandcamp client must not be called")
			},
			now:     func() time.Time { return fixedNow },
			logFunc: logger.log,
		}

		_, err := s.ClientUpdate(context.Background(), &rcpb.ClientUpdateRequest{InstanceId: 1})
		if err == nil {
			t.Fatal("expected saveConfig error, got nil")
		}
		if bcCalls != 0 {
			t.Errorf("expected 0 Bandcamp client calls, got %d", bcCalls)
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

func TestClientUpdate_DiscogsCandidates_Digital(t *testing.T) {
	fixedNow := time.Unix(1700080000, 0)
	item := &bcpb.Item{
		AlbumId:       1101,
		BandName:      "Album Artist",
		AlbumTitle:    "Full Album Title",
		RedownloadUrl: "https://bandcamp.com/download/album?id=1101",
	}

	store := newAPITestConfigStore(t, &pb.Config{
		Token:                "tok-1",
		IdentityCookie:       "valid-cookie-secret",
		LastProcess:          fixedNow.Add(-10 * time.Minute).Unix(),
		Items:                []*bcpb.Item{item},
		DownloadLinkFailures: map[int64]int32{item.GetAlbumId(): 1},
	})
	issues := &apiTestIssueManager{nextIssueID: 801}
	logger := &apiTestLogger{}
	rc := newDefaultClientUpdateRC()
	rc.searchFn = func(ctx context.Context, req *rcpb.SearchReleasesRequest) (*rcpb.SearchReleasesResponse, error) {
		return &rcpb.SearchReleasesResponse{
			Results: []*rcpb.SearchReleaseResult{
				{
					ReleaseId: 555001,
					Title:     "Album Artist - Full Album Title",
					Year:      "2024",
					Formats:   []string{"File", "WAV"},
					Url:       "https://www.discogs.com/release/555001",
				},
			},
		}, nil
	}
	var bcCalls int

	s := &Server{
		store:  store,
		issues: issues,
		rc:     rc,
		bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
			bcCalls++
			return nil, errors.New("bandcamp client must not be called")
		},
		now:     func() time.Time { return fixedNow },
		logFunc: logger.log,
	}

	if _, err := s.ClientUpdate(context.Background(), &rcpb.ClientUpdateRequest{InstanceId: 1}); err != nil {
		t.Fatalf("ClientUpdate failed: %v", err)
	}
	if bcCalls != 0 {
		t.Errorf("expected 0 Bandcamp client calls, got %d", bcCalls)
	}

	rc.mu.Lock()
	searchCalls := append([]*rcpb.SearchReleasesRequest(nil), rc.searchCalls...)
	rc.mu.Unlock()
	if len(searchCalls) != 1 {
		t.Fatalf("expected 1 SearchReleases call, got %d", len(searchCalls))
	}
	if searchCalls[0].GetArtist() != item.GetBandName() || searchCalls[0].GetReleaseTitle() != item.GetAlbumTitle() || searchCalls[0].GetFormat() != "File" || searchCalls[0].GetPerPage() != 10 {
		t.Errorf("unexpected SearchReleases request: %+v", searchCalls[0])
	}

	calls := issues.immediateSnapshot()
	if len(calls) != 1 {
		t.Fatalf("expected 1 ImmediateIssue call, got %d", len(calls))
	}
	wantTitle := fmt.Sprintf("Bandcamp entry for %v is missing mapping [%v]", item.GetBandName(), item.GetAlbumId())
	if calls[0].title != wantTitle {
		t.Errorf("issue title = %q, want %q", calls[0].title, wantTitle)
	}
	for _, wantSubstr := range []string{
		fmt.Sprintf("%v - %v (%v) is missing a mapping", item.GetBandName(), item.GetAlbumTitle(), item.GetAlbumId()),
		"### Candidate Discogs Digital Releases",
		"https://www.discogs.com/release/555001",
		"bandcampserver_cli mapping -bandcamp 1101 -discogs 555001",
	} {
		if !strings.Contains(calls[0].body, wantSubstr) {
			t.Errorf("issue body missing %q:\n%s", wantSubstr, calls[0].body)
		}
	}
	for _, forbidden := range []string{
		"Direct WAV download link",
		"time-limited",
		"expire",
		"WAV download link generation failed",
	} {
		if strings.Contains(strings.ToLower(calls[0].body), strings.ToLower(forbidden)) {
			t.Errorf("issue body must not contain %q:\n%s", forbidden, calls[0].body)
		}
	}

	saved := store.currentConfig(t)
	if saved.GetIssueIds()[item.GetAlbumId()] != 801 {
		t.Errorf("IssueIds[%d] = %d, want 801", item.GetAlbumId(), saved.GetIssueIds()[item.GetAlbumId()])
	}
}

func TestClientUpdate_DiscogsCandidates_NonDigitalFallback(t *testing.T) {
	fixedNow := time.Unix(1700081000, 0)
	item := &bcpb.Item{
		AlbumId:       1102,
		BandName:      "Vinyl Artist",
		AlbumTitle:    "Vinyl Album",
		RedownloadUrl: "https://bandcamp.com/download/album?id=1102",
	}

	store := newAPITestConfigStore(t, &pb.Config{
		Token:          "tok-1",
		IdentityCookie: "valid-cookie",
		LastProcess:    fixedNow.Add(-10 * time.Minute).Unix(),
		Items:          []*bcpb.Item{item},
	})
	issues := &apiTestIssueManager{nextIssueID: 802}
	rc := newDefaultClientUpdateRC()
	rc.searchFn = func(ctx context.Context, req *rcpb.SearchReleasesRequest) (*rcpb.SearchReleasesResponse, error) {
		if req.GetFormat() == "File" {
			return &rcpb.SearchReleasesResponse{}, nil
		}
		return &rcpb.SearchReleasesResponse{
			Results: []*rcpb.SearchReleaseResult{
				{
					ReleaseId: 666002,
					Title:     "Vinyl Artist - Vinyl Album",
					Year:      "2023",
					Formats:   []string{"Vinyl", "LP"},
					Url:       "https://www.discogs.com/release/666002",
				},
			},
		}, nil
	}
	var bcCalls int

	s := &Server{
		store:  store,
		issues: issues,
		rc:     rc,
		bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
			bcCalls++
			return nil, errors.New("bandcamp client must not be called")
		},
		now:     func() time.Time { return fixedNow },
		logFunc: (&apiTestLogger{}).log,
	}

	if _, err := s.ClientUpdate(context.Background(), &rcpb.ClientUpdateRequest{InstanceId: 1}); err != nil {
		t.Fatalf("ClientUpdate failed: %v", err)
	}
	if bcCalls != 0 {
		t.Errorf("expected 0 Bandcamp client calls, got %d", bcCalls)
	}

	rc.mu.Lock()
	searchCalls := append([]*rcpb.SearchReleasesRequest(nil), rc.searchCalls...)
	rc.mu.Unlock()
	if len(searchCalls) != 2 {
		t.Fatalf("expected 2 SearchReleases calls (primary + fallback), got %d", len(searchCalls))
	}
	if searchCalls[0].GetFormat() != "File" {
		t.Errorf("first SearchReleases call Format = %q, want %q", searchCalls[0].GetFormat(), "File")
	}
	if searchCalls[1].GetFormat() != "" {
		t.Errorf("second SearchReleases call Format = %q, want empty", searchCalls[1].GetFormat())
	}

	calls := issues.immediateSnapshot()
	if len(calls) != 1 {
		t.Fatalf("expected 1 ImmediateIssue call, got %d", len(calls))
	}
	for _, wantSubstr := range []string{
		"### Discogs Candidate Releases (Reference Only)",
		"Vinyl Artist - Vinyl Album",
		"2023 — Vinyl, LP",
		"https://www.discogs.com/release/666002",
	} {
		if !strings.Contains(calls[0].body, wantSubstr) {
			t.Errorf("issue body missing %q:\n%s", wantSubstr, calls[0].body)
		}
	}
	for _, forbidden := range []string{
		"bandcampserver_cli mapping",
		"Direct WAV download link",
		"time-limited",
		"expire",
		"WAV download link generation failed",
	} {
		if strings.Contains(strings.ToLower(calls[0].body), strings.ToLower(forbidden)) {
			t.Errorf("non-digital fallback issue body must not contain %q:\n%s", forbidden, calls[0].body)
		}
	}
}

func TestClientUpdate_DiscogsCandidates_SearchFailureResilience(t *testing.T) {
	fixedNow := time.Unix(1700083000, 0)
	const secretCookie = "super-secret-search-err-cookie"

	item := &bcpb.Item{
		AlbumId:       1104,
		BandName:      "Resilient Artist",
		AlbumTitle:    "Resilient Album",
		RedownloadUrl: "https://bandcamp.com/download/album?id=1104",
	}
	store := newAPITestConfigStore(t, &pb.Config{
		Token:          "tok-1",
		IdentityCookie: secretCookie,
		LastProcess:    fixedNow.Add(-10 * time.Minute).Unix(),
		Items:          []*bcpb.Item{item},
	})
	issues := &apiTestIssueManager{nextIssueID: 804}
	logger := &apiTestLogger{}
	rc := newDefaultClientUpdateRC()
	rc.searchFn = func(ctx context.Context, req *rcpb.SearchReleasesRequest) (*rcpb.SearchReleasesResponse, error) {
		return nil, fmt.Errorf("rpc error: code = DeadlineExceeded desc = upstream failed cookie=%s", secretCookie)
	}
	var bcCalls int

	s := &Server{
		store:  store,
		issues: issues,
		rc:     rc,
		bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
			bcCalls++
			return nil, errors.New("bandcamp client must not be called")
		},
		now:     func() time.Time { return fixedNow },
		logFunc: logger.log,
	}

	if _, err := s.ClientUpdate(context.Background(), &rcpb.ClientUpdateRequest{InstanceId: 1}); err != nil {
		t.Fatalf("ClientUpdate should not fail when SearchReleases fails: %v", err)
	}
	if bcCalls != 0 {
		t.Errorf("expected 0 Bandcamp client calls, got %d", bcCalls)
	}

	calls := issues.immediateSnapshot()
	if len(calls) != 1 {
		t.Fatalf("expected 1 ImmediateIssue call, got %d", len(calls))
	}
	for _, want := range []string{
		"Resilient Artist - Resilient Album (1104) is missing a mapping",
		"Note: Discogs candidate lookup failed; please search Discogs manually.",
	} {
		if !strings.Contains(calls[0].body, want) {
			t.Errorf("issue body missing %q:\n%s", want, calls[0].body)
		}
	}
	for _, forbidden := range []string{
		"No candidate Discogs releases were found",
		"DeadlineExceeded",
		"upstream failed",
		"Direct WAV download link",
		"WAV download link generation failed",
		secretCookie,
	} {
		if strings.Contains(calls[0].body, forbidden) {
			t.Errorf("issue body must not contain %q:\n%s", forbidden, calls[0].body)
		}
	}
	for _, entry := range logger.entries() {
		if strings.Contains(entry, secretCookie) {
			t.Errorf("log entry leaked cookie secret: %q", entry)
		}
	}

	saved := store.currentConfig(t)
	if saved.GetIssueIds()[item.GetAlbumId()] != 804 {
		t.Errorf("IssueIds[1104] = %d, want 804", saved.GetIssueIds()[item.GetAlbumId()])
	}
}

func TestClientUpdate_DiscogsCandidates_NotCalledWhenNoIssueOpened(t *testing.T) {
	fixedNow := time.Unix(1700084000, 0)
	baseItem := &bcpb.Item{
		AlbumId:       1105,
		BandName:      "Guarded Artist",
		AlbumTitle:    "Guarded Album",
		RedownloadUrl: "https://bandcamp.com/download/album?id=1105",
	}

	t.Run("existing tracked issue", func(t *testing.T) {
		store := newAPITestConfigStore(t, &pb.Config{
			IdentityCookie: "valid-cookie",
			LastProcess:    fixedNow.Add(-10 * time.Minute).Unix(),
			Items:          []*bcpb.Item{baseItem},
			IssueIds:       map[int64]int32{baseItem.GetAlbumId(): 42},
		})
		rc := newDefaultClientUpdateRC()
		var bcCalls int
		s := &Server{
			store:  store,
			issues: &apiTestIssueManager{},
			rc:     rc,
			bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
				bcCalls++
				return nil, errors.New("bandcamp client must not be called")
			},
			now:     func() time.Time { return fixedNow },
			logFunc: (&apiTestLogger{}).log,
		}

		if _, err := s.ClientUpdate(context.Background(), &rcpb.ClientUpdateRequest{InstanceId: 1}); err != nil {
			t.Fatalf("ClientUpdate failed: %v", err)
		}
		if bcCalls != 0 {
			t.Errorf("expected 0 Bandcamp client calls, got %d", bcCalls)
		}
		rc.mu.Lock()
		got := len(rc.searchCalls)
		rc.mu.Unlock()
		if got != 0 {
			t.Errorf("expected 0 SearchReleases calls for existing tracked issue, got %d", got)
		}
	})

	t.Run("cooldown skip", func(t *testing.T) {
		store := newAPITestConfigStore(t, &pb.Config{
			IdentityCookie: "valid-cookie",
			LastProcess:    fixedNow.Add(-1 * time.Minute).Unix(),
			Items:          []*bcpb.Item{baseItem},
		})
		rc := newDefaultClientUpdateRC()
		var bcCalls int
		s := &Server{
			store:  store,
			issues: &apiTestIssueManager{},
			rc:     rc,
			bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
				bcCalls++
				return nil, errors.New("bandcamp client must not be called")
			},
			now:     func() time.Time { return fixedNow },
			logFunc: (&apiTestLogger{}).log,
		}

		if _, err := s.ClientUpdate(context.Background(), &rcpb.ClientUpdateRequest{InstanceId: 1}); err != nil {
			t.Fatalf("ClientUpdate failed: %v", err)
		}
		if bcCalls != 0 {
			t.Errorf("expected 0 Bandcamp client calls, got %d", bcCalls)
		}
		rc.mu.Lock()
		got := len(rc.searchCalls)
		rc.mu.Unlock()
		if got != 0 {
			t.Errorf("expected 0 SearchReleases calls during cooldown, got %d", got)
		}
	})

	t.Run("daily limit skip", func(t *testing.T) {
		added := make(map[int32]int64, 30)
		for i := int32(1); i <= 30; i++ {
			added[i] = fixedNow.Add(-1 * time.Hour).Unix()
		}
		store := newAPITestConfigStore(t, &pb.Config{
			IdentityCookie: "valid-cookie",
			LastProcess:    fixedNow.Add(-10 * time.Minute).Unix(),
			AddedDate:      added,
			Items:          []*bcpb.Item{baseItem},
		})
		rc := newDefaultClientUpdateRC()
		var bcCalls int
		s := &Server{
			store:  store,
			issues: &apiTestIssueManager{},
			rc:     rc,
			bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
				bcCalls++
				return nil, errors.New("bandcamp client must not be called")
			},
			now:     func() time.Time { return fixedNow },
			logFunc: (&apiTestLogger{}).log,
		}

		if _, err := s.ClientUpdate(context.Background(), &rcpb.ClientUpdateRequest{InstanceId: 1}); err != nil {
			t.Fatalf("ClientUpdate failed: %v", err)
		}
		if bcCalls != 0 {
			t.Errorf("expected 0 Bandcamp client calls, got %d", bcCalls)
		}
		rc.mu.Lock()
		got := len(rc.searchCalls)
		rc.mu.Unlock()
		if got != 0 {
			t.Errorf("expected 0 SearchReleases calls at daily limit, got %d", got)
		}
	})
}

func TestClientUpdate_DiscogsCandidates_CookieRedaction(t *testing.T) {
	fixedNow := time.Unix(1700085000, 0)
	const initialCookie = "secret-missing-mapping-cookie"

	item := &bcpb.Item{
		AlbumId:       1106,
		BandName:      "Band " + initialCookie,
		AlbumTitle:    "Album " + initialCookie,
		RedownloadUrl: "https://bandcamp.com/download/album?id=1106",
	}
	store := newAPITestConfigStore(t, &pb.Config{
		Token:          "tok-1",
		IdentityCookie: initialCookie,
		LastProcess:    fixedNow.Add(-10 * time.Minute).Unix(),
		Items:          []*bcpb.Item{item},
	})
	issues := &apiTestIssueManager{nextIssueID: 806}
	logger := &apiTestLogger{}
	rc := newDefaultClientUpdateRC()
	rc.searchFn = func(ctx context.Context, req *rcpb.SearchReleasesRequest) (*rcpb.SearchReleasesResponse, error) {
		return &rcpb.SearchReleasesResponse{
			Results: []*rcpb.SearchReleaseResult{
				{
					ReleaseId: 888004,
					Title:     "Candidate " + initialCookie,
					Year:      "2024",
					Formats:   []string{"File"},
					Url:       "https://www.discogs.com/release/888004?tok=" + initialCookie,
				},
			},
		}, nil
	}
	var bcCalls int

	s := &Server{
		store:  store,
		issues: issues,
		rc:     rc,
		bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
			bcCalls++
			return nil, errors.New("bandcamp client must not be called")
		},
		now:     func() time.Time { return fixedNow },
		logFunc: logger.log,
	}

	if _, err := s.ClientUpdate(context.Background(), &rcpb.ClientUpdateRequest{InstanceId: 1}); err != nil {
		t.Fatalf("ClientUpdate failed: %v", err)
	}
	if bcCalls != 0 {
		t.Errorf("expected 0 Bandcamp client calls, got %d", bcCalls)
	}

	calls := issues.immediateSnapshot()
	if len(calls) != 1 {
		t.Fatalf("expected 1 ImmediateIssue call, got %d", len(calls))
	}
	if !strings.Contains(calls[0].body, "[REDACTED]") {
		t.Errorf("expected [REDACTED] in issue body:\n%s", calls[0].body)
	}
	if strings.Contains(calls[0].body, initialCookie) {
		t.Errorf("issue body leaked secret %q:\n%s", initialCookie, calls[0].body)
	}
	for _, forbidden := range []string{
		"Direct WAV download link",
		"WAV download link generation failed",
	} {
		if strings.Contains(calls[0].body, forbidden) {
			t.Errorf("issue body must not contain %q:\n%s", forbidden, calls[0].body)
		}
	}
}
