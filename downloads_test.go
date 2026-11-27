package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brotherlogic/bandcamplib"
	bcpb "github.com/brotherlogic/bandcamplib/proto"
	pb "github.com/brotherlogic/bandcampserver/proto"
	cdpb "github.com/brotherlogic/cdprocessor/proto"
	rcgd "github.com/brotherlogic/godiscogs/proto"
	pbgh "github.com/brotherlogic/githubcard/proto"
	rcpb "github.com/brotherlogic/recordcollection/proto"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type downloadsTestStore struct {
	mu      sync.Mutex
	saved   []*pb.Config
	saveErr error
}

func (s *downloadsTestStore) Load(ctx context.Context, key string) ([]byte, error) {
	return nil, status.Error(codes.InvalidArgument, "not found")
}

func (s *downloadsTestStore) Save(ctx context.Context, data []byte, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.saveErr != nil {
		return s.saveErr
	}
	cfg := &pb.Config{}
	if err := proto.Unmarshal(data, cfg); err != nil {
		return err
	}
	s.saved = append(s.saved, cfg)
	return nil
}

func (s *downloadsTestStore) saveCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.saved)
}

func (s *downloadsTestStore) lastSaved() *pb.Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.saved) == 0 {
		return nil
	}
	return s.saved[len(s.saved)-1]
}

type downloadsTestBandcampClient struct {
	retrieveFn   func(ctx context.Context) ([]*bcpb.Item, error)
	downloadFn   func(ctx context.Context, redownloadURL string, format bandcamplib.Format) (string, error)
	cookie       string
	retrieveCall int
	downloadCall int
}

func (c *downloadsTestBandcampClient) RetrieveItems(ctx context.Context) ([]*bcpb.Item, error) {
	c.retrieveCall++
	if c.retrieveFn != nil {
		return c.retrieveFn(ctx)
	}
	return nil, nil
}

func (c *downloadsTestBandcampClient) GetDownloadURLForRedownloadURL(ctx context.Context, redownloadURL string, format bandcamplib.Format) (string, error) {
	c.downloadCall++
	if c.downloadFn != nil {
		return c.downloadFn(ctx, redownloadURL, format)
	}
	return "", nil
}

func (c *downloadsTestBandcampClient) IdentityCookie() string {
	return c.cookie
}

type downloadsTestIssueCall struct {
	title string
	body  string
}

type downloadsTestIssueManager struct {
	mu          sync.Mutex
	nextNum     int32
	immediate   []downloadsTestIssueCall
	deleted     []int32
	immediateFn func(ctx context.Context, title, body string) (*pbgh.Issue, error)
	deleteFn    func(ctx context.Context, number int32) error
}

func (m *downloadsTestIssueManager) ImmediateIssue(ctx context.Context, title, body string, printImmediately, print bool, labels ...string) (*pbgh.Issue, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.immediate = append(m.immediate, downloadsTestIssueCall{title: title, body: body})
	if m.immediateFn != nil {
		return m.immediateFn(ctx, title, body)
	}
	if m.nextNum == 0 {
		m.nextNum = 100
	}
	m.nextNum++
	return &pbgh.Issue{Number: m.nextNum}, nil
}

func (m *downloadsTestIssueManager) DeleteIssue(ctx context.Context, number int32) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleted = append(m.deleted, number)
	if m.deleteFn != nil {
		return m.deleteFn(ctx, number)
	}
	return nil
}

func (m *downloadsTestIssueManager) RaiseIssue(title, body string, labels ...string) {}

type downloadsTestRCClient struct {
	queryCalls []*rcpb.QueryRecordsRequest
	getCalls   []*rcpb.GetRecordRequest
	addCalls   []*rcpb.AddRecordRequest
	queryFn    func(ctx context.Context, req *rcpb.QueryRecordsRequest) (*rcpb.QueryRecordsResponse, error)
	getFn      func(ctx context.Context, req *rcpb.GetRecordRequest) (*rcpb.GetRecordResponse, error)
	addFn      func(ctx context.Context, req *rcpb.AddRecordRequest) (*rcpb.AddRecordResponse, error)
}

func (r *downloadsTestRCClient) QueryRecords(ctx context.Context, req *rcpb.QueryRecordsRequest) (*rcpb.QueryRecordsResponse, error) {
	r.queryCalls = append(r.queryCalls, req)
	if r.queryFn != nil {
		return r.queryFn(ctx, req)
	}
	return &rcpb.QueryRecordsResponse{}, nil
}

func (r *downloadsTestRCClient) GetRecord(ctx context.Context, req *rcpb.GetRecordRequest) (*rcpb.GetRecordResponse, error) {
	r.getCalls = append(r.getCalls, req)
	if r.getFn != nil {
		return r.getFn(ctx, req)
	}
	return &rcpb.GetRecordResponse{}, nil
}

func (r *downloadsTestRCClient) AddRecord(ctx context.Context, req *rcpb.AddRecordRequest) (*rcpb.AddRecordResponse, error) {
	r.addCalls = append(r.addCalls, req)
	if r.addFn != nil {
		return r.addFn(ctx, req)
	}
	return &rcpb.AddRecordResponse{}, nil
}

func (r *downloadsTestRCClient) SearchReleases(ctx context.Context, req *rcpb.SearchReleasesRequest) (*rcpb.SearchReleasesResponse, error) {
	return &rcpb.SearchReleasesResponse{}, nil
}

type downloadsTestCDClient struct {
	downloadCalls []*cdpb.DownloadReleaseRequest
	statusCalls   []*cdpb.GetDownloadStatusRequest
	downloadFn    func(ctx context.Context, req *cdpb.DownloadReleaseRequest) (*cdpb.DownloadReleaseResponse, error)
	statusFn      func(ctx context.Context, req *cdpb.GetDownloadStatusRequest) (*cdpb.GetDownloadStatusResponse, error)
}

func (c *downloadsTestCDClient) DownloadRelease(ctx context.Context, req *cdpb.DownloadReleaseRequest) (*cdpb.DownloadReleaseResponse, error) {
	c.downloadCalls = append(c.downloadCalls, req)
	if c.downloadFn != nil {
		return c.downloadFn(ctx, req)
	}
	return &cdpb.DownloadReleaseResponse{State: cdpb.DownloadState_DOWNLOAD_QUEUED}, nil
}

func (c *downloadsTestCDClient) GetDownloadStatus(ctx context.Context, req *cdpb.GetDownloadStatusRequest) (*cdpb.GetDownloadStatusResponse, error) {
	c.statusCalls = append(c.statusCalls, req)
	if c.statusFn != nil {
		return c.statusFn(ctx, req)
	}
	return &cdpb.GetDownloadStatusResponse{State: cdpb.DownloadState_DOWNLOAD_IN_PROGRESS}, nil
}

type downloadsTestEnv struct {
	server   *Server
	store    *downloadsTestStore
	issues   *downloadsTestIssueManager
	rc       *downloadsTestRCClient
	cd       *downloadsTestCDClient
	bcClient *downloadsTestBandcampClient
	logs     []string
	now      time.Time
}

func newDownloadsTestEnv() *downloadsTestEnv {
	now := time.Unix(1700000000, 0)
	env := &downloadsTestEnv{
		store:    &downloadsTestStore{},
		issues:   &downloadsTestIssueManager{nextNum: 500},
		rc:       &downloadsTestRCClient{},
		cd:       &downloadsTestCDClient{},
		bcClient: &downloadsTestBandcampClient{},
		now:      now,
	}
	env.server = &Server{
		store:    env.store,
		issues:   env.issues,
		rc:       env.rc,
		cdClient: env.cd,
		now:      func() time.Time { return env.now },
		logFunc: func(ctx context.Context, message string) {
			env.logs = append(env.logs, message)
		},
		bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
			return env.bcClient, nil
		},
	}
	return env
}

