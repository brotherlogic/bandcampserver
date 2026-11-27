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
	pbgh "github.com/brotherlogic/githubcard/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type apiTestConfigStore struct {
	mu        sync.Mutex
	data      []byte
	hasData   bool
	loadCalls int
	saveCalls int
	saveErr   error
}

func newAPITestConfigStore(t *testing.T, cfg *pb.Config) *apiTestConfigStore {
	t.Helper()
	if cfg == nil {
		return &apiTestConfigStore{}
	}
	raw, err := proto.Marshal(cfg)
	if err != nil {
		t.Fatalf("proto.Marshal failed: %v", err)
	}
	return &apiTestConfigStore{
		data:    raw,
		hasData: true,
	}
}

func (s *apiTestConfigStore) Load(ctx context.Context, key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadCalls++
	if !s.hasData {
		return nil, status.Error(codes.InvalidArgument, "not found")
	}
	cp := make([]byte, len(s.data))
	copy(cp, s.data)
	return cp, nil
}

func (s *apiTestConfigStore) Save(ctx context.Context, data []byte, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saveCalls++
	if s.saveErr != nil {
		return s.saveErr
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	s.data = cp
	s.hasData = true
	return nil
}

func (s *apiTestConfigStore) currentConfig(t *testing.T) *pb.Config {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.hasData {
		return &pb.Config{}
	}
	cfg := &pb.Config{}
	if err := proto.Unmarshal(s.data, cfg); err != nil {
		t.Fatalf("proto.Unmarshal failed: %v", err)
	}
	return cfg
}

func (s *apiTestConfigStore) counts() (loads, saves int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadCalls, s.saveCalls
}

type apiTestBandcampClient struct {
	identityCookie  string
	onRefresh       func(string)
	retrieveItemsFn func(ctx context.Context, onRefresh func(string)) ([]*bcpb.Item, error)
}

func (c *apiTestBandcampClient) RetrieveItems(ctx context.Context) ([]*bcpb.Item, error) {
	if c.retrieveItemsFn == nil {
		return nil, nil
	}
	return c.retrieveItemsFn(ctx, c.onRefresh)
}

func (c *apiTestBandcampClient) GetDownloadURLForRedownloadURL(ctx context.Context, redownloadURL string, format bandcamplib.Format) (string, error) {
	return "", nil
}

func (c *apiTestBandcampClient) IdentityCookie() string {
	return c.identityCookie
}

type apiTestIssueCall struct {
	title            string
	body             string
	printImmediately bool
	print            bool
	labels           []string
}

type apiTestIssueManager struct {
	mu             sync.Mutex
	immediateCalls []apiTestIssueCall
	raiseCalls     []apiTestIssueCall
	nextIssueID    int32
	immediateErr   error
	deleteCalls    []int32
	deleteErr      error
}

func (m *apiTestIssueManager) ImmediateIssue(ctx context.Context, title, body string, printImmediately, print bool, labels ...string) (*pbgh.Issue, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.immediateCalls = append(m.immediateCalls, apiTestIssueCall{
		title:            title,
		body:             body,
		printImmediately: printImmediately,
		print:            print,
		labels:           labels,
	})
	if m.immediateErr != nil {
		return nil, m.immediateErr
	}
	if m.nextIssueID == 0 {
		m.nextIssueID = 88
	}
	id := m.nextIssueID
	m.nextIssueID++
	return &pbgh.Issue{Number: id}, nil
}

func (m *apiTestIssueManager) DeleteIssue(ctx context.Context, number int32) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteCalls = append(m.deleteCalls, number)
	return m.deleteErr
}

func (m *apiTestIssueManager) RaiseIssue(title, body string, labels ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.raiseCalls = append(m.raiseCalls, apiTestIssueCall{
		title:  title,
		body:   body,
		labels: labels,
	})
}

func (m *apiTestIssueManager) immediateSnapshot() []apiTestIssueCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]apiTestIssueCall, len(m.immediateCalls))
	copy(cp, m.immediateCalls)
	return cp
}

func (m *apiTestIssueManager) raiseSnapshot() []apiTestIssueCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]apiTestIssueCall, len(m.raiseCalls))
	copy(cp, m.raiseCalls)
	return cp
}

func (m *apiTestIssueManager) deleteSnapshot() []int32 {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]int32, len(m.deleteCalls))
	copy(cp, m.deleteCalls)
	return cp
}

type apiTestLogger struct {
	mu   sync.Mutex
	logs []string
}

func (l *apiTestLogger) log(ctx context.Context, msg string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.logs = append(l.logs, msg)
}

func (l *apiTestLogger) entries() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	cp := make([]string, len(l.logs))
	copy(cp, l.logs)
	return cp
}

