package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	bcpb "github.com/brotherlogic/bandcamplib/proto"
	pb "github.com/brotherlogic/bandcampserver/proto"
	cdpb "github.com/brotherlogic/cdprocessor/proto"
	pbgh "github.com/brotherlogic/githubcard/proto"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type triggerTestCDProcessorClient struct {
	mu          sync.Mutex
	downloadCnt int
	statusCnt   int
}

func (c *triggerTestCDProcessorClient) DownloadRelease(ctx context.Context, req *cdpb.DownloadReleaseRequest) (*cdpb.DownloadReleaseResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.downloadCnt++
	return nil, errors.New("unexpected cdprocessor DownloadRelease call")
}

func (c *triggerTestCDProcessorClient) GetDownloadStatus(ctx context.Context, req *cdpb.GetDownloadStatusRequest) (*cdpb.GetDownloadStatusResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.statusCnt++
	return nil, errors.New("unexpected cdprocessor GetDownloadStatus call")
}

func (c *triggerTestCDProcessorClient) calls() (downloads, statuses int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.downloadCnt, c.statusCnt
}

type triggerTestIssueManager struct {
	mu          sync.Mutex
	deleteCalls []int32
	deleteErrFn func(number int32) error
}

func (m *triggerTestIssueManager) ImmediateIssue(ctx context.Context, title, body string, printImmediately, print bool, labels ...string) (*pbgh.Issue, error) {
	return nil, errors.New("unexpected ImmediateIssue call")
}

func (m *triggerTestIssueManager) DeleteIssue(ctx context.Context, number int32) error {
	m.mu.Lock()
	m.deleteCalls = append(m.deleteCalls, number)
	fn := m.deleteErrFn
	m.mu.Unlock()
	if fn != nil {
		return fn(number)
	}
	return nil
}

func (m *triggerTestIssueManager) RaiseIssue(title, body string, labels ...string) {}

func (m *triggerTestIssueManager) deletes() []int32 {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]int32, len(m.deleteCalls))
	copy(cp, m.deleteCalls)
	return cp
}

func assertDownloadJobGauges(t *testing.T, pending, submitted, failed float64) {
	t.Helper()
	if got := testutil.ToFloat64(downloadJobs.WithLabelValues("pending")); got != pending {
		t.Errorf("downloadJobs[pending] = %v, want %v", got, pending)
	}
	if got := testutil.ToFloat64(downloadJobs.WithLabelValues("submitted")); got != submitted {
		t.Errorf("downloadJobs[submitted] = %v, want %v", got, submitted)
	}
	if got := testutil.ToFloat64(downloadJobs.WithLabelValues("failed")); got != failed {
		t.Errorf("downloadJobs[failed] = %v, want %v", got, failed)
	}
}

func TestAddMapping_EnqueuesPendingJobFast(t *testing.T) {
	fixedNow := time.Unix(1700010000, 0)
	store := newAPITestConfigStore(t, &pb.Config{
		Mapping:              map[int64]int32{},
		IssueIds:             map[int64]int32{101: 77},
		DownloadLinkFailures: map[int64]int32{101: 2},
	})
	issues := &triggerTestIssueManager{}
	cdClient := &triggerTestCDProcessorClient{}
	logger := &apiTestLogger{}

	var bcCalls int
	s := &Server{
		store:  store,
		issues: issues,
		cdClient: cdClient,
		bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
			bcCalls++
			return nil, errors.New("unexpected bandcamp client creation")
		},
		now:     func() time.Time { return fixedNow },
		logFunc: logger.log,
	}

	_, err := s.AddMapping(context.Background(), &pb.AddMappingRequest{
		BandcampId: 101,
		DiscogsId:  5001,
	})
	if err != nil {
		t.Fatalf("AddMapping failed: %v", err)
	}

	if bcCalls != 0 {
		t.Errorf("bcFactory calls = %d, want 0", bcCalls)
	}
	dlCalls, stCalls := cdClient.calls()
	if dlCalls != 0 || stCalls != 0 {
		t.Errorf("cdprocessor calls = (%d, %d), want (0, 0)", dlCalls, stCalls)
	}

	_, saves := store.counts()
	if saves != 1 {
		t.Errorf("store saves = %d, want 1", saves)
	}

	deletes := issues.deletes()
	if len(deletes) != 1 || deletes[0] != 77 {
		t.Errorf("DeleteIssue calls = %v, want [77]", deletes)
	}

	saved := store.currentConfig(t)
	if saved.GetMapping()[101] != 5001 {
		t.Errorf("Mapping[101] = %d, want 5001", saved.GetMapping()[101])
	}
	if _, exists := saved.GetDownloadLinkFailures()[101]; exists {
		t.Errorf("expected DownloadLinkFailures[101] deleted, got %v", saved.GetDownloadLinkFailures())
	}
	if saved.GetLastProcess() != fixedNow.Unix() {
		t.Errorf("LastProcess = %d, want %d", saved.GetLastProcess(), fixedNow.Unix())
	}

	wantJob := &pb.DownloadJob{
		DiscogsId: 5001,
		State:     pb.DownloadJobState_DOWNLOAD_JOB_STATE_PENDING,
	}
	gotJob := saved.GetPendingDownloads()[101]
	if !proto.Equal(gotJob, wantJob) {
		t.Errorf("PendingDownloads[101] = %+v, want %+v", gotJob, wantJob)
	}

	assertDownloadJobGauges(t, 1, 0, 0)
}