func transitionCount(outcome string) float64 {
	return testutil.ToFloat64(downloadTransitionsTotal.WithLabelValues(outcome))
}

func TestSelectNextEligibleDownloadJob(t *testing.T) {
	now := time.Unix(1700000000, 0)

	t.Run("nil config or empty pending downloads", func(t *testing.T) {
		if id, job, ok := selectNextEligibleDownloadJob(nil, now); ok || id != 0 || job != nil {
			t.Fatalf("expected (0, nil, false), got (%d, %v, %v)", id, job, ok)
		}
		if id, job, ok := selectNextEligibleDownloadJob(&pb.Config{}, now); ok || id != 0 || job != nil {
			t.Fatalf("expected (0, nil, false), got (%d, %v, %v)", id, job, ok)
		}
	})

	t.Run("deterministic ascending bandcamp_id and skips nil/failed/cooldown", func(t *testing.T) {
		cfg := &pb.Config{
			PendingDownloads: map[int64]*pb.DownloadJob{
				10: nil,
				20: {DiscogsId: 200, State: pb.DownloadJobState_DOWNLOAD_JOB_STATE_FAILED, LastAttempt: 0},
				30: {DiscogsId: 300, State: pb.DownloadJobState_DOWNLOAD_JOB_STATE_PENDING, LastAttempt: now.Add(-59 * time.Second).Unix()},
				40: {DiscogsId: 400, State: pb.DownloadJobState_DOWNLOAD_JOB_STATE_SUBMITTED, LastAttempt: now.Add(-29 * time.Second).Unix()},
				50: {DiscogsId: 500, State: pb.DownloadJobState_DOWNLOAD_JOB_STATE_SUBMITTED, LastAttempt: now.Add(-30 * time.Second).Unix()},
				60: {DiscogsId: 600, State: pb.DownloadJobState_DOWNLOAD_JOB_STATE_PENDING, LastAttempt: 0},
			},
		}

		id, job, ok := selectNextEligibleDownloadJob(cfg, now)
		if !ok || id != 50 || job.GetDiscogsId() != 500 {
			t.Fatalf("expected bandcamp_id 50, got (%d, %+v, %v)", id, job, ok)
		}

		// When 50 is moved inside poll interval, 60 becomes eligible.
		cfg.PendingDownloads[50].LastAttempt = now.Unix()
		id, job, ok = selectNextEligibleDownloadJob(cfg, now)
		if !ok || id != 60 || job.GetDiscogsId() != 600 {
			t.Fatalf("expected bandcamp_id 60, got (%d, %+v, %v)", id, job, ok)
		}

		// At exact 1m retry cooldown boundary, 30 becomes eligible ahead of 60.
		cfg.PendingDownloads[30].LastAttempt = now.Add(-downloadRetryCooldown).Unix()
		id, job, ok = selectNextEligibleDownloadJob(cfg, now)
		if !ok || id != 30 || job.GetDiscogsId() != 300 {
			t.Fatalf("expected bandcamp_id 30, got (%d, %+v, %v)", id, job, ok)
		}
	})
}

func TestProcessNextDownloadJob_AtMostOneTransitionPerCall(t *testing.T) {
	env := newDownloadsTestEnv()
	cfg := &pb.Config{
		IdentityCookie: "cookie-one-per-tick",
		Items: []*bcpb.Item{
			{AlbumId: 100, BandName: "Artist A", AlbumTitle: "Album A", RedownloadUrl: "https://bc/a"},
			{AlbumId: 200, BandName: "Artist B", AlbumTitle: "Album B", RedownloadUrl: "https://bc/b"},
		},
		PendingDownloads: map[int64]*pb.DownloadJob{
			200: {DiscogsId: 2000, InstanceId: 22000, State: pb.DownloadJobState_DOWNLOAD_JOB_STATE_PENDING},
			100: {DiscogsId: 1000, InstanceId: 11000, State: pb.DownloadJobState_DOWNLOAD_JOB_STATE_PENDING},
		},
	}
	env.bcClient.downloadFn = func(ctx context.Context, redownloadURL string, format bandcamplib.Format) (string, error) {
		return "https://signed/" + redownloadURL, nil
	}

	if err := env.server.processNextDownloadJob(context.Background(), cfg); err != nil {
		t.Fatalf("processNextDownloadJob failed: %v", err)
	}

	if len(env.cd.downloadCalls) != 1 || env.cd.downloadCalls[0].GetReleaseId() != 1000 {
		t.Fatalf("expected only lowest bandcamp_id (release 1000) to dispatch, got %+v", env.cd.downloadCalls)
	}
	if cfg.PendingDownloads[100].GetState() != pb.DownloadJobState_DOWNLOAD_JOB_STATE_SUBMITTED {
		t.Fatalf("expected job 100 SUBMITTED, got %v", cfg.PendingDownloads[100].GetState())
	}
	if cfg.PendingDownloads[200].GetState() != pb.DownloadJobState_DOWNLOAD_JOB_STATE_PENDING {
		t.Fatalf("expected job 200 still PENDING, got %v", cfg.PendingDownloads[200].GetState())
	}
}

