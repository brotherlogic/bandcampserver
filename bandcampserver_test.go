package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/brotherlogic/bandcamplib"
	bcpb "github.com/brotherlogic/bandcamplib/proto"
	pb "github.com/brotherlogic/bandcampserver/proto"
	cdpb "github.com/brotherlogic/cdprocessor/proto"
	recordcollection_client "github.com/brotherlogic/recordcollection/client"
	rcpb "github.com/brotherlogic/recordcollection/proto"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type fakeConfigStore struct {
	loadFn func(ctx context.Context, key string) ([]byte, error)
	saveFn func(ctx context.Context, data []byte, key string) error
}

func (f *fakeConfigStore) Load(ctx context.Context, key string) ([]byte, error) {
	return f.loadFn(ctx, key)
}

func (f *fakeConfigStore) Save(ctx context.Context, data []byte, key string) error {
	if f.saveFn != nil {
		return f.saveFn(ctx, data, key)
	}
	return nil
}

func TestInitDefaults(t *testing.T) {
	s := Init()
	if s == nil {
		t.Fatal("Init() returned nil")
	}
	if s.store == nil {
		t.Error("Init() left s.store nil")
	}
	if s.bcFactory == nil {
		t.Error("Init() left s.bcFactory nil")
	}
	if s.issues == nil {
		t.Error("Init() left s.issues nil")
	}
	if s.rc == nil {
		t.Error("Init() left s.rc nil")
	}
	if s.cdClient == nil {
		t.Error("Init() left s.cdClient nil")
	}
	if s.now == nil {
		t.Error("Init() left s.now nil")
	}

	s.mu.Lock()
	s.mu.Unlock()

	client, err := s.bcFactory(&bcpb.Config{User: 1, IdentityCookie: "abc"}, func(string) {})
	if err != nil {
		t.Fatalf("s.bcFactory returned error: %v", err)
	}
	if client == nil {
		t.Fatal("s.bcFactory returned nil client")
	}
	if got := client.IdentityCookie(); got != "abc" {
		t.Errorf("client.IdentityCookie() = %q, want %q", got, "abc")
	}
}

func TestLoadConfig_InvalidArgumentInitializesMapsAndMetrics(t *testing.T) {
	// Pre-dirty the gauges to confirm loadConfig resets them on InvalidArgument.
	cookieAge.Set(999)
	authValid.Set(1)
	downloadJobs.WithLabelValues("pending").Set(5)
	downloadJobs.WithLabelValues("submitted").Set(3)
	downloadJobs.WithLabelValues("failed").Set(2)

	s := &Server{
		store: &fakeConfigStore{
			loadFn: func(ctx context.Context, key string) ([]byte, error) {
				return nil, status.Error(codes.InvalidArgument, "not found")
			},
		},
		now:     func() time.Time { return time.Unix(1700000000, 0) },
		logFunc: func(ctx context.Context, message string) {},
	}

	cfg, err := s.loadConfig(context.Background())
	if err != nil {
		t.Fatalf("loadConfig returned unexpected error: %v", err)
	}
	if cfg == nil {
		t.Fatal("loadConfig returned nil config")
	}
	if cfg.Mapping == nil {
		t.Error("cfg.Mapping is nil")
	}
	if cfg.IssueIds == nil {
		t.Error("cfg.IssueIds is nil")
	}
	if cfg.AddedDate == nil {
		t.Error("cfg.AddedDate is nil")
	}
	if cfg.LastValidateDate == nil {
		t.Error("cfg.LastValidateDate is nil")
	}
	if cfg.DownloadLinkFailures == nil {
		t.Error("cfg.DownloadLinkFailures is nil")
	}
	if cfg.PendingDownloads == nil {
		t.Error("cfg.PendingDownloads is nil")
	}

	if got := testutil.ToFloat64(cookieAge); got != 0 {
		t.Errorf("cookieAge = %v, want 0", got)
	}
	if got := testutil.ToFloat64(authValid); got != 0 {
		t.Errorf("authValid = %v, want 0", got)
	}
	for _, state := range []string{"pending", "submitted", "failed"} {
		if got := testutil.ToFloat64(downloadJobs.WithLabelValues(state)); got != 0 {
			t.Errorf("downloadJobs[%q] = %v, want 0", state, got)
		}
	}
}