func TestAddMapping_RemapResetsJobAndPreservesAlertIssueId(t *testing.T) {
	states := []pb.DownloadJobState{
		pb.DownloadJobState_DOWNLOAD_JOB_STATE_PENDING,
		pb.DownloadJobState_DOWNLOAD_JOB_STATE_SUBMITTED,
		pb.DownloadJobState_DOWNLOAD_JOB_STATE_FAILED,
	}

	for _, priorState := range states {
		t.Run(priorState.String(), func(t *testing.T) {
			fixedNow := time.Unix(1700011000, 0)
			store := newAPITestConfigStore(t, &pb.Config{
				Mapping: map[int64]int32{101: 5001},
				PendingDownloads: map[int64]*pb.DownloadJob{
					101: {
						DiscogsId:    5001,
						InstanceId:   9001,
						State:        priorState,
						Attempts:     3,
						LastAttempt:  1700000100,
						AlertIssueId: 88,
						LastError:    "boom",
					},
				},
			})
			issues := &triggerTestIssueManager{}
			cdClient := &triggerTestCDProcessorClient{}
			logger := &apiTestLogger{}

			s := &Server{
				store:    store,
				issues:   issues,
				cdClient: cdClient,
				now:      func() time.Time { return fixedNow },
				logFunc:  logger.log,
			}

			_, err := s.AddMapping(context.Background(), &pb.AddMappingRequest{
				BandcampId: 101,
				DiscogsId:  6002,
			})
			if err != nil {
				t.Fatalf("AddMapping failed: %v", err)
			}

			if deletes := issues.deletes(); len(deletes) != 0 {
				t.Errorf("expected no DeleteIssue calls on re-map, got %v", deletes)
			}

			saved := store.currentConfig(t)
			wantJob := &pb.DownloadJob{
				DiscogsId:    6002,
				InstanceId:   0,
				State:        pb.DownloadJobState_DOWNLOAD_JOB_STATE_PENDING,
				Attempts:     0,
				LastAttempt:  0,
				AlertIssueId: 88,
				LastError:    "",
			}
			gotJob := saved.GetPendingDownloads()[101]
			if !proto.Equal(gotJob, wantJob) {
				t.Errorf("PendingDownloads[101] = %+v, want %+v", gotJob, wantJob)
			}
			assertDownloadJobGauges(t, 1, 0, 0)
		})
	}
}