func TestSetCookie_InvalidInput(t *testing.T) {
	tests := []struct {
		name   string
		cookie string
	}{
		{name: "empty", cookie: ""},
		{name: "whitespace", cookie: "   \t\n "},
		{name: "bare identity prefix", cookie: "identity="},
		{name: "uppercase bare identity prefix with whitespace", cookie: "  IDENTITY=  \n"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newAPITestConfigStore(t, &pb.Config{
				IdentityCookie:    "existing-cookie",
				LastCookieRefresh: 1234,
			})
			logger := &apiTestLogger{}
			s := &Server{
				store:   store,
				issues:  &apiTestIssueManager{},
				now:     func() time.Time { return time.Unix(1700000000, 0) },
				logFunc: logger.log,
			}

			_, err := s.SetCookie(context.Background(), &pb.SetCookieRequest{Cookie: tc.cookie})
			if err == nil {
				t.Fatal("expected InvalidArgument error, got nil")
			}
			if status.Code(err) != codes.InvalidArgument {
				t.Fatalf("status.Code(err) = %v, want %v (err=%v)", status.Code(err), codes.InvalidArgument, err)
			}

			loads, saves := store.counts()
			if loads != 0 || saves != 0 {
				t.Errorf("expected 0 loads and 0 saves on invalid input, got loads=%d saves=%d", loads, saves)
			}
			cfg := store.currentConfig(t)
			if cfg.GetIdentityCookie() != "existing-cookie" {
				t.Errorf("stored IdentityCookie = %q, want %q", cfg.GetIdentityCookie(), "existing-cookie")
			}
		})
	}
}

func TestSetCookie_ValidationFailurePreservesPreviousCookieAndRedactsSecret(t *testing.T) {
	const oldCookie = "old-valid-cookie"
	const badCookie = "bad-new-secret-cookie"

	store := newAPITestConfigStore(t, &pb.Config{
		Token:             "tok-1",
		IdentityCookie:    oldCookie,
		LastCookieRefresh: 1000,
		Items: []*bcpb.Item{
			{AlbumId: 10, AlbumTitle: "Old"},
		},
	})
	logger := &apiTestLogger{}
	issues := &apiTestIssueManager{}

	s := &Server{
		store:  store,
		issues: issues,
		bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
			if cfg.GetIdentityCookie() != badCookie {
				t.Errorf("factory received cookie %q, want %q", cfg.GetIdentityCookie(), badCookie)
			}
			return &apiTestBandcampClient{
				identityCookie: badCookie,
				onRefresh:      onRefresh,
				retrieveItemsFn: func(ctx context.Context, cb func(string)) ([]*bcpb.Item, error) {
					return nil, fmt.Errorf("%w: cookie=%s old=%s", bandcamplib.ErrAuthenticationFailed, badCookie, oldCookie)
				},
			}, nil
		},
		now:     func() time.Time { return time.Unix(1700001000, 0) },
		logFunc: logger.log,
	}

	_, err := s.SetCookie(context.Background(), &pb.SetCookieRequest{Cookie: "  identity=" + badCookie + "  "})
	if err == nil {
		t.Fatal("expected error from SetCookie validation failure, got nil")
	}
	if !errors.Is(err, bandcamplib.ErrAuthenticationFailed) {
		t.Errorf("errors.Is(err, ErrAuthenticationFailed) = false (err=%v)", err)
	}
	if strings.Contains(err.Error(), badCookie) || strings.Contains(err.Error(), oldCookie) {
		t.Errorf("SetCookie error leaked cookie secret: %q", err.Error())
	}

	_, saves := store.counts()
	if saves != 0 {
		t.Errorf("expected 0 store saves on validation failure, got %d", saves)
	}
	cfg := store.currentConfig(t)
	if cfg.GetIdentityCookie() != oldCookie {
		t.Errorf("IdentityCookie = %q, want %q", cfg.GetIdentityCookie(), oldCookie)
	}
	if cfg.GetLastCookieRefresh() != 1000 {
		t.Errorf("LastCookieRefresh = %d, want 1000", cfg.GetLastCookieRefresh())
	}
	if len(cfg.GetItems()) != 1 || cfg.GetItems()[0].GetAlbumId() != 10 {
		t.Errorf("existing Items modified unexpectedly: %+v", cfg.GetItems())
	}
}