func TestLoadConfig_PersistedConfigInitializesMapsAndAuthMetrics(t *testing.T) {
	tests := []struct {
		name           string
		cfg            *pb.Config
		wantCookieAge  float64
		wantAuthValid  float64
	}{
		{
			name: "valid cookie and no alert",
			cfg: &pb.Config{
				Token:             "tok",
				IdentityCookie:    "valid-cookie",
				LastCookieRefresh: 1700000123,
				AuthAlertIssueId:  0,
			},
			wantCookieAge: 1700000123,
			wantAuthValid: 1,
		},
		{
			name: "empty cookie",
			cfg: &pb.Config{
				Token:             "tok",
				IdentityCookie:    "",
				LastCookieRefresh: 0,
				AuthAlertIssueId:  0,
			},
			wantCookieAge: 0,
			wantAuthValid: 0,
		},
		{
			name: "whitespace-only cookie",
			cfg: &pb.Config{
				Token:             "tok",
				IdentityCookie:    "   \t\n  ",
				LastCookieRefresh: 1700000200,
				AuthAlertIssueId:  0,
			},
			wantCookieAge: 1700000200,
			wantAuthValid: 0,
		},
		{
			name: "valid cookie with active auth alert",
			cfg: &pb.Config{
				Token:             "tok",
				IdentityCookie:    "valid-cookie",
				LastCookieRefresh: 1700000456,
				AuthAlertIssueId:  42,
			},
			wantCookieAge: 1700000456,
			wantAuthValid: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := proto.Marshal(tc.cfg)
			if err != nil {
				t.Fatalf("proto.Marshal failed: %v", err)
			}

			s := &Server{
				store: &fakeConfigStore{
					loadFn: func(ctx context.Context, key string) ([]byte, error) {
						return raw, nil
					},
				},
				now:     func() time.Time { return time.Unix(1700000500, 0) },
				logFunc: func(ctx context.Context, message string) {},
			}

			gotCfg, err := s.loadConfig(context.Background())
			if err != nil {
				t.Fatalf("loadConfig failed: %v", err)
			}
			if gotCfg.Mapping == nil || gotCfg.IssueIds == nil || gotCfg.AddedDate == nil || gotCfg.LastValidateDate == nil || gotCfg.DownloadLinkFailures == nil || gotCfg.PendingDownloads == nil {
				t.Errorf("expected all maps to be non-nil, got Mapping=%v IssueIds=%v AddedDate=%v LastValidateDate=%v DownloadLinkFailures=%v PendingDownloads=%v",
					gotCfg.Mapping != nil, gotCfg.IssueIds != nil, gotCfg.AddedDate != nil, gotCfg.LastValidateDate != nil, gotCfg.DownloadLinkFailures != nil, gotCfg.PendingDownloads != nil)
			}
			if got := testutil.ToFloat64(cookieAge); got != tc.wantCookieAge {
				t.Errorf("loadConfig cookieAge = %v, want %v", got, tc.wantCookieAge)
			}
			if got := testutil.ToFloat64(authValid); got != tc.wantAuthValid {
				t.Errorf("loadConfig authValid = %v, want %v", got, tc.wantAuthValid)
			}

			// Reset gauges and verify s.metrics also updates both gauges.
			cookieAge.Set(-1)
			authValid.Set(-1)
			s.metrics(context.Background(), gotCfg)
			if got := testutil.ToFloat64(cookieAge); got != tc.wantCookieAge {
				t.Errorf("metrics cookieAge = %v, want %v", got, tc.wantCookieAge)
			}
			if got := testutil.ToFloat64(authValid); got != tc.wantAuthValid {
				t.Errorf("metrics authValid = %v, want %v", got, tc.wantAuthValid)
			}
		})
	}
}

func TestLoadConfig_SecretFreeLogging(t *testing.T) {
	const secretCookie = "super-secret-bandcamp-cookie-98765"
	raw, err := proto.Marshal(&pb.Config{
		Token:          "public-ish-token",
		IdentityCookie: secretCookie,
	})
	if err != nil {
		t.Fatalf("proto.Marshal failed: %v", err)
	}

	var logs []string
	s := &Server{
		store: &fakeConfigStore{
			loadFn: func(ctx context.Context, key string) ([]byte, error) {
				return raw, nil
			},
		},
		now: func() time.Time { return time.Unix(1700000000, 0) },
		logFunc: func(ctx context.Context, message string) {
			logs = append(logs, message)
		},
	}

	if _, err := s.loadConfig(context.Background()); err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}

	for _, entry := range logs {
		if strings.Contains(entry, secretCookie) {
			t.Errorf("log entry leaked identity cookie: %q", entry)
		}
	}
}