func TestAddMapping_SkipSentinelDeletesJobAndClosesAlert(t *testing.T) {
	const secretCookie = "secret-cookie-abc-123"

	sentinels := []int32{0, -1}
	priorStates := []pb.DownloadJobState{
		pb.DownloadJobState_DOWNLOAD_JOB_STATE_PENDING,
		pb.DownloadJobState_DOWNLOAD_JOB_STATE_SUBMITTED,
		pb.DownloadJobState_DOWNLOAD_JOB_STATE_FAILED,
	}

	for _, sentinel := range sentinels {
		for _, priorState := range priorStates {
			name := fmt.Sprintf("sentinel=%d/state=%s", sentinel, priorState.String())
			t.Run(name, func(t *testing.T) {
				store := newAPITestConfigStore(t, &pb.Config{
					IdentityCookie: secretCookie,
					Mapping:        map[int64]int32{101: 5001},
					PendingDownloads: map[int64]*pb.DownloadJob{
						101: {
							DiscogsId:    5001,
							InstanceId:   9001,
							State:        priorState,
							Attempts:     2,
							AlertIssueId: 88,
							LastError:    "err",
						},
					},
				})
				issues := &triggerTestIssueManager{}
				s := &Server{
					store:   store,
					issues:  issues,
					now:     func() time.Time { return time.Unix(1700012000, 0) },
					logFunc: (&apiTestLogger{}).log,
				}

				_, err := s.AddMapping(context.Background(), &pb.AddMappingRequest{
					BandcampId: 101,
					DiscogsId:  sentinel,
				})
				if err != nil {
					t.Fatalf("AddMapping failed: %v", err)
				}

				deletes := issues.deletes()
				if len(deletes) != 1 || deletes[0] != 88 {
					t.Errorf("DeleteIssue calls = %v, want [88]", deletes)
				}

				saved := store.currentConfig(t)
				if saved.GetMapping()[101] != sentinel {
					t.Errorf("Mapping[101] = %d, want %d", saved.GetMapping()[101], sentinel)
				}
				if _, exists := saved.GetPendingDownloads()[101]; exists {
					t.Errorf("expected PendingDownloads[101] deleted, got %+v", saved.GetPendingDownloads()[101])
				}
				assertDownloadJobGauges(t, 0, 0, 0)
			})
		}
	}

	alreadyClosedCases := []struct {
		name string
		err  error
	}{
		{name: "grpc NotFound", err: status.Error(codes.NotFound, "issue not found")},
		{name: "already closed message", err: errors.New("issue already closed")},
	}
	for _, tc := range alreadyClosedCases {
		t.Run("tolerates_"+tc.name, func(t *testing.T) {
			store := newAPITestConfigStore(t, &pb.Config{
				IdentityCookie: secretCookie,
				Mapping:        map[int64]int32{101: 5001},
				PendingDownloads: map[int64]*pb.DownloadJob{
					101: {
						DiscogsId:    5001,
						State:        pb.DownloadJobState_DOWNLOAD_JOB_STATE_FAILED,
						AlertIssueId: 88,
					},
				},
			})
			issues := &triggerTestIssueManager{
				deleteErrFn: func(number int32) error { return tc.err },
			}
			s := &Server{
				store:   store,
				issues:  issues,
				now:     func() time.Time { return time.Unix(1700012000, 0) },
				logFunc: (&apiTestLogger{}).log,
			}

			_, err := s.AddMapping(context.Background(), &pb.AddMappingRequest{
				BandcampId: 101,
				DiscogsId:  -1,
			})
			if err != nil {
				t.Fatalf("AddMapping should tolerate %q, got error: %v", tc.name, err)
			}
			saved := store.currentConfig(t)
			if _, exists := saved.GetPendingDownloads()[101]; exists {
				t.Errorf("expected PendingDownloads[101] deleted")
			}
			assertDownloadJobGauges(t, 0, 0, 0)
		})
	}

	t.Run("non-not-found alert delete failure returns sanitized error without saving", func(t *testing.T) {
		store := newAPITestConfigStore(t, &pb.Config{
			IdentityCookie: secretCookie,
			Mapping:        map[int64]int32{101: 5001},
			PendingDownloads: map[int64]*pb.DownloadJob{
				101: {
					DiscogsId:    5001,
					State:        pb.DownloadJobState_DOWNLOAD_JOB_STATE_FAILED,
					AlertIssueId: 88,
				},
			},
		})
		issues := &triggerTestIssueManager{
			deleteErrFn: func(number int32) error {
				return fmt.Errorf("github unavailable for cookie=%s", secretCookie)
			},
		}
		s := &Server{
			store:   store,
			issues:  issues,
			now:     func() time.Time { return time.Unix(1700012000, 0) },
			logFunc: (&apiTestLogger{}).log,
		}

		_, err := s.AddMapping(context.Background(), &pb.AddMappingRequest{
			BandcampId: 101,
			DiscogsId:  0,
		})
		if err == nil {
			t.Fatal("expected error when alert DeleteIssue fails")
		}
		if strings.Contains(err.Error(), secretCookie) {
			t.Errorf("error leaked identity cookie: %q", err.Error())
		}
		if !strings.Contains(err.Error(), "[REDACTED]") {
			t.Errorf("expected sanitized error with [REDACTED], got %q", err.Error())
		}
		_, saves := store.counts()
		if saves != 0 {
			t.Errorf("store saves = %d, want 0", saves)
		}
	})

	t.Run("no pending job or zero alert issue succeeds without alert delete", func(t *testing.T) {
		store := newAPITestConfigStore(t, &pb.Config{
			IdentityCookie: secretCookie,
			Mapping:        map[int64]int32{101: 5001, 102: 5002},
			PendingDownloads: map[int64]*pb.DownloadJob{
				102: {
					DiscogsId:    5002,
					State:        pb.DownloadJobState_DOWNLOAD_JOB_STATE_PENDING,
					AlertIssueId: 0,
				},
			},
		})
		issues := &triggerTestIssueManager{}
		s := &Server{
			store:   store,
			issues:  issues,
			now:     func() time.Time { return time.Unix(1700012000, 0) },
			logFunc: (&apiTestLogger{}).log,
		}

		if _, err := s.AddMapping(context.Background(), &pb.AddMappingRequest{BandcampId: 101, DiscogsId: -1}); err != nil {
			t.Fatalf("AddMapping(101, -1) failed: %v", err)
		}
		if _, err := s.AddMapping(context.Background(), &pb.AddMappingRequest{BandcampId: 102, DiscogsId: 0}); err != nil {
			t.Fatalf("AddMapping(102, 0) failed: %v", err)
		}
		if deletes := issues.deletes(); len(deletes) != 0 {
			t.Errorf("expected 0 DeleteIssue calls, got %v", deletes)
		}
		saved := store.currentConfig(t)
		if _, exists := saved.GetPendingDownloads()[102]; exists {
			t.Errorf("expected PendingDownloads[102] deleted")
		}
	})

	t.Run("sanitizes missing-mapping DeleteIssue and saveConfig errors", func(t *testing.T) {
		store := newAPITestConfigStore(t, &pb.Config{
			IdentityCookie: secretCookie,
			IssueIds:       map[int64]int32{101: 77},
		})
		issues := &triggerTestIssueManager{
			deleteErrFn: func(number int32) error {
				return fmt.Errorf("delete failed cookie=%s", secretCookie)
			},
		}
		s := &Server{
			store:   store,
			issues:  issues,
			now:     func() time.Time { return time.Unix(1700012000, 0) },
			logFunc: (&apiTestLogger{}).log,
		}
		_, err := s.AddMapping(context.Background(), &pb.AddMappingRequest{BandcampId: 101, DiscogsId: 5001})
		if err == nil || strings.Contains(err.Error(), secretCookie) || !strings.Contains(err.Error(), "[REDACTED]") {
			t.Errorf("expected sanitized DeleteIssue error, got %v", err)
		}

		store.saveErr = fmt.Errorf("dstore write failed cookie=%s", secretCookie)
		s.issues = &triggerTestIssueManager{}
		_, err = s.AddMapping(context.Background(), &pb.AddMappingRequest{BandcampId: 102, DiscogsId: 5002})
		if err == nil || strings.Contains(err.Error(), secretCookie) || !strings.Contains(err.Error(), "[REDACTED]") {
			t.Errorf("expected sanitized saveConfig error, got %v", err)
		}
	})
}