func TestSetCookie_SuccessUpdatesConfigClearsAlertAndPrefersRotatedCookie(t *testing.T) {
	fixedNow := time.Unix(1700002000, 0)

	t.Run("normalizes cookie, updates items and timestamp, and clears open alert", func(t *testing.T) {
		store := newAPITestConfigStore(t, &pb.Config{
			Token:            "tok-abc",
			IdentityCookie:   "old-cookie",
			AuthAlertIssueId: 55,
		})
		issues := &apiTestIssueManager{}
		logger := &apiTestLogger{}

		var factoryCfg *bcpb.Config
		s := &Server{
			store:  store,
			issues: issues,
			bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
				factoryCfg = cfg
				return &apiTestBandcampClient{
					identityCookie: cfg.GetIdentityCookie(),
					onRefresh:      onRefresh,
					retrieveItemsFn: func(ctx context.Context, cb func(string)) ([]*bcpb.Item, error) {
						return []*bcpb.Item{
							{
								AlbumId:       101,
								BandName:      "Artist",
								AlbumTitle:    "Record",
								RedownloadUrl: "https://bandcamp.com/download?id=101",
							},
						}, nil
					},
				}, nil
			},
			now:     func() time.Time { return fixedNow },
			logFunc: logger.log,
		}

		_, err := s.SetCookie(context.Background(), &pb.SetCookieRequest{Cookie: "IDENTITY=new-cookie-val"})
		if err != nil {
			t.Fatalf("SetCookie failed: %v", err)
		}

		if factoryCfg == nil {
			t.Fatal("bandcamp factory was not called")
		}
		if factoryCfg.GetUser() != 873256 {
			t.Errorf("factory User = %d, want 873256", factoryCfg.GetUser())
		}
		if factoryCfg.GetToken() != "tok-abc" {
			t.Errorf("factory Token = %q, want %q", factoryCfg.GetToken(), "tok-abc")
		}
		if factoryCfg.GetIdentityCookie() != "new-cookie-val" {
			t.Errorf("factory IdentityCookie = %q, want %q", factoryCfg.GetIdentityCookie(), "new-cookie-val")
		}

		saved := store.currentConfig(t)
		if saved.GetIdentityCookie() != "new-cookie-val" {
			t.Errorf("saved IdentityCookie = %q, want %q", saved.GetIdentityCookie(), "new-cookie-val")
		}
		if saved.GetLastCookieRefresh() != fixedNow.Unix() {
			t.Errorf("saved LastCookieRefresh = %d, want %d", saved.GetLastCookieRefresh(), fixedNow.Unix())
		}
		if saved.GetAuthAlertIssueId() != 0 {
			t.Errorf("saved AuthAlertIssueId = %d, want 0", saved.GetAuthAlertIssueId())
		}
		if len(saved.GetItems()) != 1 || saved.GetItems()[0].GetRedownloadUrl() != "https://bandcamp.com/download?id=101" {
			t.Errorf("saved Items = %+v, want updated item with redownload_url", saved.GetItems())
		}
		deletes := issues.deleteSnapshot()
		if len(deletes) != 1 || deletes[0] != 55 {
			t.Errorf("DeleteIssue calls = %v, want [55]", deletes)
		}
	})

	t.Run("prefers rotated cookie received during validation", func(t *testing.T) {
		store := newAPITestConfigStore(t, &pb.Config{
			Token:          "tok-rot",
			IdentityCookie: "old-cookie",
		})
		issues := &apiTestIssueManager{}
		logger := &apiTestLogger{}

		s := &Server{
			store:  store,
			issues: issues,
			bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
				return &apiTestBandcampClient{
					identityCookie: cfg.GetIdentityCookie(),
					onRefresh:      onRefresh,
					retrieveItemsFn: func(ctx context.Context, cb func(string)) ([]*bcpb.Item, error) {
						cb("rotated-cookie-val")
						return []*bcpb.Item{{AlbumId: 202, RedownloadUrl: "https://bandcamp.com/dl/202"}}, nil
					},
				}, nil
			},
			now:     func() time.Time { return fixedNow },
			logFunc: logger.log,
		}

		_, err := s.SetCookie(context.Background(), &pb.SetCookieRequest{Cookie: "new-cookie-val"})
		if err != nil {
			t.Fatalf("SetCookie failed: %v", err)
		}

		saved := store.currentConfig(t)
		if saved.GetIdentityCookie() != "rotated-cookie-val" {
			t.Errorf("saved IdentityCookie = %q, want %q", saved.GetIdentityCookie(), "rotated-cookie-val")
		}
		if saved.GetLastCookieRefresh() != fixedNow.Unix() {
			t.Errorf("saved LastCookieRefresh = %d, want %d", saved.GetLastCookieRefresh(), fixedNow.Unix())
		}
	})

	t.Run("persists validated cookie even if clearAuthAlert returns transient error and redacts secret", func(t *testing.T) {
		const secretCookie = "new-valid-secret-cookie"
		store := newAPITestConfigStore(t, &pb.Config{
			Token:            "tok-clear-err",
			IdentityCookie:   "old-cookie",
			AuthAlertIssueId: 77,
		})
		issues := &apiTestIssueManager{
			deleteErr: fmt.Errorf("github 503 with cookie=%s", secretCookie),
		}
		logger := &apiTestLogger{}

		s := &Server{
			store:  store,
			issues: issues,
			bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
				return &apiTestBandcampClient{
					identityCookie: cfg.GetIdentityCookie(),
					onRefresh:      onRefresh,
					retrieveItemsFn: func(ctx context.Context, cb func(string)) ([]*bcpb.Item, error) {
						return []*bcpb.Item{{AlbumId: 303, RedownloadUrl: "https://bandcamp.com/dl/303"}}, nil
					},
				}, nil
			},
			now:     func() time.Time { return fixedNow },
			logFunc: logger.log,
		}

		_, err := s.SetCookie(context.Background(), &pb.SetCookieRequest{Cookie: secretCookie})
		if err == nil {
			t.Fatal("expected transient clearAuthAlert error, got nil")
		}
		if strings.Contains(err.Error(), secretCookie) {
			t.Errorf("returned error leaked cookie: %q", err.Error())
		}

		saved := store.currentConfig(t)
		if saved.GetIdentityCookie() != secretCookie {
			t.Errorf("saved IdentityCookie = %q, want %q", saved.GetIdentityCookie(), secretCookie)
		}
		if saved.GetAuthAlertIssueId() != 77 {
			t.Errorf("saved AuthAlertIssueId = %d, want 77 (preserved for retry)", saved.GetAuthAlertIssueId())
		}
		if len(saved.GetItems()) != 1 || saved.GetItems()[0].GetAlbumId() != 303 {
			t.Errorf("saved Items = %+v, want updated item 303", saved.GetItems())
		}
	})
}