func TestRefreshStartupToken_ActiveContextAndResilience(t *testing.T) {
	const secretCookie = "super-secret-bandcamp-cookie-98765"
	fixedNow := time.Unix(1700000999, 0)

	var logs []string
	s := &Server{
		now: func() time.Time { return fixedNow },
		logFunc: func(ctx context.Context, message string) {
			logs = append(logs, message)
		},
	}

	type callRecord struct {
		token  string
		ctxErr error
	}
	var calls []callRecord

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := &pb.Config{
		Token:          "saved-token",
		IdentityCookie: secretCookie,
	}

	s.refreshStartupToken(ctx, cfg, func(callCtx context.Context, req *pb.SetTokenRequest) (*pb.SetTokenResponse, error) {
		calls = append(calls, callRecord{
			token:  req.GetToken(),
			ctxErr: callCtx.Err(),
		})
		return nil, fmt.Errorf("%w: cookie=%s", bandcamplib.ErrAuthenticationFailed, "unexpected")
	})

	if len(calls) != 2 {
		t.Fatalf("expected 2 SetToken calls, got %d", len(calls))
	}
	wantFirst := fmt.Sprintf("%v::a::", fixedNow.Unix())
	if calls[0].token != wantFirst {
		t.Errorf("first SetToken token = %q, want %q", calls[0].token, wantFirst)
	}
	if calls[1].token != "saved-token" {
		t.Errorf("second SetToken token = %q, want %q", calls[1].token, "saved-token")
	}
	for i, c := range calls {
		if c.ctxErr != nil {
			t.Errorf("call %d received canceled/expired ctx: %v", i, c.ctxErr)
		}
	}

	for _, entry := range logs {
		if strings.Contains(entry, secretCookie) {
			t.Errorf("startup log leaked identity cookie: %q", entry)
		}
	}
}

func TestProdRCClient_SearchReleases(t *testing.T) {
	t.Run("delegates request and returns response", func(t *testing.T) {
		var gotReq *rcpb.SearchReleasesRequest
		wantResp := &rcpb.SearchReleasesResponse{
			Results: []*rcpb.SearchReleaseResult{
				{
					ReleaseId: 12345,
					Title:     "Test Artist - Test Album",
					Year:      "2025",
					Formats:   []string{"File", "WAV"},
					Url:       "https://www.discogs.com/release/12345",
				},
			},
		}

		s := &Server{
			rcclient: &recordcollection_client.RecordCollectionClient{
				Test: true,
				SearchFunc: func(ctx context.Context, req *rcpb.SearchReleasesRequest) (*rcpb.SearchReleasesResponse, error) {
					gotReq = req
					return wantResp, nil
				},
			},
		}
		var rc rcClient = &prodRCClient{server: s}

		req := &rcpb.SearchReleasesRequest{
			Artist:       "Test Artist",
			ReleaseTitle: "Test Album",
			Format:       "File",
			PerPage:      10,
		}
		gotResp, err := rc.SearchReleases(context.Background(), req)
		if err != nil {
			t.Fatalf("SearchReleases returned unexpected error: %v", err)
		}
		if gotReq == nil {
			t.Fatal("SearchFunc was not invoked")
		}
		if gotReq.GetArtist() != req.GetArtist() || gotReq.GetReleaseTitle() != req.GetReleaseTitle() || gotReq.GetFormat() != req.GetFormat() || gotReq.GetPerPage() != req.GetPerPage() {
			t.Errorf("SearchFunc received %+v, want %+v", gotReq, req)
		}
		if !proto.Equal(gotResp, wantResp) {
			t.Errorf("SearchReleases response = %+v, want %+v", gotResp, wantResp)
		}
	})

	t.Run("propagates error", func(t *testing.T) {
		wantErr := errors.New("search failed")
		s := &Server{
			rcclient: &recordcollection_client.RecordCollectionClient{
				Test: true,
				SearchFunc: func(ctx context.Context, req *rcpb.SearchReleasesRequest) (*rcpb.SearchReleasesResponse, error) {
					return nil, wantErr
				},
			},
		}
		var rc rcClient = &prodRCClient{server: s}

		gotResp, err := rc.SearchReleases(context.Background(), &rcpb.SearchReleasesRequest{Artist: "Test Artist"})
		if !errors.Is(err, wantErr) {
			t.Fatalf("SearchReleases error = %v, want %v", err, wantErr)
		}
		if gotResp != nil {
			t.Errorf("expected nil response on error, got %+v", gotResp)
		}
	})
}