func TestTriggerDownload_HappyPaths(t *testing.T) {
	t.Run("historical mapped item by bandcamp_id", func(t *testing.T) {
		store := newAPITestConfigStore(t, &pb.Config{
			Mapping: map[int64]int32{101: 5001},
		})
		s := &Server{
			store:   store,
			now:     func() time.Time { return time.Unix(1700013000, 0) },
			logFunc: (&apiTestLogger{}).log,
		}

		resp, err := s.TriggerDownload(context.Background(), &pb.TriggerDownloadRequest{BandcampId: 101})
		if err != nil {
			t.Fatalf("TriggerDownload failed: %v", err)
		}
		if resp.GetBandcampId() != 101 || resp.GetDiscogsId() != 5001 {
			t.Errorf("resp IDs = (%d, %d), want (101, 5001)", resp.GetBandcampId(), resp.GetDiscogsId())
		}
		if resp.GetState() != pb.DownloadJobState_DOWNLOAD_JOB_STATE_PENDING {
			t.Errorf("resp.State = %v, want PENDING", resp.GetState())
		}
		if resp.GetAlreadyActive() {
			t.Errorf("resp.AlreadyActive = true, want false")
		}
		if strings.TrimSpace(resp.GetMessage()) == "" {
			t.Errorf("expected non-empty resp.Message")
		}

		_, saves := store.counts()
		if saves != 1 {
			t.Errorf("store saves = %d, want 1", saves)
		}

		saved := store.currentConfig(t)
		wantJob := &pb.DownloadJob{
			DiscogsId: 5001,
			State:     pb.DownloadJobState_DOWNLOAD_JOB_STATE_PENDING,
		}
		if !proto.Equal(saved.GetPendingDownloads()[101], wantJob) {
			t.Errorf("PendingDownloads[101] = %+v, want %+v", saved.GetPendingDownloads()[101], wantJob)
		}
		assertDownloadJobGauges(t, 1, 0, 0)
	})

	t.Run("historical mapped item by discogs_id", func(t *testing.T) {
		store := newAPITestConfigStore(t, &pb.Config{
			Mapping: map[int64]int32{202: 6002},
		})
		s := &Server{
			store:   store,
			now:     func() time.Time { return time.Unix(1700013000, 0) },
			logFunc: (&apiTestLogger{}).log,
		}

		resp, err := s.TriggerDownload(context.Background(), &pb.TriggerDownloadRequest{DiscogsId: 6002})
		if err != nil {
			t.Fatalf("TriggerDownload failed: %v", err)
		}
		if resp.GetBandcampId() != 202 || resp.GetDiscogsId() != 6002 {
			t.Errorf("resp IDs = (%d, %d), want (202, 6002)", resp.GetBandcampId(), resp.GetDiscogsId())
		}
		if resp.GetState() != pb.DownloadJobState_DOWNLOAD_JOB_STATE_PENDING || resp.GetAlreadyActive() || strings.TrimSpace(resp.GetMessage()) == "" {
			t.Errorf("unexpected resp: %+v", resp)
		}
		saved := store.currentConfig(t)
		wantJob := &pb.DownloadJob{
			DiscogsId: 6002,
			State:     pb.DownloadJobState_DOWNLOAD_JOB_STATE_PENDING,
		}
		if !proto.Equal(saved.GetPendingDownloads()[202], wantJob) {
			t.Errorf("PendingDownloads[202] = %+v, want %+v", saved.GetPendingDownloads()[202], wantJob)
		}
		assertDownloadJobGauges(t, 1, 0, 0)
	})

	t.Run("failed job matching discogs_id preserves AlertIssueId and InstanceId", func(t *testing.T) {
		for _, byDiscogs := range []bool{false, true} {
			store := newAPITestConfigStore(t, &pb.Config{
				Mapping: map[int64]int32{101: 5001},
				PendingDownloads: map[int64]*pb.DownloadJob{
					101: {
						DiscogsId:    5001,
						InstanceId:   777888,
						State:        pb.DownloadJobState_DOWNLOAD_JOB_STATE_FAILED,
						Attempts:     3,
						LastAttempt:  1700001234,
						AlertIssueId: 91,
						LastError:    "track mismatch",
					},
				},
			})
			s := &Server{
				store:   store,
				now:     func() time.Time { return time.Unix(1700013000, 0) },
				logFunc: (&apiTestLogger{}).log,
			}

			req := &pb.TriggerDownloadRequest{BandcampId: 101}
			if byDiscogs {
				req = &pb.TriggerDownloadRequest{DiscogsId: 5001}
			}

			resp, err := s.TriggerDownload(context.Background(), req)
			if err != nil {
				t.Fatalf("TriggerDownload(byDiscogs=%v) failed: %v", byDiscogs, err)
			}
			if resp.GetState() != pb.DownloadJobState_DOWNLOAD_JOB_STATE_PENDING || resp.GetAlreadyActive() {
				t.Errorf("unexpected resp: %+v", resp)
			}

			saved := store.currentConfig(t)
			wantJob := &pb.DownloadJob{
				DiscogsId:    5001,
				InstanceId:   777888,
				State:        pb.DownloadJobState_DOWNLOAD_JOB_STATE_PENDING,
				Attempts:     0,
				LastAttempt:  0,
				AlertIssueId: 91,
				LastError:    "",
			}
			if !proto.Equal(saved.GetPendingDownloads()[101], wantJob) {
				t.Errorf("PendingDownloads[101] = %+v, want %+v", saved.GetPendingDownloads()[101], wantJob)
			}
			assertDownloadJobGauges(t, 1, 0, 0)
		}
	})

	t.Run("failed job mismatched discogs_id preserves AlertIssueId and clears InstanceId", func(t *testing.T) {
		store := newAPITestConfigStore(t, &pb.Config{
			Mapping: map[int64]int32{101: 6002},
			PendingDownloads: map[int64]*pb.DownloadJob{
				101: {
					DiscogsId:    5001,
					InstanceId:   777888,
					State:        pb.DownloadJobState_DOWNLOAD_JOB_STATE_FAILED,
					Attempts:     3,
					LastAttempt:  1700001234,
					AlertIssueId: 91,
					LastError:    "old release error",
				},
			},
		})
		s := &Server{
			store:   store,
			now:     func() time.Time { return time.Unix(1700013000, 0) },
			logFunc: (&apiTestLogger{}).log,
		}

		resp, err := s.TriggerDownload(context.Background(), &pb.TriggerDownloadRequest{BandcampId: 101})
		if err != nil {
			t.Fatalf("TriggerDownload failed: %v", err)
		}
		if resp.GetDiscogsId() != 6002 || resp.GetAlreadyActive() {
			t.Errorf("unexpected resp: %+v", resp)
		}

		saved := store.currentConfig(t)
		wantJob := &pb.DownloadJob{
			DiscogsId:    6002,
			InstanceId:   0,
			State:        pb.DownloadJobState_DOWNLOAD_JOB_STATE_PENDING,
			Attempts:     0,
			LastAttempt:  0,
			AlertIssueId: 91,
			LastError:    "",
		}
		if !proto.Equal(saved.GetPendingDownloads()[101], wantJob) {
			t.Errorf("PendingDownloads[101] = %+v, want %+v", saved.GetPendingDownloads()[101], wantJob)
		}
		assertDownloadJobGauges(t, 1, 0, 0)
	})
}