func TestSetToken_AuthenticatedSuccessAndCookieRefresh(t *testing.T) {
	fixedNow := time.Unix(1700003000, 0)
	store := newAPITestConfigStore(t, &pb.Config{
		Token:            "old-tok",
		IdentityCookie:   "valid-cookie",
		AuthAlertIssueId: 42,
	})
	issues := &apiTestIssueManager{}
	logger := &apiTestLogger{}

	var seenCookies []string
	s := &Server{
		store:  store,
		issues: issues,
		bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
			seenCookies = append(seenCookies, cfg.GetIdentityCookie())
			return &apiTestBandcampClient{
				identityCookie: cfg.GetIdentityCookie(),
				onRefresh:      onRefresh,
				retrieveItemsFn: func(ctx context.Context, cb func(string)) ([]*bcpb.Item, error) {
					cb("rotated-during-token")
					return []*bcpb.Item{
						{AlbumId: 501, RedownloadUrl: "https://bandcamp.com/dl/501"},
					}, nil
				},
			}, nil
		},
		now:     func() time.Time { return fixedNow },
		logFunc: logger.log,
	}

	_, err := s.SetToken(context.Background(), &pb.SetTokenRequest{Token: "new-tok"})
	if err != nil {
		t.Fatalf("SetToken failed: %v", err)
	}

	if len(seenCookies) != 1 || seenCookies[0] != "valid-cookie" {
		t.Errorf("factory cookies = %v, want [valid-cookie]", seenCookies)
	}

	saved := store.currentConfig(t)
	if saved.GetToken() != "new-tok" {
		t.Errorf("saved Token = %q, want %q", saved.GetToken(), "new-tok")
	}
	if saved.GetLastTokenRefresh() != fixedNow.Unix() {
		t.Errorf("saved LastTokenRefresh = %d, want %d", saved.GetLastTokenRefresh(), fixedNow.Unix())
	}
	if saved.GetIdentityCookie() != "rotated-during-token" {
		t.Errorf("saved IdentityCookie = %q, want %q", saved.GetIdentityCookie(), "rotated-during-token")
	}
	if saved.GetLastCookieRefresh() != fixedNow.Unix() {
		t.Errorf("saved LastCookieRefresh = %d, want %d", saved.GetLastCookieRefresh(), fixedNow.Unix())
	}
	if saved.GetAuthAlertIssueId() != 0 {
		t.Errorf("saved AuthAlertIssueId = %d, want 0", saved.GetAuthAlertIssueId())
	}
	if len(saved.GetItems()) != 1 || saved.GetItems()[0].GetRedownloadUrl() != "https://bandcamp.com/dl/501" {
		t.Errorf("saved Items = %+v, want item 501", saved.GetItems())
	}
	deletes := issues.deleteSnapshot()
	if len(deletes) != 1 || deletes[0] != 42 {
		t.Errorf("DeleteIssue calls = %v, want [42]", deletes)
	}
}