func TestProcessNextDownloadJob_HappyPathAndAlertClosure(t *testing.T) {
	env := newDownloadsTestEnv()
	cfg := &pb.Config{
		IdentityCookie:   "cookie-happy",
		AuthAlertIssueId: 777,
		Items: []*bcpb.Item{
			{AlbumId: 42, BandName: "Boards", AlbumTitle: "Camp", RedownloadUrl: "https://bc/redl/42"},
		},
		PendingDownloads: map[int64]*pb.DownloadJob{
			42: {
				DiscogsId:    9042,
				InstanceId:   88042,
				State:        pb.DownloadJobState_DOWNLOAD_JOB_STATE_PENDING,
				AlertIssueId: 321,
				LastError:    "previous failure",
			},
		},
	}
	env.bcClient.downloadFn = func(ctx context.Context, redownloadURL string, format bandcamplib.Format) (string, error) {
		if format != bandcamplib.FormatWAV {
			t.Fatalf("expected WAV format, got %v", format)
		}
		return "https://signed.bandcamp.com/wav/42", nil
	}
	env.cd.downloadFn = func(ctx context.Context, req *cdpb.DownloadReleaseRequest) (*cdpb.DownloadReleaseResponse, error) {
		if req.GetReleaseId() != 9042 || req.GetInstanceId() != 88042 || req.GetDownloadUrl() != "https://signed.bandcamp.com/wav/42" {
			t.Fatalf("unexpected DownloadReleaseRequest: %+v", req)
		}
		return &cdpb.DownloadReleaseResponse{State: cdpb.DownloadState_DOWNLOAD_QUEUED}, nil
	}

	subBefore := transitionCount("submitted")
	compBefore := transitionCount("completed")

	// Tick 1: PENDING -> SUBMITTED (clears auth alert, preserves failure alert until completion).
	if err := env.server.processNextDownloadJob(context.Background(), cfg); err != nil {
		t.Fatalf("tick 1 failed: %v", err)
	}
	if cfg.GetAuthAlertIssueId() != 0 {
		t.Errorf("expected auth alert cleared, got %d", cfg.GetAuthAlertIssueId())
	}
	job := cfg.PendingDownloads[42]
	if job.GetState() != pb.DownloadJobState_DOWNLOAD_JOB_STATE_SUBMITTED || job.GetLastError() != "" {
		t.Fatalf("unexpected job state after submit: %+v", job)
	}
	if job.GetAlertIssueId() != 321 {
		t.Errorf("expected prior AlertIssueId 321 preserved until completion, got %d", job.GetAlertIssueId())
	}
	if transitionCount("submitted")-subBefore != 1 {
		t.Errorf("expected submitted transition counter +1")
	}
	if testutil.ToFloat64(downloadJobs.WithLabelValues("submitted")) != 1 {
		t.Errorf("expected submitted gauge = 1")
	}

	// Tick 2 at +20s: inside 30s poll interval -> no RPC call.
	env.now = env.now.Add(20 * time.Second)
	if err := env.server.processNextDownloadJob(context.Background(), cfg); err != nil {
		t.Fatalf("tick 2 failed: %v", err)
	}
	if len(env.cd.statusCalls) != 0 {
		t.Fatalf("expected 0 status polls inside 30s interval, got %d", len(env.cd.statusCalls))
	}

	// Tick 3 at +30s: DOWNLOAD_IN_PROGRESS keeps SUBMITTED and advances LastAttempt.
	env.now = env.now.Add(10 * time.Second)
	env.cd.statusFn = func(ctx context.Context, req *cdpb.GetDownloadStatusRequest) (*cdpb.GetDownloadStatusResponse, error) {
		return &cdpb.GetDownloadStatusResponse{State: cdpb.DownloadState_DOWNLOAD_IN_PROGRESS}, nil
	}
	if err := env.server.processNextDownloadJob(context.Background(), cfg); err != nil {
		t.Fatalf("tick 3 failed: %v", err)
	}
	if len(env.cd.statusCalls) != 1 || cfg.PendingDownloads[42].GetState() != pb.DownloadJobState_DOWNLOAD_JOB_STATE_SUBMITTED {
		t.Fatalf("expected job to stay SUBMITTED after DOWNLOAD_IN_PROGRESS")
	}

	// Tick 4 at +30s: DOWNLOAD_COMPLETED deletes job and closes prior alert_issue_id=321.
	env.now = env.now.Add(30 * time.Second)
	env.cd.statusFn = func(ctx context.Context, req *cdpb.GetDownloadStatusRequest) (*cdpb.GetDownloadStatusResponse, error) {
		return &cdpb.GetDownloadStatusResponse{State: cdpb.DownloadState_DOWNLOAD_COMPLETED}, nil
	}
	if err := env.server.processNextDownloadJob(context.Background(), cfg); err != nil {
		t.Fatalf("tick 4 failed: %v", err)
	}
	if _, exists := cfg.PendingDownloads[42]; exists {
		t.Fatalf("expected job 42 deleted on completion")
	}
	if len(env.issues.deleted) != 2 || env.issues.deleted[0] != 777 || env.issues.deleted[1] != 321 {
		t.Fatalf("expected deleted issues [777, 321], got %v", env.issues.deleted)
	}
	if transitionCount("completed")-compBefore != 1 {
		t.Errorf("expected completed transition counter +1")
	}
	if testutil.ToFloat64(downloadJobs.WithLabelValues("submitted")) != 0 {
		t.Errorf("expected submitted gauge = 0")
	}
}

func TestProcessNextDownloadJob_IdempotentAlreadyExistsAndCompleted(t *testing.T) {
	t.Run("DownloadRelease DOWNLOAD_ALREADY_EXISTS tolerates closed alert", func(t *testing.T) {
		env := newDownloadsTestEnv()
		cfg := &pb.Config{
			IdentityCookie: "cookie-idem-1",
			Items: []*bcpb.Item{
				{AlbumId: 1, BandName: "A", AlbumTitle: "B", RedownloadUrl: "https://bc/1"},
			},
			PendingDownloads: map[int64]*pb.DownloadJob{
				1: {DiscogsId: 101, InstanceId: 201, State: pb.DownloadJobState_DOWNLOAD_JOB_STATE_PENDING, AlertIssueId: 888},
			},
		}
		env.bcClient.downloadFn = func(ctx context.Context, redownloadURL string, format bandcamplib.Format) (string, error) {
			return "https://wav/1", nil
		}
		env.cd.downloadFn = func(ctx context.Context, req *cdpb.DownloadReleaseRequest) (*cdpb.DownloadReleaseResponse, error) {
			return &cdpb.DownloadReleaseResponse{State: cdpb.DownloadState_DOWNLOAD_ALREADY_EXISTS}, nil
		}
		env.issues.deleteFn = func(ctx context.Context, number int32) error {
			return status.Error(codes.NotFound, "issue already closed")
		}

		before := transitionCount("already_exists")
		if err := env.server.processNextDownloadJob(context.Background(), cfg); err != nil {
			t.Fatalf("expected nil error when alert already closed, got %v", err)
		}
		if _, exists := cfg.PendingDownloads[1]; exists {
			t.Errorf("expected job deleted")
		}
		if transitionCount("already_exists")-before != 1 {
			t.Errorf("expected already_exists counter +1")
		}
	})

	t.Run("DownloadRelease DOWNLOAD_COMPLETED", func(t *testing.T) {
		env := newDownloadsTestEnv()
		cfg := &pb.Config{
			IdentityCookie: "cookie-idem-2",
			Items: []*bcpb.Item{
				{AlbumId: 2, BandName: "A", AlbumTitle: "B", RedownloadUrl: "https://bc/2"},
			},
			PendingDownloads: map[int64]*pb.DownloadJob{
				2: {DiscogsId: 102, InstanceId: 202, State: pb.DownloadJobState_DOWNLOAD_JOB_STATE_PENDING},
			},
		}
		env.bcClient.downloadFn = func(ctx context.Context, redownloadURL string, format bandcamplib.Format) (string, error) {
			return "https://wav/2", nil
		}
		env.cd.downloadFn = func(ctx context.Context, req *cdpb.DownloadReleaseRequest) (*cdpb.DownloadReleaseResponse, error) {
			return &cdpb.DownloadReleaseResponse{State: cdpb.DownloadState_DOWNLOAD_COMPLETED}, nil
		}

		before := transitionCount("completed")
		if err := env.server.processNextDownloadJob(context.Background(), cfg); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, exists := cfg.PendingDownloads[2]; exists {
			t.Errorf("expected job deleted")
		}
		if transitionCount("completed")-before != 1 {
			t.Errorf("expected completed counter +1")
		}
	})

	t.Run("GetDownloadStatus DOWNLOAD_ALREADY_EXISTS", func(t *testing.T) {
		env := newDownloadsTestEnv()
		cfg := &pb.Config{
			PendingDownloads: map[int64]*pb.DownloadJob{
				3: {DiscogsId: 103, InstanceId: 203, State: pb.DownloadJobState_DOWNLOAD_JOB_STATE_SUBMITTED, AlertIssueId: 999},
			},
		}
		env.cd.statusFn = func(ctx context.Context, req *cdpb.GetDownloadStatusRequest) (*cdpb.GetDownloadStatusResponse, error) {
			return &cdpb.GetDownloadStatusResponse{State: cdpb.DownloadState_DOWNLOAD_ALREADY_EXISTS}, nil
		}

		before := transitionCount("already_exists")
		if err := env.server.processNextDownloadJob(context.Background(), cfg); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, exists := cfg.PendingDownloads[3]; exists {
			t.Errorf("expected job deleted")
		}
		if len(env.issues.deleted) != 1 || env.issues.deleted[0] != 999 {
			t.Errorf("expected alert 999 deleted, got %v", env.issues.deleted)
		}
		if transitionCount("already_exists")-before != 1 {
			t.Errorf("expected already_exists counter +1")
		}
	})
}

