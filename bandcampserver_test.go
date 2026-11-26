package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/brotherlogic/bandcamplib"
	bcpb "github.com/brotherlogic/bandcamplib/proto"
	pb "github.com/brotherlogic/bandcampserver/proto"
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

	if got := testutil.ToFloat64(cookieAge); got != 0 {
		t.Errorf("cookieAge = %v, want 0", got)
	}
	if got := testutil.ToFloat64(authValid); got != 0 {
		t.Errorf("authValid = %v, want 0", got)
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
			if gotCfg.Mapping == nil || gotCfg.IssueIds == nil || gotCfg.AddedDate == nil || gotCfg.LastValidateDate == nil || gotCfg.DownloadLinkFailures == nil {
				t.Errorf("expected all maps to be non-nil, got Mapping=%v IssueIds=%v AddedDate=%v LastValidateDate=%v DownloadLinkFailures=%v",
					gotCfg.Mapping != nil, gotCfg.IssueIds != nil, gotCfg.AddedDate != nil, gotCfg.LastValidateDate != nil, gotCfg.DownloadLinkFailures != nil)
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