func TestSetToken_AuthFailedRaisesAlertAndFallsBackUnauthenticated(t *testing.T) {
	const expiredCookie = "expired-secret-cookie"
	fixedNow := time.Unix(1700004000, 0)

	store := newAPITestConfigStore(t, &pb.Config{
		Token:            "old-tok",
		IdentityCookie:   expiredCookie,
		AuthAlertIssueId: 0,
	})
	issues := &apiTestIssueManager{nextIssueID: 88}
	logger := &apiTestLogger{}

	var callCookies []string
	s := &Server{
		store:  store,
		issues: issues,
		bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
			callCookies = append(callCookies, cfg.GetIdentityCookie())
			if cfg.GetIdentityCookie() != "" {
				return &apiTestBandcampClient{
					identityCookie: cfg.GetIdentityCookie(),
					onRefresh:      onRefresh,
					retrieveItemsFn: func(ctx context.Context, cb func(string)) ([]*bcpb.Item, error) {
						return nil, fmt.Errorf("%w: cookie=%s", bandcamplib.ErrAuthenticationFailed, expiredCookie)
					},
				}, nil
			}
			return &apiTestBandcampClient{
				retrieveItemsFn: func(ctx context.Context, cb func(string)) ([]*bcpb.Item, error) {
					return []*bcpb.Item{{AlbumId: 601, AlbumTitle: "Unauth Item"}}, nil
				},
			}, nil
		},
		now:     func() time.Time { return fixedNow },
		logFunc: logger.log,
	}

	_, err := s.SetToken(context.Background(), &pb.SetTokenRequest{Token: "new-tok"})
	if err != nil {
		t.Fatalf("SetToken failed on auth fallback: %v", err)
	}

	if len(callCookies) != 2 || callCookies[0] != expiredCookie || callCookies[1] != "" {
		t.Errorf("factory call cookies = %v, want [%q, \"\"]", callCookies, expiredCookie)
	}

	saved := store.currentConfig(t)
	if saved.GetToken() != "new-tok" {
		t.Errorf("saved Token = %q, want %q", saved.GetToken(), "new-tok")
	}
	if saved.GetLastTokenRefresh() != fixedNow.Unix() {
		t.Errorf("saved LastTokenRefresh = %d, want %d", saved.GetLastTokenRefresh(), fixedNow.Unix())
	}
	if saved.GetAuthAlertIssueId() != 88 {
		t.Errorf("saved AuthAlertIssueId = %d, want 88", saved.GetAuthAlertIssueId())
	}
	if len(saved.GetItems()) != 1 || saved.GetItems()[0].GetAlbumId() != 601 {
		t.Errorf("saved Items = %+v, want unauthenticated fallback item 601", saved.GetItems())
	}

	// Second call while alert is still open must deduplicate alert creation.
	_, err = s.SetToken(context.Background(), &pb.SetTokenRequest{Token: "new-tok-2"})
	if err != nil {
		t.Fatalf("second SetToken failed on auth fallback: %v", err)
	}

	created := issues.immediateSnapshot()
	if len(created) != 1 {
		t.Fatalf("expected 1 deduplicated auth alert issue, got %d", len(created))
	}
	if strings.Contains(created[0].body, expiredCookie) || strings.Contains(created[0].title, expiredCookie) {
		t.Errorf("auth alert issue leaked expired cookie: title=%q body=%q", created[0].title, created[0].body)
	}
	for _, entry := range logger.entries() {
		if strings.Contains(entry, expiredCookie) {
			t.Errorf("log entry leaked expired cookie: %q", entry)
		}
	}
}

func TestSetToken_UnauthenticatedWhenNoCookie(t *testing.T) {
	fixedNow := time.Unix(1700005000, 0)
	store := newAPITestConfigStore(t, &pb.Config{
		Token: "old-tok",
	})
	issues := &apiTestIssueManager{}
	logger := &apiTestLogger{}

	var callCookies []string
	s := &Server{
		store:  store,
		issues: issues,
		bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
			callCookies = append(callCookies, cfg.GetIdentityCookie())
			return &apiTestBandcampClient{
				retrieveItemsFn: func(ctx context.Context, cb func(string)) ([]*bcpb.Item, error) {
					return []*bcpb.Item{{AlbumId: 701, AlbumTitle: "Public"}}, nil
				},
			}, nil
		},
		now:     func() time.Time { return fixedNow },
		logFunc: logger.log,
	}

	_, err := s.SetToken(context.Background(), &pb.SetTokenRequest{Token: "tok-no-cookie"})
	if err != nil {
		t.Fatalf("SetToken failed: %v", err)
	}

	if len(callCookies) != 1 || callCookies[0] != "" {
		t.Errorf("factory cookies = %v, want [\"\"]", callCookies)
	}
	if len(issues.immediateSnapshot()) != 0 {
		t.Errorf("expected no auth alerts when no cookie is configured, got %d", len(issues.immediateSnapshot()))
	}

	saved := store.currentConfig(t)
	if saved.GetToken() != "tok-no-cookie" {
		t.Errorf("saved Token = %q, want %q", saved.GetToken(), "tok-no-cookie")
	}
	if saved.GetLastTokenRefresh() != fixedNow.Unix() {
		t.Errorf("saved LastTokenRefresh = %d, want %d", saved.GetLastTokenRefresh(), fixedNow.Unix())
	}
	if len(saved.GetItems()) != 1 || saved.GetItems()[0].GetAlbumId() != 701 {
		t.Errorf("saved Items = %+v, want item 701", saved.GetItems())
	}
}