func TestProcessNextDownloadJob_RetryableErrorsFreshURLAndExhaustion(t *testing.T) {
	t.Run("resolver, DownloadRelease retryable, and GetDownloadStatus NOT_FOUND re-resolve fresh URL then succeed", func(t *testing.T) {
		env := newDownloadsTestEnv()
		cfg := &pb.Config{
			IdentityCookie: "cookie-retry-ok",
			Items: []*bcpb.Item{
				{AlbumId: 55, BandName: "Artist", AlbumTitle: "Album", RedownloadUrl: "https://bc/55"},
			},
			PendingDownloads: map[int64]*pb.DownloadJob{
				55: {DiscogsId: 5500, InstanceId: 55001, State: pb.DownloadJobState_DOWNLOAD_JOB_STATE_PENDING},
			},
		}

		urlCounter := 0
		env.bcClient.downloadFn = func(ctx context.Context, redownloadURL string, format bandcamplib.Format) (string, error) {
			urlCounter++
			if urlCounter == 1 {
				return "", errors.New("temporary bandcamp 503")
			}
			return fmt.Sprintf("https://signed.example/wav?token=%d", urlCounter), nil
		}
		env.cd.downloadFn = func(ctx context.Context, req *cdpb.DownloadReleaseRequest) (*cdpb.DownloadReleaseResponse, error) {
			return &cdpb.DownloadReleaseResponse{State: cdpb.DownloadState_DOWNLOAD_QUEUED}, nil
		}
		env.cd.statusFn = func(ctx context.Context, req *cdpb.GetDownloadStatusRequest) (*cdpb.GetDownloadStatusResponse, error) {
			return &cdpb.GetDownloadStatusResponse{
				State:        cdpb.DownloadState_DOWNLOAD_NOT_FOUND,
				ErrorMessage: "cdprocessor restarted",
			}, nil
		}

		retryBefore := transitionCount("retryable_error")

		// Attempt 1: Bandcamp resolver fails -> attempts=1, stays PENDING.
		if err := env.server.processNextDownloadJob(context.Background(), cfg); err != nil {
			t.Fatalf("attempt 1 error: %v", err)
		}
		job := cfg.PendingDownloads[55]
		if job.GetAttempts() != 1 || job.GetState() != pb.DownloadJobState_DOWNLOAD_JOB_STATE_PENDING {
			t.Fatalf("expected attempts=1 PENDING, got %+v", job)
		}

		// Inside 1m cooldown -> skipped.
		env.now = env.now.Add(45 * time.Second)
		if err := env.server.processNextDownloadJob(context.Background(), cfg); err != nil {
			t.Fatalf("cooldown check error: %v", err)
		}
		if urlCounter != 1 {
			t.Fatalf("expected no retry inside 1m cooldown, urlCounter=%d", urlCounter)
		}

		// After 1m cooldown -> resolves URL #2 and dispatches to SUBMITTED.
		env.now = env.now.Add(15 * time.Second)
		if err := env.server.processNextDownloadJob(context.Background(), cfg); err != nil {
			t.Fatalf("dispatch retry error: %v", err)
		}
		if job.GetState() != pb.DownloadJobState_DOWNLOAD_JOB_STATE_SUBMITTED || env.cd.downloadCalls[0].GetDownloadUrl() != "https://signed.example/wav?token=2" {
			t.Fatalf("expected SUBMITTED with fresh token=2 URL, got state=%v calls=%+v", job.GetState(), env.cd.downloadCalls)
		}

		// Poll at +30s -> DOWNLOAD_NOT_FOUND -> attempts=2, returns to PENDING.
		env.now = env.now.Add(30 * time.Second)
		if err := env.server.processNextDownloadJob(context.Background(), cfg); err != nil {
			t.Fatalf("status poll error: %v", err)
		}
		if job.GetAttempts() != 2 || job.GetState() != pb.DownloadJobState_DOWNLOAD_JOB_STATE_PENDING {
			t.Fatalf("expected attempts=2 PENDING after NOT_FOUND, got %+v", job)
		}

		// After 1m cooldown -> resolves fresh URL #3 and completes!
		env.now = env.now.Add(1 * time.Minute)
		env.cd.downloadFn = func(ctx context.Context, req *cdpb.DownloadReleaseRequest) (*cdpb.DownloadReleaseResponse, error) {
			return &cdpb.DownloadReleaseResponse{State: cdpb.DownloadState_DOWNLOAD_COMPLETED}, nil
		}
		if err := env.server.processNextDownloadJob(context.Background(), cfg); err != nil {
			t.Fatalf("final retry error: %v", err)
		}
		if _, exists := cfg.PendingDownloads[55]; exists {
			t.Fatalf("expected job completed and removed")
		}
		if len(env.cd.downloadCalls) != 2 || env.cd.downloadCalls[1].GetDownloadUrl() != "https://signed.example/wav?token=3" {
			t.Fatalf("expected second dispatch to use fresh token=3 URL, got %+v", env.cd.downloadCalls)
		}
		if transitionCount("retryable_error")-retryBefore != 2 {
			t.Errorf("expected retryable_error delta 2, got %v", transitionCount("retryable_error")-retryBefore)
		}
	})

	t.Run("exhausts at 3 attempts across DownloadRelease RPC and GetDownloadStatus retryable errors", func(t *testing.T) {
		env := newDownloadsTestEnv()
		cfg := &pb.Config{
			IdentityCookie: "cookie-exhaust",
			Items: []*bcpb.Item{
				{AlbumId: 66, BandName: "Synth", AlbumTitle: "Wave", RedownloadUrl: "https://bc/66"},
			},
			PendingDownloads: map[int64]*pb.DownloadJob{
				66: {DiscogsId: 6600, InstanceId: 66001, State: pb.DownloadJobState_DOWNLOAD_JOB_STATE_PENDING},
			},
		}
		env.bcClient.downloadFn = func(ctx context.Context, redownloadURL string, format bandcamplib.Format) (string, error) {
			return "https://signed/66", nil
		}
		// Attempt 1: DownloadRelease RPC error
		env.cd.downloadFn = func(ctx context.Context, req *cdpb.DownloadReleaseRequest) (*cdpb.DownloadReleaseResponse, error) {
			return nil, status.Error(codes.Unavailable, "cdprocessor dialing failed")
		}
		if err := env.server.processNextDownloadJob(context.Background(), cfg); err != nil {
			t.Fatalf("attempt 1 error: %v", err)
		}
		// Attempt 2: DownloadRelease returns DOWNLOAD_FAILED_RETRYABLE
		env.now = env.now.Add(1 * time.Minute)
		env.cd.downloadFn = func(ctx context.Context, req *cdpb.DownloadReleaseRequest) (*cdpb.DownloadReleaseResponse, error) {
			return &cdpb.DownloadReleaseResponse{State: cdpb.DownloadState_DOWNLOAD_FAILED_RETRYABLE, Message: "http 410 expired"}, nil
		}
		if err := env.server.processNextDownloadJob(context.Background(), cfg); err != nil {
			t.Fatalf("attempt 2 error: %v", err)
		}
		if cfg.PendingDownloads[66].GetAttempts() != 2 || len(env.issues.immediate) != 0 {
			t.Fatalf("expected attempts=2 and 0 alerts before 3rd attempt")
		}

		// Attempt 3: DownloadRelease queues, then GetDownloadStatus returns DOWNLOAD_FAILED_RETRYABLE -> exhausts!
		env.now = env.now.Add(1 * time.Minute)
		env.cd.downloadFn = func(ctx context.Context, req *cdpb.DownloadReleaseRequest) (*cdpb.DownloadReleaseResponse, error) {
			return &cdpb.DownloadReleaseResponse{State: cdpb.DownloadState_DOWNLOAD_QUEUED}, nil
		}
		env.cd.statusFn = func(ctx context.Context, req *cdpb.GetDownloadStatusRequest) (*cdpb.GetDownloadStatusResponse, error) {
			return &cdpb.GetDownloadStatusResponse{
				State:        cdpb.DownloadState_DOWNLOAD_FAILED_RETRYABLE,
				ErrorMessage: "timeout downloading wav",
			}, nil
		}
		if err := env.server.processNextDownloadJob(context.Background(), cfg); err != nil {
			t.Fatalf("submit 3 error: %v", err)
		}

		exhaustBefore := transitionCount("failed_exhausted")
		env.now = env.now.Add(30 * time.Second)
		if err := env.server.processNextDownloadJob(context.Background(), cfg); err != nil {
			t.Fatalf("poll 3 error: %v", err)
		}

		job := cfg.PendingDownloads[66]
		if job.GetState() != pb.DownloadJobState_DOWNLOAD_JOB_STATE_FAILED || job.GetAttempts() != 3 {
			t.Fatalf("expected FAILED at 3 attempts, got %+v", job)
		}
		if job.GetAlertIssueId() == 0 || len(env.issues.immediate) != 1 {
			t.Fatalf("expected single alert issue created, got alert=%d calls=%d", job.GetAlertIssueId(), len(env.issues.immediate))
		}
		if transitionCount("failed_exhausted")-exhaustBefore != 1 {
			t.Errorf("expected failed_exhausted counter +1")
		}
		if testutil.ToFloat64(downloadJobs.WithLabelValues("failed")) != 1 {
			t.Errorf("expected failed gauge = 1")
		}
	})
}