func TestDiscogsCandidateLookupTotalMetric(t *testing.T) {
	outcomes := []string{"digital", "fallback", "none", "skipped", "error"}
	for _, outcome := range outcomes {
		before := testutil.ToFloat64(discogsCandidateLookupTotal.WithLabelValues(outcome))
		discogsCandidateLookupTotal.WithLabelValues(outcome).Inc()
		after := testutil.ToFloat64(discogsCandidateLookupTotal.WithLabelValues(outcome))
		if after != before+1 {
			t.Errorf("discogsCandidateLookupTotal[%q] = %v, want %v", outcome, after, before+1)
		}
	}
}

func TestClientUpdateFakeRCClient_SearchReleases(t *testing.T) {
	t.Run("default empty response and records call", func(t *testing.T) {
		fake := &clientUpdateFakeRCClient{}
		req := &rcpb.SearchReleasesRequest{Artist: "Artist", ReleaseTitle: "Album"}
		resp, err := fake.SearchReleases(context.Background(), req)
		if err != nil {
			t.Fatalf("SearchReleases returned unexpected error: %v", err)
		}
		if resp == nil || len(resp.GetResults()) != 0 {
			t.Errorf("expected empty response, got %+v", resp)
		}
		if len(fake.searchCalls) != 1 || fake.searchCalls[0] != req {
			t.Errorf("searchCalls = %+v, want [%+v]", fake.searchCalls, req)
		}
	})

	t.Run("delegates to searchFn when provided", func(t *testing.T) {
		wantResp := &rcpb.SearchReleasesResponse{
			Results: []*rcpb.SearchReleaseResult{{ReleaseId: 99, Title: "Match"}},
		}
		wantErr := errors.New("boom")
		fake := &clientUpdateFakeRCClient{
			searchFn: func(ctx context.Context, req *rcpb.SearchReleasesRequest) (*rcpb.SearchReleasesResponse, error) {
				return wantResp, wantErr
			},
		}
		req := &rcpb.SearchReleasesRequest{Artist: "Artist 2"}
		resp, err := fake.SearchReleases(context.Background(), req)
		if !errors.Is(err, wantErr) {
			t.Fatalf("error = %v, want %v", err, wantErr)
		}
		if !proto.Equal(resp, wantResp) {
			t.Errorf("resp = %+v, want %+v", resp, wantResp)
		}
		if len(fake.searchCalls) != 1 || fake.searchCalls[0] != req {
			t.Errorf("searchCalls = %+v, want [%+v]", fake.searchCalls, req)
		}
	})
}

type fakeCDProcessorClient struct {
	downloadFn func(ctx context.Context, req *cdpb.DownloadReleaseRequest) (*cdpb.DownloadReleaseResponse, error)
	statusFn   func(ctx context.Context, req *cdpb.GetDownloadStatusRequest) (*cdpb.GetDownloadStatusResponse, error)
}

func (f *fakeCDProcessorClient) DownloadRelease(ctx context.Context, req *cdpb.DownloadReleaseRequest) (*cdpb.DownloadReleaseResponse, error) {
	if f.downloadFn != nil {
		return f.downloadFn(ctx, req)
	}
	return &cdpb.DownloadReleaseResponse{}, nil
}

func (f *fakeCDProcessorClient) GetDownloadStatus(ctx context.Context, req *cdpb.GetDownloadStatusRequest) (*cdpb.GetDownloadStatusResponse, error) {
	if f.statusFn != nil {
		return f.statusFn(ctx, req)
	}
	return &cdpb.GetDownloadStatusResponse{}, nil
}

type validateFakeRCClient struct {
	queryCalls []*rcpb.QueryRecordsRequest
	queryFn    func(ctx context.Context, req *rcpb.QueryRecordsRequest) (*rcpb.QueryRecordsResponse, error)
}

func (f *validateFakeRCClient) GetRecord(ctx context.Context, req *rcpb.GetRecordRequest) (*rcpb.GetRecordResponse, error) {
	return &rcpb.GetRecordResponse{}, nil
}