func TestAddMapping_DeletesStaleDownloadLinkFailures(t *testing.T) {
	fixedNow := time.Unix(1700006000, 0)
	store := newAPITestConfigStore(t, &pb.Config{
		Mapping:              map[int64]int32{},
		IssueIds:             map[int64]int32{12345: 77},
		DownloadLinkFailures: map[int64]int32{12345: 2, 99999: 1},
	})
	issues := &apiTestIssueManager{}
	logger := &apiTestLogger{}

	s := &Server{
		store:   store,
		issues:  issues,
		now:     func() time.Time { return fixedNow },
		logFunc: logger.log,
	}

	_, err := s.AddMapping(context.Background(), &pb.AddMappingRequest{
		BandcampId: 12345,
		DiscogsId:  555,
	})
	if err != nil {
		t.Fatalf("AddMapping failed: %v", err)
	}

	saved := store.currentConfig(t)
	if saved.GetMapping()[12345] != 555 {
		t.Errorf("Mapping[12345] = %d, want 555", saved.GetMapping()[12345])
	}
	if _, exists := saved.GetDownloadLinkFailures()[12345]; exists {
		t.Errorf("expected DownloadLinkFailures[12345] to be deleted, got %v", saved.GetDownloadLinkFailures())
	}
	if saved.GetDownloadLinkFailures()[99999] != 1 {
		t.Errorf("DownloadLinkFailures[99999] = %d, want 1", saved.GetDownloadLinkFailures()[99999])
	}
	if saved.GetLastProcess() != fixedNow.Unix() {
		t.Errorf("LastProcess = %d, want %d", saved.GetLastProcess(), fixedNow.Unix())
	}
	deletes := issues.deleteSnapshot()
	if len(deletes) != 1 || deletes[0] != 77 {
		t.Errorf("DeleteIssue calls = %v, want [77]", deletes)
	}
}

func TestReset_ClearsIssueIds(t *testing.T) {
	store := newAPITestConfigStore(t, &pb.Config{
		IssueIds: map[int64]int32{1: 10, 2: 20},
	})
	s := &Server{
		store:   store,
		issues:  &apiTestIssueManager{},
		now:     func() time.Time { return time.Unix(1700007000, 0) },
		logFunc: (&apiTestLogger{}).log,
	}

	_, err := s.Reset(context.Background(), &pb.ResetRequest{})
	if err != nil {
		t.Fatalf("Reset failed: %v", err)
	}

	saved := store.currentConfig(t)
	if len(saved.GetIssueIds()) != 0 {
		t.Errorf("expected empty IssueIds after Reset, got %v", saved.GetIssueIds())
	}
}

func TestRPCs_ConcurrentSerialization(t *testing.T) {
	fixedNow := time.Unix(1700008000, 0)
	store := newAPITestConfigStore(t, &pb.Config{
		Token:                "initial-tok",
		IdentityCookie:       "initial-cookie",
		DownloadLinkFailures: map[int64]int32{1: 2, 2: 1},
		IssueIds:             map[int64]int32{1: 10},
	})
	issues := &apiTestIssueManager{}
	logger := &apiTestLogger{}

	s := &Server{
		store:  store,
		issues: issues,
		bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
			return &apiTestBandcampClient{
				identityCookie: cfg.GetIdentityCookie(),
				onRefresh:      onRefresh,
				retrieveItemsFn: func(ctx context.Context, cb func(string)) ([]*bcpb.Item, error) {
					return []*bcpb.Item{{AlbumId: 1, RedownloadUrl: "https://bandcamp.com/dl/1"}}, nil
				},
			}, nil
		},
		now:     func() time.Time { return fixedNow },
		logFunc: logger.log,
	}

	var wg sync.WaitGroup
	errs := make(chan error, 16)

	for i := 0; i < 4; i++ {
		wg.Add(4)
		go func(idx int) {
			defer wg.Done()
			_, err := s.SetCookie(context.Background(), &pb.SetCookieRequest{Cookie: fmt.Sprintf("cookie-%d", idx)})
			if err != nil {
				errs <- fmt.Errorf("SetCookie(%d): %w", idx, err)
			}
		}(i)
		go func(idx int) {
			defer wg.Done()
			_, err := s.SetToken(context.Background(), &pb.SetTokenRequest{Token: fmt.Sprintf("tok-%d", idx)})
			if err != nil {
				errs <- fmt.Errorf("SetToken(%d): %w", idx, err)
			}
		}(i)
		go func(idx int) {
			defer wg.Done()
			_, err := s.AddMapping(context.Background(), &pb.AddMappingRequest{BandcampId: int64(idx + 1), DiscogsId: int32(idx + 100)})
			if err != nil {
				errs <- fmt.Errorf("AddMapping(%d): %w", idx, err)
			}
		}(i)
		go func() {
			defer wg.Done()
			_, err := s.Reset(context.Background(), &pb.ResetRequest{})
			if err != nil {
				errs <- fmt.Errorf("Reset: %w", err)
			}
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Error(err)
	}

	saved := store.currentConfig(t)
	if len(saved.GetMapping()) != 4 {
		t.Errorf("expected 4 mappings preserved across concurrent updates, got %v", saved.GetMapping())
	}
	if _, exists := saved.GetDownloadLinkFailures()[1]; exists {
		t.Errorf("expected DownloadLinkFailures[1] deleted, got %v", saved.GetDownloadLinkFailures())
	}
	if _, exists := saved.GetDownloadLinkFailures()[2]; exists {
		t.Errorf("expected DownloadLinkFailures[2] deleted, got %v", saved.GetDownloadLinkFailures())
	}
}