func TestProcessNextDownloadJob_PermanentFailureAndAlertReuse(t *testing.T) {
	t.Run("GetDownloadStatus DOWNLOAD_FAILED_PERMANENT with quarantine path", func(t *testing.T) {
		env := newDownloadsTestEnv()
		secret := "super-secret-cookie-99"
		cfg := &pb.Config{
			IdentityCookie: secret,
			Items: []*bcpb.Item{
				{AlbumId: 707, BandName: "Aphex", AlbumTitle: "Drukqs", RedownloadUrl: "https://bc/707"},
			},
			PendingDownloads: map[int64]*pb.DownloadJob{
				707: {DiscogsId: 123456, InstanceId: 987654, State: pb.DownloadJobState_DOWNLOAD_JOB_STATE_SUBMITTED},
			},
		}
		env.cd.statusFn = func(ctx context.Context, req *cdpb.GetDownloadStatusRequest) (*cdpb.GetDownloadStatusResponse, error) {
			return &cdpb.GetDownloadStatusResponse{
				State:          cdpb.DownloadState_DOWNLOAD_FAILED_PERMANENT,
				ErrorMessage:   "track count mismatch (cookie=" + secret + ")",
				QuarantinePath: "/var/quarantine/123456",
			}, nil
		}

		permBefore := transitionCount("permanent_error")
		if err := env.server.processNextDownloadJob(context.Background(), cfg); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		job := cfg.PendingDownloads[707]
		if job.GetState() != pb.DownloadJobState_DOWNLOAD_JOB_STATE_FAILED {
			t.Fatalf("expected immediate FAILED, got %v", job.GetState())
		}
		if job.GetAttempts() != 0 {
			t.Errorf("expected 0 retry attempts burned on permanent failure, got %d", job.GetAttempts())
		}
		if strings.Contains(job.GetLastError(), secret) {
			t.Errorf("LastError leaked cookie secret: %q", job.GetLastError())
		}
		if len(env.issues.immediate) != 1 {
			t.Fatalf("expected 1 alert issue, got %d", len(env.issues.immediate))
		}
		issue := env.issues.immediate[0]
		if issue.title != "Bandcamp WAV download failed for Aphex - Drukqs [707]" {
			t.Errorf("unexpected alert title: %q", issue.title)
		}
		for _, want := range []string{
			"Aphex",
			"Drukqs",
			"707",
			"https://www.discogs.com/release/123456",
			"track count mismatch",
			"/var/quarantine/123456",
			"bandcampserver_cli download -bandcamp 707",
		} {
			if !strings.Contains(issue.body, want) {
				t.Errorf("alert body missing %q:\n%s", want, issue.body)
			}
		}
		if strings.Contains(issue.body, secret) {
			t.Errorf("alert body leaked cookie secret:\n%s", issue.body)
		}
		if transitionCount("permanent_error")-permBefore != 1 {
			t.Errorf("expected permanent_error counter +1")
		}
	})

	t.Run("DownloadRelease DOWNLOAD_FAILED_PERMANENT reuses existing alert_issue_id and handles nil item", func(t *testing.T) {
		env := newDownloadsTestEnv()
		cfg := &pb.Config{
			IdentityCookie: "cookie-perm-reuse",
			Items: []*bcpb.Item{
				{AlbumId: 808, BandName: "Artist", AlbumTitle: "Album", RedownloadUrl: "https://bc/808"},
			},
			PendingDownloads: map[int64]*pb.DownloadJob{
				808: {
					DiscogsId:    8808,
					InstanceId:   88081,
					State:        pb.DownloadJobState_DOWNLOAD_JOB_STATE_PENDING,
					AlertIssueId: 444,
				},
			},
		}
		env.bcClient.downloadFn = func(ctx context.Context, redownloadURL string, format bandcamplib.Format) (string, error) {
			return "https://signed/808", nil
		}
		env.cd.downloadFn = func(ctx context.Context, req *cdpb.DownloadReleaseRequest) (*cdpb.DownloadReleaseResponse, error) {
			return &cdpb.DownloadReleaseResponse{
				State:   cdpb.DownloadState_DOWNLOAD_FAILED_PERMANENT,
				Message: "corrupt archive",
			}, nil
		}

		if err := env.server.processNextDownloadJob(context.Background(), cfg); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.PendingDownloads[808].GetState() != pb.DownloadJobState_DOWNLOAD_JOB_STATE_FAILED {
			t.Fatalf("expected FAILED")
		}
		if cfg.PendingDownloads[808].GetAlertIssueId() != 444 {
			t.Errorf("expected AlertIssueId 444 preserved, got %d", cfg.PendingDownloads[808].GetAlertIssueId())
		}
		if len(env.issues.immediate) != 0 {
			t.Errorf("expected no duplicate alert issue opened, got %d", len(env.issues.immediate))
		}

		// Also verify ensureDownloadFailureAlert handles nil item gracefully.
		jobNilItem := &pb.DownloadJob{DiscogsId: 9999}
		if err := env.server.ensureDownloadFailureAlert(context.Background(), &pb.Config{}, 909, jobNilItem, "err", ""); err != nil {
			t.Fatalf("ensureDownloadFailureAlert nil item failed: %v", err)
		}
		if len(env.issues.immediate) != 1 || !strings.Contains(env.issues.immediate[0].title, "909") {
			t.Errorf("unexpected fallback alert title: %+v", env.issues.immediate)
		}
	})
}