func TestTriggerDownload_AlreadyActiveNoOp(t *testing.T) {
	states := []pb.DownloadJobState{
		pb.DownloadJobState_DOWNLOAD_JOB_STATE_PENDING,
		pb.DownloadJobState_DOWNLOAD_JOB_STATE_SUBMITTED,
	}

	for _, state := range states {
		for _, byDiscogs := range []bool{false, true} {
			name := fmt.Sprintf("%s/byDiscogs=%v", state.String(), byDiscogs)
			t.Run(name, func(t *testing.T) {
				initialJob := &pb.DownloadJob{
					DiscogsId:    5001,
					InstanceId:   444555,
					State:        state,
					Attempts:     2,
					LastAttempt:  1700000555,
					AlertIssueId: 66,
					LastError:    "transient",
				}
				store := newAPITestConfigStore(t, &pb.Config{
					Mapping: map[int64]int32{101: 5001},
					PendingDownloads: map[int64]*pb.DownloadJob{
						101: proto.Clone(initialJob).(*pb.DownloadJob),
					},
				})
				s := &Server{
					store:   store,
					now:     func() time.Time { return time.Unix(1700014000, 0) },
					logFunc: (&apiTestLogger{}).log,
				}

				req := &pb.TriggerDownloadRequest{BandcampId: 101}
				if byDiscogs {
					req = &pb.TriggerDownloadRequest{DiscogsId: 5001}
				}

				resp, err := s.TriggerDownload(context.Background(), req)
				if err != nil {
					t.Fatalf("TriggerDownload failed: %v", err)
				}
				if resp.GetBandcampId() != 101 || resp.GetDiscogsId() != 5001 {
					t.Errorf("resp IDs = (%d, %d), want (101, 5001)", resp.GetBandcampId(), resp.GetDiscogsId())
				}
				if resp.GetState() != state {
					t.Errorf("resp.State = %v, want %v", resp.GetState(), state)
				}
				if !resp.GetAlreadyActive() {
					t.Errorf("resp.AlreadyActive = false, want true")
				}
				if strings.TrimSpace(resp.GetMessage()) == "" {
					t.Errorf("expected non-empty resp.Message")
				}

				_, saves := store.counts()
				if saves != 0 {
					t.Errorf("store saves = %d, want 0", saves)
				}
				saved := store.currentConfig(t)
				if !proto.Equal(saved.GetPendingDownloads()[101], initialJob) {
					t.Errorf("job was mutated: got %+v, want %+v", saved.GetPendingDownloads()[101], initialJob)
				}
			})
		}
	}
}