func (f *validateFakeRCClient) QueryRecords(ctx context.Context, req *rcpb.QueryRecordsRequest) (*rcpb.QueryRecordsResponse, error) {
	f.queryCalls = append(f.queryCalls, req)
	if f.queryFn != nil {
		return f.queryFn(ctx, req)
	}
	return &rcpb.QueryRecordsResponse{}, nil
}

func (f *validateFakeRCClient) AddRecord(ctx context.Context, req *rcpb.AddRecordRequest) (*rcpb.AddRecordResponse, error) {
	return &rcpb.AddRecordResponse{}, nil
}

func (f *validateFakeRCClient) SearchReleases(ctx context.Context, req *rcpb.SearchReleasesRequest) (*rcpb.SearchReleasesResponse, error) {
	return &rcpb.SearchReleasesResponse{}, nil
}

func TestLoadConfig_PopulatesDownloadMetrics(t *testing.T) {
	cfg := &pb.Config{
		PendingDownloads: map[int64]*pb.DownloadJob{
			1: {DiscogsId: 101, State: pb.DownloadJobState_DOWNLOAD_JOB_STATE_PENDING},
			2: {DiscogsId: 102, State: pb.DownloadJobState_DOWNLOAD_JOB_STATE_PENDING},
			3: {DiscogsId: 103, State: pb.DownloadJobState_DOWNLOAD_JOB_STATE_SUBMITTED},
			4: {DiscogsId: 104, State: pb.DownloadJobState_DOWNLOAD_JOB_STATE_FAILED},
			5: {DiscogsId: 105, State: pb.DownloadJobState_DOWNLOAD_JOB_STATE_FAILED},
			6: {DiscogsId: 106, State: pb.DownloadJobState_DOWNLOAD_JOB_STATE_FAILED},
		},
	}
	raw, err := proto.Marshal(cfg)
	if err != nil {
		t.Fatalf("proto.Marshal failed: %v", err)
	}

	s := &Server{
		store: &fakeConfigStore{
			loadFn: func(ctx context.Context, key string) ([]byte, error) {
				return raw, nil
			},
		},
		now:     func() time.Time { return time.Unix(1700000000, 0) },
		logFunc: func(ctx context.Context, message string) {},
	}

	if _, err := s.loadConfig(context.Background()); err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}

	want := map[string]float64{
		"pending":   2,
		"submitted": 1,
		"failed":    3,
	}
	for state, wantVal := range want {
		if got := testutil.ToFloat64(downloadJobs.WithLabelValues(state)); got != wantVal {
			t.Errorf("downloadJobs[%q] = %v, want %v", state, got, wantVal)
		}
	}
}

func TestUpdateDownloadMetrics(t *testing.T) {
	cfg := &pb.Config{
		PendingDownloads: map[int64]*pb.DownloadJob{
			10: {State: pb.DownloadJobState_DOWNLOAD_JOB_STATE_PENDING},
			11: {State: pb.DownloadJobState_DOWNLOAD_JOB_STATE_SUBMITTED},
			12: {State: pb.DownloadJobState_DOWNLOAD_JOB_STATE_SUBMITTED},
			13: {State: pb.DownloadJobState_DOWNLOAD_JOB_STATE_FAILED},
			14: {State: pb.DownloadJobState_DOWNLOAD_JOB_STATE_UNSPECIFIED},
			15: nil,
		},
	}

	updateDownloadMetrics(cfg)
	want := map[string]float64{
		"pending":   1,
		"submitted": 2,
		"failed":    1,
	}
	for state, wantVal := range want {
		if got := testutil.ToFloat64(downloadJobs.WithLabelValues(state)); got != wantVal {
			t.Errorf("updateDownloadMetrics downloadJobs[%q] = %v, want %v", state, got, wantVal)
		}
	}

	// Removing all jobs resets every gauge back to 0.
	cfg.PendingDownloads = map[int64]*pb.DownloadJob{}
	updateDownloadMetrics(cfg)
	for _, state := range []string{"pending", "submitted", "failed"} {
		if got := testutil.ToFloat64(downloadJobs.WithLabelValues(state)); got != 0 {
			t.Errorf("after clearing jobs downloadJobs[%q] = %v, want 0", state, got)
		}
	}

	// s.metrics also updates downloadJobs gauges.
	cfg.PendingDownloads = map[int64]*pb.DownloadJob{
		20: {State: pb.DownloadJobState_DOWNLOAD_JOB_STATE_PENDING},
		21: {State: pb.DownloadJobState_DOWNLOAD_JOB_STATE_FAILED},
	}
	for _, state := range []string{"pending", "submitted", "failed"} {
		downloadJobs.WithLabelValues(state).Set(-1)
	}
	s := &Server{
		now: func() time.Time { return time.Unix(1700000000, 0) },
	}
	s.metrics(context.Background(), cfg)
	wantAfterMetrics := map[string]float64{
		"pending":   1,
		"submitted": 0,
		"failed":    1,
	}
	for state, wantVal := range wantAfterMetrics {
		if got := testutil.ToFloat64(downloadJobs.WithLabelValues(state)); got != wantVal {
			t.Errorf("metrics() downloadJobs[%q] = %v, want %v", state, got, wantVal)
		}
	}
}