func TestProcessNextDownloadJob_AuthPauseAndResume(t *testing.T) {
	for _, authErr := range []error{bandcamplib.ErrAuthenticationRequired, bandcamplib.ErrAuthenticationFailed} {
		t.Run(authErr.Error(), func(t *testing.T) {
			env := newDownloadsTestEnv()
			secret := "expired-cookie-secret"
			cookieVal := secret
			if errors.Is(authErr, bandcamplib.ErrAuthenticationRequired) {
				cookieVal = ""
			}
			cfg := &pb.Config{
				IdentityCookie: cookieVal,
				Items: []*bcpb.Item{
					{AlbumId: 91, BandName: "Auth", AlbumTitle: "Test", RedownloadUrl: "https://bc/91"},
				},
				PendingDownloads: map[int64]*pb.DownloadJob{
					91: {DiscogsId: 9100, InstanceId: 91001, State: pb.DownloadJobState_DOWNLOAD_JOB_STATE_PENDING},
				},
			}
			env.bcClient.downloadFn = func(ctx context.Context, redownloadURL string, format bandcamplib.Format) (string, error) {
				return "", fmt.Errorf("%w (cookie=%s)", authErr, secret)
			}

			authBefore := transitionCount("auth_paused")
			if err := env.server.processNextDownloadJob(context.Background(), cfg); err != nil {
				t.Fatalf("auth pause should return nil, got %v", err)
			}

			job := cfg.PendingDownloads[91]
			if job.GetState() != pb.DownloadJobState_DOWNLOAD_JOB_STATE_PENDING {
				t.Errorf("expected job to stay PENDING, got %v", job.GetState())
			}
			if job.GetAttempts() != 0 {
				t.Errorf("expected 0 attempts burned on auth pause, got %d", job.GetAttempts())
			}
			if cfg.GetAuthAlertIssueId() == 0 {
				t.Errorf("expected AuthAlertIssueId to be set")
			}
			if len(env.issues.immediate) != 1 {
				t.Errorf("expected 1 auth alert opened, got %d", len(env.issues.immediate))
			}
			if transitionCount("auth_paused")-authBefore != 1 {
				t.Errorf("expected auth_paused counter +1")
			}
			for _, msg := range env.logs {
				if cookieVal != "" && strings.Contains(msg, secret) {
					t.Errorf("log leaked cookie: %q", msg)
				}
			}

			// Simulate SetCookie updating cookie and clearing auth alert, then resume immediately.
			cfg.IdentityCookie = "fresh-cookie-ok"
			cfg.AuthAlertIssueId = 0
			env.bcClient.downloadFn = func(ctx context.Context, redownloadURL string, format bandcamplib.Format) (string, error) {
				return "https://signed/91", nil
			}
			if err := env.server.processNextDownloadJob(context.Background(), cfg); err != nil {
				t.Fatalf("resume after cookie update failed: %v", err)
			}
			if job.GetState() != pb.DownloadJobState_DOWNLOAD_JOB_STATE_SUBMITTED {
				t.Errorf("expected job to resume to SUBMITTED, got %v", job.GetState())
			}
		})
	}
}

func TestProcessNextDownloadJob_MissingCachedItemRefresh(t *testing.T) {
	t.Run("one-time refresh finds item and persists refreshed cookie", func(t *testing.T) {
		env := newDownloadsTestEnv()
		initialCookie := "old-cookie-abc"
		rotatedCookie := "rotated-cookie-xyz"
		cfg := &pb.Config{
			IdentityCookie: initialCookie,
			Items:          nil, // missing from cache
			PendingDownloads: map[int64]*pb.DownloadJob{
				303: {DiscogsId: 3030, InstanceId: 30301, State: pb.DownloadJobState_DOWNLOAD_JOB_STATE_PENDING},
			},
		}
		env.bcClient.cookie = rotatedCookie
		env.bcClient.retrieveFn = func(ctx context.Context) ([]*bcpb.Item, error) {
			return []*bcpb.Item{
				{AlbumId: 303, BandName: "Refreshed", AlbumTitle: "Album", RedownloadUrl: "https://bc/303"},
			}, nil
		}
		env.bcClient.downloadFn = func(ctx context.Context, redownloadURL string, format bandcamplib.Format) (string, error) {
			return "https://signed/303", nil
		}

		if err := env.server.processNextDownloadJob(context.Background(), cfg); err != nil {
			t.Fatalf("processNextDownloadJob failed: %v", err)
		}
		if env.bcClient.retrieveCall != 1 {
			t.Errorf("expected 1 RetrieveItems call, got %d", env.bcClient.retrieveCall)
		}
		if cfg.GetIdentityCookie() != rotatedCookie {
			t.Errorf("expected rotated cookie persisted, got %q", cfg.GetIdentityCookie())
		}
		if len(cfg.GetItems()) != 1 || cfg.GetItems()[0].GetAlbumId() != 303 {
			t.Errorf("expected refreshed items persisted, got %+v", cfg.GetItems())
		}
		if cfg.PendingDownloads[303].GetState() != pb.DownloadJobState_DOWNLOAD_JOB_STATE_SUBMITTED {
			t.Errorf("expected SUBMITTED after refresh, got %v", cfg.PendingDownloads[303].GetState())
		}
	})

	t.Run("item still missing after refresh counts as retryable failure", func(t *testing.T) {
		env := newDownloadsTestEnv()
		cfg := &pb.Config{
			IdentityCookie: "cookie-missing-still",
			PendingDownloads: map[int64]*pb.DownloadJob{
				304: {DiscogsId: 3040, InstanceId: 30401, State: pb.DownloadJobState_DOWNLOAD_JOB_STATE_PENDING},
			},
		}
		env.bcClient.retrieveFn = func(ctx context.Context) ([]*bcpb.Item, error) {
			return []*bcpb.Item{}, nil
		}

		if err := env.server.processNextDownloadJob(context.Background(), cfg); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if env.bcClient.retrieveCall != 1 {
			t.Errorf("expected 1 RetrieveItems call, got %d", env.bcClient.retrieveCall)
		}
		if cfg.PendingDownloads[304].GetAttempts() != 1 || cfg.PendingDownloads[304].GetState() != pb.DownloadJobState_DOWNLOAD_JOB_STATE_PENDING {
			t.Errorf("expected attempts=1 PENDING, got %+v", cfg.PendingDownloads[304])
		}
	})

	t.Run("auth failure during missing-item refresh pauses without burning attempts", func(t *testing.T) {
		env := newDownloadsTestEnv()
		cfg := &pb.Config{
			IdentityCookie: "cookie-refresh-auth-fail",
			PendingDownloads: map[int64]*pb.DownloadJob{
				305: {DiscogsId: 3050, InstanceId: 30501, State: pb.DownloadJobState_DOWNLOAD_JOB_STATE_PENDING},
			},
		}
		env.bcClient.retrieveFn = func(ctx context.Context) ([]*bcpb.Item, error) {
			return nil, bandcamplib.ErrAuthenticationFailed
		}

		if err := env.server.processNextDownloadJob(context.Background(), cfg); err != nil {
			t.Fatalf("expected nil on auth failure, got %v", err)
		}
		if cfg.PendingDownloads[305].GetAttempts() != 0 || cfg.GetAuthAlertIssueId() == 0 {
			t.Errorf("expected attempts=0 and auth alert set, got job=%+v alert=%d", cfg.PendingDownloads[305], cfg.GetAuthAlertIssueId())
		}
	})
}