func TestTriggerDownload_ValidationAndErrors(t *testing.T) {
	baseConfig := func() *pb.Config {
		return &pb.Config{
			Mapping: map[int64]int32{
				101: 5001,
				102: 5001, // ambiguous discogs_id 5001
				103: 0,    // skipped sentinel 0
				104: -1,   // skipped sentinel -1
			},
		}
	}

	tests := []struct {
		name            string
		req             *pb.TriggerDownloadRequest
		wantCode        codes.Code
		wantMsgContains string
	}{
		{
			name:     "both IDs set",
			req:      &pb.TriggerDownloadRequest{BandcampId: 101, DiscogsId: 5001},
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "neither ID set",
			req:      &pb.TriggerDownloadRequest{},
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "negative bandcamp_id",
			req:      &pb.TriggerDownloadRequest{BandcampId: -5},
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "negative discogs_id",
			req:      &pb.TriggerDownloadRequest{DiscogsId: -5},
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "unmapped bandcamp_id",
			req:      &pb.TriggerDownloadRequest{BandcampId: 999},
			wantCode: codes.NotFound,
		},
		{
			name:     "skipped bandcamp_id zero",
			req:      &pb.TriggerDownloadRequest{BandcampId: 103},
			wantCode: codes.FailedPrecondition,
		},
		{
			name:     "skipped bandcamp_id negative",
			req:      &pb.TriggerDownloadRequest{BandcampId: 104},
			wantCode: codes.FailedPrecondition,
		},
		{
			name:     "unmapped discogs_id",
			req:      &pb.TriggerDownloadRequest{DiscogsId: 9999},
			wantCode: codes.NotFound,
		},
		{
			name:            "ambiguous discogs_id",
			req:             &pb.TriggerDownloadRequest{DiscogsId: 5001},
			wantCode:        codes.FailedPrecondition,
			wantMsgContains: "-bandcamp",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := baseConfig()
			store := newAPITestConfigStore(t, cfg)
			s := &Server{
				store:   store,
				now:     func() time.Time { return time.Unix(1700015000, 0) },
				logFunc: (&apiTestLogger{}).log,
			}

			resp, err := s.TriggerDownload(context.Background(), tc.req)
			if err == nil {
				t.Fatalf("expected error (%v), got response %+v", tc.wantCode, resp)
			}
			if got := status.Code(err); got != tc.wantCode {
				t.Errorf("status code = %v, want %v (err=%v)", got, tc.wantCode, err)
			}
			if tc.wantMsgContains != "" && !strings.Contains(err.Error(), tc.wantMsgContains) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantMsgContains)
			}
			_, saves := store.counts()
			if saves != 0 {
				t.Errorf("store saves = %d, want 0", saves)
			}
			saved := store.currentConfig(t)
			if !proto.Equal(saved, cfg) {
				t.Errorf("config was mutated on error: got %+v, want %+v", saved, cfg)
			}
		})
	}
}

func TestTriggerDownload_SanitizesSaveError(t *testing.T) {
	const secretCookie = "secret-cookie-trigger-999"
	store := newAPITestConfigStore(t, &pb.Config{
		IdentityCookie: secretCookie,
		Mapping:        map[int64]int32{101: 5001},
	})
	store.saveErr = fmt.Errorf("dstore unavailable with cookie=%s", secretCookie)

	s := &Server{
		store:   store,
		now:     func() time.Time { return time.Unix(1700016000, 0) },
		logFunc: (&apiTestLogger{}).log,
	}

	_, err := s.TriggerDownload(context.Background(), &pb.TriggerDownloadRequest{BandcampId: 101})
	if err == nil {
		t.Fatal("expected error from TriggerDownload")
	}
	if strings.Contains(err.Error(), secretCookie) {
		t.Errorf("error leaked identity cookie: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "[REDACTED]") {
		t.Errorf("expected sanitized error with [REDACTED], got %q", err.Error())
	}
}