func TestDownloadTransitionsTotalMetric(t *testing.T) {
	outcomes := []string{
		"submitted",
		"completed",
		"already_exists",
		"retryable_error",
		"permanent_error",
		"auth_paused",
		"failed_exhausted",
	}
	for _, outcome := range outcomes {
		before := testutil.ToFloat64(downloadTransitionsTotal.WithLabelValues(outcome))
		downloadTransitionsTotal.WithLabelValues(outcome).Inc()
		after := testutil.ToFloat64(downloadTransitionsTotal.WithLabelValues(outcome))
		if after != before+1 {
			t.Errorf("downloadTransitionsTotal[%q] = %v, want %v", outcome, after, before+1)
		}
	}
}

func TestGetCDProcessorClient(t *testing.T) {
	t.Run("returns injected fake when set", func(t *testing.T) {
		fake := &fakeCDProcessorClient{}
		s := &Server{cdClient: fake}
		if got := s.getCDProcessorClient(); got != fake {
			t.Errorf("getCDProcessorClient() = %v, want injected fake %v", got, fake)
		}
	})

	t.Run("defaults to prodCDProcessorClient when nil", func(t *testing.T) {
		s := &Server{}
		got := s.getCDProcessorClient()
		prod, ok := got.(*prodCDProcessorClient)
		if !ok {
			t.Fatalf("getCDProcessorClient() returned %T, want *prodCDProcessorClient", got)
		}
		if prod.server != s {
			t.Errorf("prodCDProcessorClient.server = %p, want %p", prod.server, s)
		}
	})
}