func TestEnsureDownloadInstanceID(t *testing.T) {
	t.Run("resolves existing digital record via QueryRecords and GetRecord", func(t *testing.T) {
		env := newDownloadsTestEnv()
		cfg := &pb.Config{
			IdentityCookie: "cookie-iid-existing",
			AddedDate:      map[int32]int64{},
			Items: []*bcpb.Item{
				{AlbumId: 401, BandName: "B", AlbumTitle: "A", RedownloadUrl: "https://bc/401"},
			},
			PendingDownloads: map[int64]*pb.DownloadJob{
				401: {DiscogsId: 4010, InstanceId: 0, State: pb.DownloadJobState_DOWNLOAD_JOB_STATE_PENDING},
			},
		}
		env.rc.queryFn = func(ctx context.Context, req *rcpb.QueryRecordsRequest) (*rcpb.QueryRecordsResponse, error) {
			return &rcpb.QueryRecordsResponse{InstanceIds: []int64{111, 222}}, nil
		}
		env.rc.getFn = func(ctx context.Context, req *rcpb.GetRecordRequest) (*rcpb.GetRecordResponse, error) {
			if req.GetInstanceId() == 111 {
				return &rcpb.GetRecordResponse{Record: &rcpb.Record{Metadata: &rcpb.ReleaseMetadata{GoalFolder: 999}}}, nil
			}
			return &rcpb.GetRecordResponse{Record: &rcpb.Record{Metadata: &rcpb.ReleaseMetadata{GoalFolder: digitalGoalFolder}}}, nil
		}
		env.bcClient.downloadFn = func(ctx context.Context, redownloadURL string, format bandcamplib.Format) (string, error) {
			return "https://signed/401", nil
		}

		if err := env.server.processNextDownloadJob(context.Background(), cfg); err != nil {
			t.Fatalf("processNextDownloadJob failed: %v", err)
		}
		if len(env.rc.addCalls) != 0 {
			t.Errorf("expected no AddRecord call when digital record exists, got %d", len(env.rc.addCalls))
		}
		if cfg.PendingDownloads[401].GetInstanceId() != 222 {
			t.Errorf("expected InstanceId 222, got %d", cfg.PendingDownloads[401].GetInstanceId())
		}
		if cfg.AddedDate[4010] != env.now.Unix() {
			t.Errorf("expected AddedDate[4010]=%d, got %d", env.now.Unix(), cfg.AddedDate[4010])
		}
		if len(env.cd.downloadCalls) != 1 || env.cd.downloadCalls[0].GetInstanceId() != 222 {
			t.Errorf("expected cdprocessor called with instance_id 222, got %+v", env.cd.downloadCalls)
		}
	})

	t.Run("adds digital record via AddRecord when missing", func(t *testing.T) {
		env := newDownloadsTestEnv()
		cfg := &pb.Config{
			IdentityCookie: "cookie-iid-add",
			AddedDate:      map[int32]int64{4020: 12345},
			Items: []*bcpb.Item{
				{AlbumId: 402, BandName: "B", AlbumTitle: "A", RedownloadUrl: "https://bc/402"},
			},
			PendingDownloads: map[int64]*pb.DownloadJob{
				402: {DiscogsId: 4020, InstanceId: 0, State: pb.DownloadJobState_DOWNLOAD_JOB_STATE_PENDING},
			},
		}
		env.rc.queryFn = func(ctx context.Context, req *rcpb.QueryRecordsRequest) (*rcpb.QueryRecordsResponse, error) {
			return &rcpb.QueryRecordsResponse{}, nil
		}
		env.rc.addFn = func(ctx context.Context, req *rcpb.AddRecordRequest) (*rcpb.AddRecordResponse, error) {
			meta := req.GetToAdd().GetMetadata()
			if req.GetToAdd().GetRelease().GetId() != 4020 ||
				meta.GetGoalFolder() != digitalGoalFolder ||
				meta.GetCost() != 1 ||
				meta.GetFiledUnder() != rcpb.ReleaseMetadata_FILE_DIGITAL ||
				meta.GetPurchaseBudget() != fmt.Sprintf("float%v", env.now.Year()) ||
				meta.GetDateArrived() != env.now.Unix() {
				t.Fatalf("unexpected AddRecord payload: %+v", req)
			}
			return &rcpb.AddRecordResponse{
				Added: &rcpb.Record{Release: &rcgd.Release{InstanceId: 76543}},
			}, nil
		}
		env.bcClient.downloadFn = func(ctx context.Context, redownloadURL string, format bandcamplib.Format) (string, error) {
			return "https://signed/402", nil
		}

		if err := env.server.processNextDownloadJob(context.Background(), cfg); err != nil {
			t.Fatalf("processNextDownloadJob failed: %v", err)
		}
		if cfg.PendingDownloads[402].GetInstanceId() != 76543 {
			t.Errorf("expected InstanceId 76543, got %d", cfg.PendingDownloads[402].GetInstanceId())
		}
		if cfg.AddedDate[4020] != 12345 {
			t.Errorf("expected existing non-zero AddedDate preserved (12345), got %d", cfg.AddedDate[4020])
		}
		if len(env.cd.downloadCalls) != 1 || env.cd.downloadCalls[0].GetInstanceId() != 76543 {
			t.Errorf("expected cdprocessor called with instance_id 76543, got %+v", env.cd.downloadCalls)
		}
	})

	t.Run("never dispatches to cdprocessor when AddRecord returns instance_id <= 0", func(t *testing.T) {
		env := newDownloadsTestEnv()
		cfg := &pb.Config{
			IdentityCookie: "cookie-iid-zero",
			Items: []*bcpb.Item{
				{AlbumId: 403, BandName: "B", AlbumTitle: "A", RedownloadUrl: "https://bc/403"},
			},
			PendingDownloads: map[int64]*pb.DownloadJob{
				403: {DiscogsId: 4030, InstanceId: 0, State: pb.DownloadJobState_DOWNLOAD_JOB_STATE_PENDING},
			},
		}
		env.rc.addFn = func(ctx context.Context, req *rcpb.AddRecordRequest) (*rcpb.AddRecordResponse, error) {
			return &rcpb.AddRecordResponse{
				Added: &rcpb.Record{Release: &rcgd.Release{InstanceId: 0}},
			}, nil
		}

		if err := env.server.processNextDownloadJob(context.Background(), cfg); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(env.cd.downloadCalls) != 0 {
			t.Fatalf("expected 0 cdprocessor calls when instance_id == 0, got %d", len(env.cd.downloadCalls))
		}
		if cfg.PendingDownloads[403].GetAttempts() != 1 || cfg.PendingDownloads[403].GetState() != pb.DownloadJobState_DOWNLOAD_JOB_STATE_PENDING {
			t.Errorf("expected retryable failure recorded, got %+v", cfg.PendingDownloads[403])
		}
	})
}