func TestRefreshPaths_ItemUrlPersistenceAndStaleOverwrite(t *testing.T) {
	fixedNow := time.Unix(1700009000, 0)

	t.Run("SetToken authenticated success persists ItemUrl", func(t *testing.T) {
		store := newAPITestConfigStore(t, &pb.Config{
			Token:          "old-tok",
			IdentityCookie: "valid-cookie",
		})
		s := &Server{
			store:  store,
			issues: &apiTestIssueManager{},
			bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
				return &apiTestBandcampClient{
					identityCookie: cfg.GetIdentityCookie(),
					onRefresh:      onRefresh,
					retrieveItemsFn: func(ctx context.Context, cb func(string)) ([]*bcpb.Item, error) {
						return []*bcpb.Item{
							{
								AlbumId:       101,
								BandName:      "Album Artist",
								AlbumTitle:    "Album One",
								RedownloadUrl: "https://bandcamp.com/download?id=101",
								ItemUrl:       "https://albumartist.bandcamp.com/album/album-one",
							},
							{
								AlbumId:    102,
								BandName:   "Custom Artist",
								AlbumTitle: "Track Two",
								ItemUrl:    "https://music.customartist.com/track/track-two",
							},
						}, nil
					},
				}, nil
			},
			now:     func() time.Time { return fixedNow },
			logFunc: (&apiTestLogger{}).log,
		}

		if _, err := s.SetToken(context.Background(), &pb.SetTokenRequest{Token: "new-tok"}); err != nil {
			t.Fatalf("SetToken failed: %v", err)
		}

		saved := store.currentConfig(t)
		if len(saved.GetItems()) != 2 {
			t.Fatalf("saved Items count = %d, want 2", len(saved.GetItems()))
		}
		if got := saved.GetItems()[0].GetItemUrl(); got != "https://albumartist.bandcamp.com/album/album-one" {
			t.Errorf("Items[0].ItemUrl = %q, want album URL", got)
		}
		if got := saved.GetItems()[1].GetItemUrl(); got != "https://music.customartist.com/track/track-two" {
			t.Errorf("Items[1].ItemUrl = %q, want custom track URL", got)
		}
	})

	t.Run("SetToken auth failure fallback persists unauthenticated ItemUrl", func(t *testing.T) {
		store := newAPITestConfigStore(t, &pb.Config{
			Token:          "old-tok",
			IdentityCookie: "expired-cookie",
		})
		s := &Server{
			store:  store,
			issues: &apiTestIssueManager{nextIssueID: 91},
			bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
				if cfg.GetIdentityCookie() != "" {
					return &apiTestBandcampClient{
						identityCookie: cfg.GetIdentityCookie(),
						onRefresh:      onRefresh,
						retrieveItemsFn: func(ctx context.Context, cb func(string)) ([]*bcpb.Item, error) {
							return nil, bandcamplib.ErrAuthenticationFailed
						},
					}, nil
				}
				return &apiTestBandcampClient{
					retrieveItemsFn: func(ctx context.Context, cb func(string)) ([]*bcpb.Item, error) {
						return []*bcpb.Item{
							{
								AlbumId:    201,
								BandName:   "Fallback Artist",
								AlbumTitle: "Fallback Album",
								ItemUrl:    "https://fallback.bandcamp.com/album/fallback-album",
							},
						}, nil
					},
				}, nil
			},
			now:     func() time.Time { return fixedNow },
			logFunc: (&apiTestLogger{}).log,
		}

		if _, err := s.SetToken(context.Background(), &pb.SetTokenRequest{Token: "new-tok"}); err != nil {
			t.Fatalf("SetToken fallback failed: %v", err)
		}

		saved := store.currentConfig(t)
		if len(saved.GetItems()) != 1 || saved.GetItems()[0].GetItemUrl() != "https://fallback.bandcamp.com/album/fallback-album" {
			t.Errorf("saved Items = %+v, want fallback ItemUrl persisted", saved.GetItems())
		}
	})

	t.Run("SetToken unauthenticated without cookie persists ItemUrl", func(t *testing.T) {
		store := newAPITestConfigStore(t, &pb.Config{
			Token: "old-tok",
		})
		s := &Server{
			store:  store,
			issues: &apiTestIssueManager{},
			bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
				return &apiTestBandcampClient{
					retrieveItemsFn: func(ctx context.Context, cb func(string)) ([]*bcpb.Item, error) {
						return []*bcpb.Item{
							{
								AlbumId:    301,
								BandName:   "Public Artist",
								AlbumTitle: "Public Track",
								ItemUrl:    "https://publicartist.bandcamp.com/track/public-track",
							},
						}, nil
					},
				}, nil
			},
			now:     func() time.Time { return fixedNow },
			logFunc: (&apiTestLogger{}).log,
		}

		if _, err := s.SetToken(context.Background(), &pb.SetTokenRequest{Token: "new-tok"}); err != nil {
			t.Fatalf("SetToken failed: %v", err)
		}

		saved := store.currentConfig(t)
		if len(saved.GetItems()) != 1 || saved.GetItems()[0].GetItemUrl() != "https://publicartist.bandcamp.com/track/public-track" {
			t.Errorf("saved Items = %+v, want unauthenticated ItemUrl persisted", saved.GetItems())
		}
	})

	t.Run("SetCookie persists ItemUrl", func(t *testing.T) {
		store := newAPITestConfigStore(t, &pb.Config{
			Token: "tok-1",
		})
		s := &Server{
			store:  store,
			issues: &apiTestIssueManager{},
			bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
				return &apiTestBandcampClient{
					identityCookie: cfg.GetIdentityCookie(),
					onRefresh:      onRefresh,
					retrieveItemsFn: func(ctx context.Context, cb func(string)) ([]*bcpb.Item, error) {
						return []*bcpb.Item{
							{
								AlbumId:       401,
								BandName:      "Cookie Artist",
								AlbumTitle:    "Cookie Album",
								RedownloadUrl: "https://bandcamp.com/download?id=401",
								ItemUrl:       "https://cookieartist.bandcamp.com/album/cookie-album",
							},
						}, nil
					},
				}, nil
			},
			now:     func() time.Time { return fixedNow },
			logFunc: (&apiTestLogger{}).log,
		}

		if _, err := s.SetCookie(context.Background(), &pb.SetCookieRequest{Cookie: "new-cookie"}); err != nil {
			t.Fatalf("SetCookie failed: %v", err)
		}

		saved := store.currentConfig(t)
		if len(saved.GetItems()) != 1 || saved.GetItems()[0].GetItemUrl() != "https://cookieartist.bandcamp.com/album/cookie-album" {
			t.Errorf("saved Items = %+v, want SetCookie ItemUrl persisted", saved.GetItems())
		}
	})

	t.Run("SetToken and SetCookie overwrite stale ItemUrl when refreshed item omits ItemUrl", func(t *testing.T) {
		for _, path := range []string{"SetToken", "SetCookie"} {
			t.Run(path, func(t *testing.T) {
				store := newAPITestConfigStore(t, &pb.Config{
					Token:          "tok-1",
					IdentityCookie: "valid-cookie",
					Items: []*bcpb.Item{
						{
							AlbumId:    501,
							BandName:   "Stale Artist",
							AlbumTitle: "Stale Album",
							ItemUrl:    "https://staleartist.bandcamp.com/album/old-url",
						},
					},
				})
				s := &Server{
					store:  store,
					issues: &apiTestIssueManager{},
					bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
						return &apiTestBandcampClient{
							identityCookie: cfg.GetIdentityCookie(),
							onRefresh:      onRefresh,
							retrieveItemsFn: func(ctx context.Context, cb func(string)) ([]*bcpb.Item, error) {
								return []*bcpb.Item{
									{
										AlbumId:    501,
										BandName:   "Stale Artist",
										AlbumTitle: "Stale Album",
										ItemUrl:    "",
									},
								}, nil
							},
						}, nil
					},
					now:     func() time.Time { return fixedNow },
					logFunc: (&apiTestLogger{}).log,
				}

				if path == "SetToken" {
					if _, err := s.SetToken(context.Background(), &pb.SetTokenRequest{Token: "tok-2"}); err != nil {
						t.Fatalf("SetToken failed: %v", err)
					}
				} else {
					if _, err := s.SetCookie(context.Background(), &pb.SetCookieRequest{Cookie: "cookie-2"}); err != nil {
						t.Fatalf("SetCookie failed: %v", err)
					}
				}

				saved := store.currentConfig(t)
				if len(saved.GetItems()) != 1 || saved.GetItems()[0].GetItemUrl() != "" {
					t.Errorf("%s: expected stale ItemUrl overwritten to empty, got %q", path, saved.GetItems()[0].GetItemUrl())
				}
			})
		}
	})
}