func TestValidate_Guards(t *testing.T) {
	fixedNow := time.Unix(1700000000, 0)

	t.Run("skips mid <= 0", func(t *testing.T) {
		rc := &validateFakeRCClient{}
		s := &Server{
			rc:  rc,
			now: func() time.Time { return fixedNow },
		}
		cfg := &pb.Config{
			Mapping: map[int64]int32{
				1: 0,
				2: -1,
			},
			AddedDate:        map[int32]int64{0: 100, -1: 100},
			LastValidateDate: map[int64]int64{},
		}

		if err := s.validate(context.Background(), cfg); err != nil {
			t.Fatalf("validate failed: %v", err)
		}
		if len(rc.queryCalls) != 0 {
			t.Errorf("expected 0 QueryRecords calls, got %d", len(rc.queryCalls))
		}
		if _, ok := cfg.Mapping[1]; !ok {
			t.Error("expected Mapping[1]=0 to be preserved")
		}
		if _, ok := cfg.Mapping[2]; !ok {
			t.Error("expected Mapping[2]=-1 to be preserved")
		}
	})

	t.Run("skips entries with AddedDate[mid] == 0", func(t *testing.T) {
		rc := &validateFakeRCClient{}
		s := &Server{
			rc:  rc,
			now: func() time.Time { return fixedNow },
		}
		cfg := &pb.Config{
			Mapping: map[int64]int32{
				10: 500,
				11: 501,
			},
			AddedDate:        map[int32]int64{501: 0},
			LastValidateDate: map[int64]int64{},
		}

		if err := s.validate(context.Background(), cfg); err != nil {
			t.Fatalf("validate failed: %v", err)
		}
		if len(rc.queryCalls) != 0 {
			t.Errorf("expected 0 QueryRecords calls, got %d", len(rc.queryCalls))
		}
		if cfg.Mapping[10] != 500 || cfg.Mapping[11] != 501 {
			t.Errorf("expected un-added mappings to be preserved, got %v", cfg.Mapping)
		}
	})

	for _, state := range []pb.DownloadJobState{
		pb.DownloadJobState_DOWNLOAD_JOB_STATE_PENDING,
		pb.DownloadJobState_DOWNLOAD_JOB_STATE_SUBMITTED,
		pb.DownloadJobState_DOWNLOAD_JOB_STATE_FAILED,
	} {
		t.Run(fmt.Sprintf("skips entries in PendingDownloads (%s)", state), func(t *testing.T) {
			rc := &validateFakeRCClient{}
			s := &Server{
				rc:  rc,
				now: func() time.Time { return fixedNow },
			}
			cfg := &pb.Config{
				Mapping:          map[int64]int32{20: 600},
				AddedDate:        map[int32]int64{600: fixedNow.Add(-24 * time.Hour).Unix()},
				LastValidateDate: map[int64]int64{},
				PendingDownloads: map[int64]*pb.DownloadJob{
					20: {DiscogsId: 600, State: state},
				},
			}

			if err := s.validate(context.Background(), cfg); err != nil {
				t.Fatalf("validate failed: %v", err)
			}
			if len(rc.queryCalls) != 0 {
				t.Errorf("expected 0 QueryRecords calls, got %d", len(rc.queryCalls))
			}
			if cfg.Mapping[20] != 600 {
				t.Errorf("expected mapping with PendingDownloads entry to be preserved, got %v", cfg.Mapping)
			}
		})
	}

	t.Run("deletes stale eligible mapping", func(t *testing.T) {
		rc := &validateFakeRCClient{
			queryFn: func(ctx context.Context, req *rcpb.QueryRecordsRequest) (*rcpb.QueryRecordsResponse, error) {
				return &rcpb.QueryRecordsResponse{InstanceIds: nil}, nil
			},
		}
		s := &Server{
			rc:  rc,
			now: func() time.Time { return fixedNow },
		}
		cfg := &pb.Config{
			Mapping:          map[int64]int32{30: 700},
			AddedDate:        map[int32]int64{700: fixedNow.Add(-8 * 24 * time.Hour).Unix()},
			LastValidateDate: map[int64]int64{},
			PendingDownloads: map[int64]*pb.DownloadJob{},
		}

		if err := s.validate(context.Background(), cfg); err != nil {
			t.Fatalf("validate failed: %v", err)
		}
		if len(rc.queryCalls) != 1 || rc.queryCalls[0].GetReleaseId() != 700 {
			t.Errorf("expected 1 QueryRecords call for release 700, got %+v", rc.queryCalls)
		}
		if _, exists := cfg.Mapping[30]; exists {
			t.Error("expected stale eligible mapping to be deleted")
		}
		if cfg.LastValidateDate[30] != fixedNow.Unix() {
			t.Errorf("LastValidateDate[30] = %d, want %d", cfg.LastValidateDate[30], fixedNow.Unix())
		}
	})

	t.Run("preserves valid eligible mapping", func(t *testing.T) {
		rc := &validateFakeRCClient{
			queryFn: func(ctx context.Context, req *rcpb.QueryRecordsRequest) (*rcpb.QueryRecordsResponse, error) {
				return &rcpb.QueryRecordsResponse{InstanceIds: []int64{999}}, nil
			},
		}
		s := &Server{
			rc:  rc,
			now: func() time.Time { return fixedNow },
		}
		cfg := &pb.Config{
			Mapping:          map[int64]int32{40: 800},
			AddedDate:        map[int32]int64{800: fixedNow.Add(-8 * 24 * time.Hour).Unix()},
			LastValidateDate: map[int64]int64{},
			PendingDownloads: map[int64]*pb.DownloadJob{},
		}

		if err := s.validate(context.Background(), cfg); err != nil {
			t.Fatalf("validate failed: %v", err)
		}
		if len(rc.queryCalls) != 1 || rc.queryCalls[0].GetReleaseId() != 800 {
			t.Errorf("expected 1 QueryRecords call for release 800, got %+v", rc.queryCalls)
		}
		if cfg.Mapping[40] != 800 {
			t.Errorf("expected valid eligible mapping to be preserved, got %v", cfg.Mapping)
		}
		if cfg.LastValidateDate[40] != fixedNow.Unix() {
			t.Errorf("LastValidateDate[40] = %d, want %d", cfg.LastValidateDate[40], fixedNow.Unix())
		}
	})
}