func TestProcessNextDownloadJob_CookieRedactionAcrossAllOutputs(t *testing.T) {
	env := newDownloadsTestEnv()
	initialCookie := "initial-secret-cookie-111"
	rotatedCookie := "rotated-secret-cookie-222"
	cfg := &pb.Config{
		IdentityCookie: initialCookie,
		Items: []*bcpb.Item{
			{AlbumId: 505, BandName: "Band", AlbumTitle: "Album", RedownloadUrl: "https://bc/505"},
		},
		PendingDownloads: map[int64]*pb.DownloadJob{
			505: {DiscogsId: 5050, InstanceId: 50501, State: pb.DownloadJobState_DOWNLOAD_JOB_STATE_PENDING, Attempts: 2},
		},
	}

	env.bcClient.cookie = rotatedCookie
	env.bcClient.downloadFn = func(ctx context.Context, redownloadURL string, format bandcamplib.Format) (string, error) {
		return "https://signed/505", nil
	}
	env.cd.downloadFn = func(ctx context.Context, req *cdpb.DownloadReleaseRequest) (*cdpb.DownloadReleaseResponse, error) {
		return nil, fmt.Errorf("upstream leaked initial=%s rotated=%s", initialCookie, rotatedCookie)
	}
	env.issues.immediateFn = func(ctx context.Context, title, body string) (*pbgh.Issue, error) {
		return &pbgh.Issue{Number: 650}, nil
	}

	if err := env.server.processNextDownloadJob(context.Background(), cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	job := cfg.PendingDownloads[505]
	for _, secret := range []string{initialCookie, rotatedCookie} {
		if strings.Contains(job.GetLastError(), secret) {
			t.Errorf("job.LastError leaked %q: %q", secret, job.GetLastError())
		}
		for _, msg := range env.logs {
			if strings.Contains(msg, secret) {
				t.Errorf("log leaked %q: %q", secret, msg)
			}
		}
		for _, iss := range env.issues.immediate {
			if strings.Contains(iss.title, secret) || strings.Contains(iss.body, secret) {
				t.Errorf("issue leaked %q: title=%q body=%q", secret, iss.title, iss.body)
			}
		}
	}
	if !strings.Contains(job.GetLastError(), "[REDACTED]") {
		t.Errorf("expected [REDACTED] in job.LastError, got %q", job.GetLastError())
	}

	// Also verify saveConfig error returned to caller is redacted.
	env2 := newDownloadsTestEnv()
	cfg2 := &pb.Config{
		IdentityCookie: initialCookie,
		Items: []*bcpb.Item{
			{AlbumId: 506, BandName: "Band", AlbumTitle: "Album", RedownloadUrl: "https://bc/506"},
		},
		PendingDownloads: map[int64]*pb.DownloadJob{
			506: {DiscogsId: 5060, InstanceId: 50601, State: pb.DownloadJobState_DOWNLOAD_JOB_STATE_PENDING},
		},
	}
	env2.bcClient.downloadFn = func(ctx context.Context, redownloadURL string, format bandcamplib.Format) (string, error) {
		return "https://signed/506", nil
	}
	env2.store.saveErr = fmt.Errorf("dstore write failed with %s", initialCookie)
	err := env2.server.processNextDownloadJob(context.Background(), cfg2)
	if err == nil {
		t.Fatal("expected saveConfig error")
	}
	if strings.Contains(err.Error(), initialCookie) {
		t.Errorf("returned error leaked cookie: %q", err.Error())
	}
}

func TestRetryFailedJob_OpensNewAlertIssueOnSubsequentFailure(t *testing.T) {
	const (
		bcID      int64 = 2796019519
		discogsID int32 = 31692413
		oldAlert  int32 = 5132
	)

	cases := []struct {
		name    string
		trigger func(ctx context.Context, s *Server) error
	}{
		{
			name: "via AddMapping",
			trigger: func(ctx context.Context, s *Server) error {
				_, err := s.AddMapping(ctx, &pb.AddMappingRequest{
					BandcampId: bcID,
					DiscogsId:  discogsID,
				})
				return err
			},
		},
		{
			name: "via TriggerDownload",
			trigger: func(ctx context.Context, s *Server) error {
				_, err := s.TriggerDownload(ctx, &pb.TriggerDownloadRequest{
					BandcampId: bcID,
				})
				return err
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			now := time.Unix(1700020000, 0)
			store := newAPITestConfigStore(t, &pb.Config{
				IdentityCookie: "cookie-retry-alert",
				Items: []*bcpb.Item{
					{
						AlbumId:       bcID,
						BandName:      "Artist",
						AlbumTitle:    "Album",
						RedownloadUrl: "https://bc/2796019519",
					},
				},
				Mapping: map[int64]int32{bcID: discogsID},
				PendingDownloads: map[int64]*pb.DownloadJob{
					bcID: {
						DiscogsId:    discogsID,
						InstanceId:   99001,
						State:        pb.DownloadJobState_DOWNLOAD_JOB_STATE_FAILED,
						Attempts:     3,
						LastAttempt:  now.Add(-5 * time.Minute).Unix(),
						LastError:    "previous failure",
						AlertIssueId: oldAlert,
					},
				},
			})

			issues := &downloadsTestIssueManager{
				nextNum: 5200,
				deleteFn: func(ctx context.Context, number int32) error {
					return status.Error(codes.NotFound, "issue already closed")
				},
			}
			rc := &downloadsTestRCClient{
				queryFn: func(ctx context.Context, req *rcpb.QueryRecordsRequest) (*rcpb.QueryRecordsResponse, error) {
					return &rcpb.QueryRecordsResponse{InstanceIds: []int64{99001}}, nil
				},
				getFn: func(ctx context.Context, req *rcpb.GetRecordRequest) (*rcpb.GetRecordResponse, error) {
					return &rcpb.GetRecordResponse{
						Record: &rcpb.Record{
							Metadata: &rcpb.ReleaseMetadata{GoalFolder: digitalGoalFolder},
						},
					}, nil
				},
			}
			bcClient := &downloadsTestBandcampClient{
				downloadFn: func(ctx context.Context, redownloadURL string, format bandcamplib.Format) (string, error) {
					return "https://signed/2796019519", nil
				},
			}
			cdClient := &downloadsTestCDClient{
				downloadFn: func(ctx context.Context, req *cdpb.DownloadReleaseRequest) (*cdpb.DownloadReleaseResponse, error) {
					return &cdpb.DownloadReleaseResponse{
						State:   cdpb.DownloadState_DOWNLOAD_FAILED_PERMANENT,
						Message: "new failure after retry",
					}, nil
				},
			}

			s := &Server{
				store:    store,
				issues:   issues,
				rc:       rc,
				cdClient: cdClient,
				now:      func() time.Time { return now },
				logFunc:  func(ctx context.Context, message string) {},
				bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
					return bcClient, nil
				},
			}

			if err := tc.trigger(ctx, s); err != nil {
				t.Fatalf("trigger failed: %v", err)
			}

			if len(issues.deleted) != 1 || issues.deleted[0] != oldAlert {
				t.Fatalf("DeleteIssue calls = %v, want [%d]", issues.deleted, oldAlert)
			}

			persisted := store.currentConfig(t)
			pendingJob := persisted.GetPendingDownloads()[bcID]
			if pendingJob == nil || pendingJob.GetState() != pb.DownloadJobState_DOWNLOAD_JOB_STATE_PENDING {
				t.Fatalf("expected PENDING job after retry, got %+v", pendingJob)
			}
			if pendingJob.GetAlertIssueId() != 0 {
				t.Fatalf("expected AlertIssueId reset to 0 after retry, got %d", pendingJob.GetAlertIssueId())
			}

			cfg, err := s.loadConfig(ctx)
			if err != nil {
				t.Fatalf("loadConfig failed: %v", err)
			}
			if err := s.processNextDownloadJob(ctx, cfg); err != nil {
				t.Fatalf("processNextDownloadJob failed: %v", err)
			}

			failedJob := cfg.GetPendingDownloads()[bcID]
			if failedJob == nil || failedJob.GetState() != pb.DownloadJobState_DOWNLOAD_JOB_STATE_FAILED {
				t.Fatalf("expected FAILED job after subsequent failure, got %+v", failedJob)
			}
			if len(issues.immediate) != 1 {
				t.Fatalf("expected 1 new alert issue opened, got %d", len(issues.immediate))
			}
			if failedJob.GetAlertIssueId() != 5201 {
				t.Errorf("AlertIssueId = %d, want 5201", failedJob.GetAlertIssueId())
			}
			if !strings.Contains(issues.immediate[0].body, "new failure after retry") {
				t.Errorf("new alert issue body missing failure reason:\n%s", issues.immediate[0].body)
			}
		})
	}
}
