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
	pbgh "github.com/brotherlogic/githubcard/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type dlConfigStore struct {
	savedData [][]byte
	saveErr   error
	saveFn    func(ctx context.Context, data []byte, key string) error
}

func (f *dlConfigStore) Load(ctx context.Context, key string) ([]byte, error) {
	return nil, status.Error(codes.InvalidArgument, "not found")
}

func (f *dlConfigStore) Save(ctx context.Context, data []byte, key string) error {
	cp := make([]byte, len(data))
	copy(cp, data)
	f.savedData = append(f.savedData, cp)
	if f.saveFn != nil {
		return f.saveFn(ctx, cp, key)
	}
	return f.saveErr
}

func (f *dlConfigStore) lastSavedConfig(t *testing.T) *pb.Config {
	t.Helper()
	if len(f.savedData) == 0 {
		t.Fatal("expected at least one saved config, got 0")
	}
	cfg := &pb.Config{}
	if err := proto.Unmarshal(f.savedData[len(f.savedData)-1], cfg); err != nil {
		t.Fatalf("failed to unmarshal saved config: %v", err)
	}
	return cfg
}

type dlBandcampClient struct {
	retrieveItemsFn func(ctx context.Context, onRefresh func(string)) ([]*bcpb.Item, error)
	getDownloadFn   func(ctx context.Context, redownloadURL string, format bandcamplib.Format, onRefresh func(string)) (string, error)
	identityCookie  string
	onRefresh       func(string)
}

func (f *dlBandcampClient) RetrieveItems(ctx context.Context) ([]*bcpb.Item, error) {
	if f.retrieveItemsFn == nil {
		return nil, nil
	}
	return f.retrieveItemsFn(ctx, f.onRefresh)
}

func (f *dlBandcampClient) GetDownloadURLForRedownloadURL(ctx context.Context, redownloadURL string, format bandcamplib.Format) (string, error) {
	if f.getDownloadFn == nil {
		return "", nil
	}
	return f.getDownloadFn(ctx, redownloadURL, format, f.onRefresh)
}

func (f *dlBandcampClient) IdentityCookie() string {
	return f.identityCookie
}

type dlIssueCall struct {
	title            string
	body             string
	printImmediately bool
	print            bool
	labels           []string
}

type dlIssueManager struct {
	immediateCalls []dlIssueCall
	immediateIssue *pbgh.Issue
	immediateErr   error
	deleteCalls    []int32
	deleteErr      error
}

func (f *dlIssueManager) ImmediateIssue(ctx context.Context, title, body string, printImmediately, print bool, labels ...string) (*pbgh.Issue, error) {
	f.immediateCalls = append(f.immediateCalls, dlIssueCall{
		title:            title,
		body:             body,
		printImmediately: printImmediately,
		print:            print,
		labels:           labels,
	})
	if f.immediateErr != nil {
		return nil, f.immediateErr
	}
	if f.immediateIssue != nil {
		return f.immediateIssue, nil
	}
	return &pbgh.Issue{Number: 101}, nil
}

func (f *dlIssueManager) DeleteIssue(ctx context.Context, number int32) error {
	f.deleteCalls = append(f.deleteCalls, number)
	return f.deleteErr
}

func (f *dlIssueManager) RaiseIssue(title, body string, labels ...string) {}

func TestNormalizeCookieInput(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		want     string
		wantCode codes.Code
	}{
		{
			name: "plain cookie",
			raw:  "abc123%3Dxyz",
			want: "abc123%3Dxyz",
		},
		{
			name: "trims leading and trailing whitespace",
			raw:  "  \t abc123 \n ",
			want: "abc123",
		},
		{
			name: "strips lowercase identity= prefix",
			raw:  "identity=abc123",
			want: "abc123",
		},
		{
			name: "strips uppercase IDENTITY= prefix and inner whitespace",
			raw:  "  IDENTITY=  abc=123==  \n",
			want: "abc=123==",
		},
		{
			name: "strips mixed case Identity= prefix",
			raw:  "Identity=secret_val",
			want: "secret_val",
		},
		{
			name:     "empty string rejected",
			raw:      "",
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "whitespace only rejected",
			raw:      "   \t\n  ",
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "bare identity= prefix rejected",
			raw:      "identity=",
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "whitespace around IDENTITY= prefix rejected",
			raw:      "  IDENTITY=   \t",
			wantCode: codes.InvalidArgument,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeCookieInput(tc.raw)
			if tc.wantCode != codes.OK {
				if err == nil {
					t.Fatalf("expected error with code %v, got nil (value=%q)", tc.wantCode, got)
				}
				if status.Code(err) != tc.wantCode {
					t.Fatalf("status.Code(err) = %v, want %v (err=%v)", status.Code(err), tc.wantCode, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("normalizeCookieInput(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestEnsureAuthAlert(t *testing.T) {
	t.Run("creates alert once, persists id, deduplicates, and redacts secret", func(t *testing.T) {
		const secretCookie = "secret-cookie-value-xyz"
		store := &dlConfigStore{}
		issues := &dlIssueManager{immediateIssue: &pbgh.Issue{Number: 77}}
		var logs []string
		s := &Server{
			store:   store,
			issues:  issues,
			now:     func() time.Time { return time.Unix(1700000000, 0) },
			logFunc: func(ctx context.Context, msg string) { logs = append(logs, msg) },
		}

		cfg := &pb.Config{IdentityCookie: secretCookie}
		err := s.ensureAuthAlert(context.Background(), cfg, "expired while checking cookie "+secretCookie)
		if err != nil {
			t.Fatalf("ensureAuthAlert returned unexpected error: %v", err)
		}

		if len(issues.immediateCalls) != 1 {
			t.Fatalf("expected 1 ImmediateIssue call, got %d", len(issues.immediateCalls))
		}
		call := issues.immediateCalls[0]
		if call.title != "Bandcamp session cookie missing or expired" {
			t.Errorf("issue title = %q, want %q", call.title, "Bandcamp session cookie missing or expired")
		}
		if call.printImmediately || call.print {
			t.Errorf("expected printImmediately=false, print=false, got %v, %v", call.printImmediately, call.print)
		}
		if strings.Contains(call.body, secretCookie) {
			t.Errorf("issue body leaked raw cookie: %q", call.body)
		}
		for _, wantSubstr := range []string{
			"https://bandcamp.com",
			"identity",
			"bandcampserver_cli cookie -cookie '<value>'",
		} {
			if !strings.Contains(call.body, wantSubstr) {
				t.Errorf("issue body missing %q; body=%q", wantSubstr, call.body)
			}
		}

		if cfg.GetAuthAlertIssueId() != 77 {
			t.Errorf("cfg.AuthAlertIssueId = %d, want 77", cfg.GetAuthAlertIssueId())
		}
		saved := store.lastSavedConfig(t)
		if saved.GetAuthAlertIssueId() != 77 {
			t.Errorf("saved AuthAlertIssueId = %d, want 77", saved.GetAuthAlertIssueId())
		}

		// Subsequent call must deduplicate.
		if err := s.ensureAuthAlert(context.Background(), cfg, "second failure"); err != nil {
			t.Fatalf("second ensureAuthAlert returned error: %v", err)
		}
		if len(issues.immediateCalls) != 1 {
			t.Errorf("expected deduplicated ImmediateIssue count 1, got %d", len(issues.immediateCalls))
		}
		if len(store.savedData) != 1 {
			t.Errorf("expected deduplicated saveConfig count 1, got %d", len(store.savedData))
		}
		for _, entry := range logs {
			if strings.Contains(entry, secretCookie) {
				t.Errorf("log entry leaked cookie: %q", entry)
			}
		}
	})

	t.Run("propagates ImmediateIssue and saveConfig errors without leaking cookie", func(t *testing.T) {
		const secretCookie = "secret-cookie-leak-test"
		store := &dlConfigStore{}
		issues := &dlIssueManager{
			immediateErr: fmt.Errorf("failed with %s", secretCookie),
		}
		s := &Server{
			store:   store,
			issues:  issues,
			logFunc: func(ctx context.Context, msg string) {},
		}
		cfg := &pb.Config{IdentityCookie: secretCookie}
		err := s.ensureAuthAlert(context.Background(), cfg, "missing")
		if err == nil {
			t.Fatal("expected error from ImmediateIssue, got nil")
		}
		if strings.Contains(err.Error(), secretCookie) {
			t.Errorf("returned error leaked cookie: %q", err.Error())
		}
		if cfg.GetAuthAlertIssueId() != 0 {
			t.Errorf("cfg.AuthAlertIssueId = %d, want 0", cfg.GetAuthAlertIssueId())
		}

		issues.immediateErr = nil
		issues.immediateIssue = &pbgh.Issue{Number: 99}
		store.saveErr = fmt.Errorf("save failed for %s", secretCookie)
		err = s.ensureAuthAlert(context.Background(), cfg, "missing")
		if err == nil {
			t.Fatal("expected error from saveConfig, got nil")
		}
		if strings.Contains(err.Error(), secretCookie) {
			t.Errorf("returned save error leaked cookie: %q", err.Error())
		}
	})
}

func TestClearAuthAlert(t *testing.T) {
	t.Run("no-ops when AuthAlertIssueId is 0", func(t *testing.T) {
		issues := &dlIssueManager{}
		s := &Server{
			issues:  issues,
			logFunc: func(ctx context.Context, msg string) {},
		}
		cfg := &pb.Config{AuthAlertIssueId: 0}
		if err := s.clearAuthAlert(context.Background(), cfg); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(issues.deleteCalls) != 0 {
			t.Errorf("expected 0 DeleteIssue calls, got %d", len(issues.deleteCalls))
		}
	})

	t.Run("deletes active alert and resets AuthAlertIssueId", func(t *testing.T) {
		issues := &dlIssueManager{}
		s := &Server{
			issues:  issues,
			logFunc: func(ctx context.Context, msg string) {},
		}
		cfg := &pb.Config{AuthAlertIssueId: 88}
		if err := s.clearAuthAlert(context.Background(), cfg); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(issues.deleteCalls) != 1 || issues.deleteCalls[0] != 88 {
			t.Errorf("deleteCalls = %v, want [88]", issues.deleteCalls)
		}
		if cfg.GetAuthAlertIssueId() != 0 {
			t.Errorf("cfg.AuthAlertIssueId = %d, want 0", cfg.GetAuthAlertIssueId())
		}
	})

	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "grpc NotFound", err: status.Error(codes.NotFound, "issue not found")},
		{name: "already closed", err: errors.New("GitHub issue #88 is already closed")},
		{name: "404 error", err: errors.New("unexpected status 404 Not Found")},
	} {
		t.Run("tolerates "+tc.name, func(t *testing.T) {
			issues := &dlIssueManager{deleteErr: tc.err}
			s := &Server{
				issues:  issues,
				logFunc: func(ctx context.Context, msg string) {},
			}
			cfg := &pb.Config{AuthAlertIssueId: 88}
			if err := s.clearAuthAlert(context.Background(), cfg); err != nil {
				t.Fatalf("expected nil error for %s, got %v", tc.name, err)
			}
			if cfg.GetAuthAlertIssueId() != 0 {
				t.Errorf("cfg.AuthAlertIssueId = %d, want 0", cfg.GetAuthAlertIssueId())
			}
		})
	}

	t.Run("returns transient error without resetting AuthAlertIssueId", func(t *testing.T) {
		issues := &dlIssueManager{deleteErr: status.Error(codes.Unavailable, "githubcard down")}
		s := &Server{
			issues:  issues,
			logFunc: func(ctx context.Context, msg string) {},
		}
		cfg := &pb.Config{AuthAlertIssueId: 88}
		err := s.clearAuthAlert(context.Background(), cfg)
		if err == nil {
			t.Fatal("expected transient error, got nil")
		}
		if cfg.GetAuthAlertIssueId() != 88 {
			t.Errorf("cfg.AuthAlertIssueId = %d, want 88", cfg.GetAuthAlertIssueId())
		}
	})
}

func TestBandcampCookieAutoRefreshPersistence(t *testing.T) {
	const (
		oldCookie = "old-secret-cookie-111"
		newCookie = "refreshed-secret-cookie-222"
	)
	fixedNow := time.Unix(1700001234, 0)

	t.Run("persists callback cookie on downstream success", func(t *testing.T) {
		store := &dlConfigStore{}
		s := &Server{
			store: store,
			bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
				return &dlBandcampClient{
					identityCookie: oldCookie,
					onRefresh:      onRefresh,
					getDownloadFn: func(ctx context.Context, redownloadURL string, format bandcamplib.Format, cb func(string)) (string, error) {
						cb(newCookie)
						return "https://download.example/wav", nil
					},
				}, nil
			},
			now:     func() time.Time { return fixedNow },
			logFunc: func(ctx context.Context, msg string) {},
		}

		cfg := &pb.Config{IdentityCookie: oldCookie}
		var gotURL string
		err := s.withBandcampClient(context.Background(), cfg, func(client bandcampClient) error {
			u, err := client.GetDownloadURLForRedownloadURL(context.Background(), "https://bandcamp.com/download?id=1", bandcamplib.FormatWAV)
			gotURL = u
			return err
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if gotURL != "https://download.example/wav" {
			t.Errorf("gotURL = %q, want %q", gotURL, "https://download.example/wav")
		}
		if cfg.GetIdentityCookie() != newCookie {
			t.Errorf("cfg.IdentityCookie = %q, want %q", cfg.GetIdentityCookie(), newCookie)
		}
		if cfg.GetLastCookieRefresh() != fixedNow.Unix() {
			t.Errorf("cfg.LastCookieRefresh = %d, want %d", cfg.GetLastCookieRefresh(), fixedNow.Unix())
		}
		saved := store.lastSavedConfig(t)
		if saved.GetIdentityCookie() != newCookie || saved.GetLastCookieRefresh() != fixedNow.Unix() {
			t.Errorf("saved config = (%q, %d), want (%q, %d)", saved.GetIdentityCookie(), saved.GetLastCookieRefresh(), newCookie, fixedNow.Unix())
		}
	})

	t.Run("persists client.IdentityCookie() when callback did not fire", func(t *testing.T) {
		store := &dlConfigStore{}
		s := &Server{
			store: store,
			bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
				client := &dlBandcampClient{
					identityCookie: oldCookie,
					onRefresh:      onRefresh,
				}
				client.getDownloadFn = func(ctx context.Context, redownloadURL string, format bandcamplib.Format, cb func(string)) (string, error) {
					client.identityCookie = newCookie
					return "https://download.example/wav2", nil
				}
				return client, nil
			},
			now:     func() time.Time { return fixedNow },
			logFunc: func(ctx context.Context, msg string) {},
		}

		cfg := &pb.Config{IdentityCookie: oldCookie}
		err := s.withBandcampClient(context.Background(), cfg, func(client bandcampClient) error {
			_, err := client.GetDownloadURLForRedownloadURL(context.Background(), "https://bandcamp.com/download?id=2", bandcamplib.FormatWAV)
			return err
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.GetIdentityCookie() != newCookie {
			t.Errorf("cfg.IdentityCookie = %q, want %q", cfg.GetIdentityCookie(), newCookie)
		}
		saved := store.lastSavedConfig(t)
		if saved.GetIdentityCookie() != newCookie {
			t.Errorf("saved.IdentityCookie = %q, want %q", saved.GetIdentityCookie(), newCookie)
		}
	})

	t.Run("persists refreshed cookie even when downstream call fails", func(t *testing.T) {
		store := &dlConfigStore{}
		s := &Server{
			store: store,
			bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
				return &dlBandcampClient{
					identityCookie: oldCookie,
					onRefresh:      onRefresh,
					getDownloadFn: func(ctx context.Context, redownloadURL string, format bandcamplib.Format, cb func(string)) (string, error) {
						cb(newCookie)
						return "", bandcamplib.ErrDownloadTimeout
					},
				}, nil
			},
			now:     func() time.Time { return fixedNow },
			logFunc: func(ctx context.Context, msg string) {},
		}

		cfg := &pb.Config{IdentityCookie: oldCookie}
		err := s.withBandcampClient(context.Background(), cfg, func(client bandcampClient) error {
			_, err := client.GetDownloadURLForRedownloadURL(context.Background(), "https://bandcamp.com/download?id=3", bandcamplib.FormatWAV)
			return err
		})
		if !errors.Is(err, bandcamplib.ErrDownloadTimeout) {
			t.Fatalf("expected ErrDownloadTimeout, got %v", err)
		}
		if cfg.GetIdentityCookie() != newCookie {
			t.Errorf("cfg.IdentityCookie = %q, want %q", cfg.GetIdentityCookie(), newCookie)
		}
		if cfg.GetLastCookieRefresh() != fixedNow.Unix() {
			t.Errorf("cfg.LastCookieRefresh = %d, want %d", cfg.GetLastCookieRefresh(), fixedNow.Unix())
		}
		saved := store.lastSavedConfig(t)
		if saved.GetIdentityCookie() != newCookie {
			t.Errorf("saved.IdentityCookie = %q, want %q", saved.GetIdentityCookie(), newCookie)
		}
	})

	t.Run("surfaces saveConfig failure without leaking cookie values", func(t *testing.T) {
		saveErr := fmt.Errorf("dstore write failed for %s -> %s", oldCookie, newCookie)
		store := &dlConfigStore{saveErr: saveErr}
		s := &Server{
			store: store,
			bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
				return &dlBandcampClient{
					identityCookie: oldCookie,
					onRefresh:      onRefresh,
					getDownloadFn: func(ctx context.Context, redownloadURL string, format bandcamplib.Format, cb func(string)) (string, error) {
						cb(newCookie)
						return "https://download.example/wav", nil
					},
				}, nil
			},
			now:     func() time.Time { return fixedNow },
			logFunc: func(ctx context.Context, msg string) {},
		}

		cfg := &pb.Config{IdentityCookie: oldCookie}
		err := s.withBandcampClient(context.Background(), cfg, func(client bandcampClient) error {
			_, err := client.GetDownloadURLForRedownloadURL(context.Background(), "https://bandcamp.com/download?id=4", bandcamplib.FormatWAV)
			return err
		})
		if err == nil {
			t.Fatal("expected error when saveConfig fails, got nil")
		}
		if strings.Contains(err.Error(), oldCookie) || strings.Contains(err.Error(), newCookie) {
			t.Errorf("saveConfig error leaked cookie: %q", err.Error())
		}

		// Also verify saveConfig failure when downstream call ALSO failed.
		s.bcFactory = func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
			return &dlBandcampClient{
				identityCookie: oldCookie,
				onRefresh:      onRefresh,
				getDownloadFn: func(ctx context.Context, redownloadURL string, format bandcamplib.Format, cb func(string)) (string, error) {
					cb(newCookie)
					return "", fmt.Errorf("%w: cookie=%s", bandcamplib.ErrDownloadUnavailable, newCookie)
				},
			}, nil
		}
		cfg2 := &pb.Config{IdentityCookie: oldCookie}
		err2 := s.withBandcampClient(context.Background(), cfg2, func(client bandcampClient) error {
			_, err := client.GetDownloadURLForRedownloadURL(context.Background(), "https://bandcamp.com/download?id=5", bandcamplib.FormatWAV)
			return err
		})
		if err2 == nil {
			t.Fatal("expected error when both downstream and saveConfig fail, got nil")
		}
		if strings.Contains(err2.Error(), oldCookie) || strings.Contains(err2.Error(), newCookie) {
			t.Errorf("combined error leaked cookie: %q", err2.Error())
		}
		if !strings.Contains(err2.Error(), "persist") && !strings.Contains(err2.Error(), "save") && !strings.Contains(err2.Error(), "dstore") {
			t.Errorf("expected saveConfig persistence failure to be surfaced, got %q", err2.Error())
		}
	})

	t.Run("does not save when cookie did not change", func(t *testing.T) {
		store := &dlConfigStore{}
		s := &Server{
			store: store,
			bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
				return &dlBandcampClient{
					identityCookie: oldCookie,
					onRefresh:      onRefresh,
					getDownloadFn: func(ctx context.Context, redownloadURL string, format bandcamplib.Format, cb func(string)) (string, error) {
						cb(oldCookie)
						return "https://download.example/wav", nil
					},
				}, nil
			},
			now:     func() time.Time { return fixedNow },
			logFunc: func(ctx context.Context, msg string) {},
		}

		cfg := &pb.Config{IdentityCookie: oldCookie}
		err := s.withBandcampClient(context.Background(), cfg, func(client bandcampClient) error {
			_, err := client.GetDownloadURLForRedownloadURL(context.Background(), "https://bandcamp.com/download?id=6", bandcamplib.FormatWAV)
			return err
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(store.savedData) != 0 {
			t.Errorf("expected 0 saves when cookie unchanged, got %d", len(store.savedData))
		}
	})
}

func TestResolveWAVLinkForItem(t *testing.T) {
	const (
		secretCookie    = "resolve-secret-cookie-111"
		rotatedCookie   = "resolve-rotated-cookie-222"
		albumRedownload = "https://bandcamp.com/download?from=collection&payment_id=10"
		trackRedownload = "https://bandcamp.com/download?from=collection&payment_id=20&item_type=t"
	)
	fixedNow := time.Unix(1700005555, 0)

	t.Run("missing or whitespace cookie returns ErrAuthenticationRequired", func(t *testing.T) {
		factoryCalled := false
		s := &Server{
			bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
				factoryCalled = true
				return nil, nil
			},
			logFunc: func(ctx context.Context, msg string) {},
		}

		for _, cookie := range []string{"", "   \t\n"} {
			_, err := s.resolveWAVLinkForItem(context.Background(), &pb.Config{IdentityCookie: cookie}, &bcpb.Item{AlbumId: 1})
			if !errors.Is(err, bandcamplib.ErrAuthenticationRequired) {
				t.Errorf("cookie=%q: got err=%v, want ErrAuthenticationRequired", cookie, err)
			}
		}
		if factoryCalled {
			t.Error("expected bcFactory not to be called when cookie is missing")
		}
	})

	t.Run("fast path resolves WAV for albums and single tracks", func(t *testing.T) {
		for _, tc := range []struct {
			name          string
			item          *bcpb.Item
			wantRedownURL string
			wantWAVURL    string
		}{
			{
				name: "multi-track album",
				item: &bcpb.Item{
					AlbumId:       1001,
					BandName:      "Album Artist",
					AlbumTitle:    "LP",
					RedownloadUrl: albumRedownload,
				},
				wantRedownURL: albumRedownload,
				wantWAVURL:    "https://p4.bcbits.com/download/album-signed-wav",
			},
			{
				name: "single track",
				item: &bcpb.Item{
					AlbumId:       1002,
					BandName:      "Track Artist",
					AlbumTitle:    "Single",
					RedownloadUrl: trackRedownload,
				},
				wantRedownURL: trackRedownload,
				wantWAVURL:    "https://p4.bcbits.com/download/track-signed-wav",
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				store := &dlConfigStore{}
				var seenCfg *bcpb.Config
				var seenFormat bandcamplib.Format
				var seenURL string
				retrieveCalled := false

				s := &Server{
					store: store,
					bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
						seenCfg = cfg
						return &dlBandcampClient{
							identityCookie: cfg.GetIdentityCookie(),
							onRefresh:      onRefresh,
							retrieveItemsFn: func(ctx context.Context, cb func(string)) ([]*bcpb.Item, error) {
								retrieveCalled = true
								return nil, nil
							},
							getDownloadFn: func(ctx context.Context, redownloadURL string, format bandcamplib.Format, cb func(string)) (string, error) {
								seenURL = redownloadURL
								seenFormat = format
								return tc.wantWAVURL, nil
							},
						}, nil
					},
					now:     func() time.Time { return fixedNow },
					logFunc: func(ctx context.Context, msg string) {},
				}

				cfg := &pb.Config{Token: "tok-123", IdentityCookie: secretCookie}
				got, err := s.resolveWAVLinkForItem(context.Background(), cfg, tc.item)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got != tc.wantWAVURL {
					t.Errorf("got WAV URL = %q, want %q", got, tc.wantWAVURL)
				}
				if retrieveCalled {
					t.Error("expected RetrieveItems not to be called on fast path")
				}
				if seenCfg.GetUser() != 873256 || seenCfg.GetToken() != "tok-123" || seenCfg.GetIdentityCookie() != secretCookie {
					t.Errorf("bcFactory config = %+v, want User=873256 Token=tok-123 IdentityCookie=%q", seenCfg, secretCookie)
				}
				if seenURL != tc.wantRedownURL || seenFormat != bandcamplib.FormatWAV {
					t.Errorf("GetDownloadURLForRedownloadURL(%q, %v), want (%q, %v)", seenURL, seenFormat, tc.wantRedownURL, bandcamplib.FormatWAV)
				}
			})
		}
	})

	t.Run("stale metadata refreshes via RetrieveItems, saves items, and preserves rotated cookie", func(t *testing.T) {
		store := &dlConfigStore{}
		s := &Server{
			store: store,
			bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
				return &dlBandcampClient{
					// Deliberately keeps initialCookie on IdentityCookie() to verify callback rotation isn't reverted on subsequent calls.
					identityCookie: secretCookie,
					onRefresh:      onRefresh,
					retrieveItemsFn: func(ctx context.Context, cb func(string)) ([]*bcpb.Item, error) {
						cb(rotatedCookie)
						return []*bcpb.Item{
							{AlbumId: 2001, BandName: "Other", AlbumTitle: "Other", RedownloadUrl: "https://bandcamp.com/other"},
							{AlbumId: 2002, BandName: "Target", AlbumTitle: "Target", RedownloadUrl: "  " + albumRedownload + "  "},
						}, nil
					},
					getDownloadFn: func(ctx context.Context, redownloadURL string, format bandcamplib.Format, cb func(string)) (string, error) {
						if redownloadURL != albumRedownload || format != bandcamplib.FormatWAV {
							return "", fmt.Errorf("unexpected args (%q, %v)", redownloadURL, format)
						}
						return "https://p4.bcbits.com/download/refreshed-wav", nil
					},
				}, nil
			},
			now:     func() time.Time { return fixedNow },
			logFunc: func(ctx context.Context, msg string) {},
		}

		cfg := &pb.Config{
			Token:          "tok-123",
			IdentityCookie: secretCookie,
			Items:          []*bcpb.Item{{AlbumId: 2002, BandName: "Target", AlbumTitle: "Target"}},
		}
		got, err := s.resolveWAVLinkForItem(context.Background(), cfg, cfg.Items[0])
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "https://p4.bcbits.com/download/refreshed-wav" {
			t.Errorf("got WAV URL = %q", got)
		}
		if cfg.GetIdentityCookie() != rotatedCookie {
			t.Errorf("cfg.IdentityCookie = %q, want %q (must not revert)", cfg.GetIdentityCookie(), rotatedCookie)
		}
		if len(cfg.GetItems()) != 2 || cfg.GetItems()[1].GetRedownloadUrl() == "" {
			t.Errorf("expected config.Items to be updated with refreshed items, got %v", cfg.GetItems())
		}
		saved := store.lastSavedConfig(t)
		if saved.GetIdentityCookie() != rotatedCookie || len(saved.GetItems()) != 2 {
			t.Errorf("saved config not updated properly: cookie=%q items=%d", saved.GetIdentityCookie(), len(saved.GetItems()))
		}
	})

	t.Run("stale metadata RetrieveItems ErrAuthenticationFailed persists rotated cookie and returns auth error", func(t *testing.T) {
		store := &dlConfigStore{}
		s := &Server{
			store: store,
			bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
				return &dlBandcampClient{
					identityCookie: secretCookie,
					onRefresh:      onRefresh,
					retrieveItemsFn: func(ctx context.Context, cb func(string)) ([]*bcpb.Item, error) {
						cb(rotatedCookie)
						return nil, fmt.Errorf("%w: rejected %s", bandcamplib.ErrAuthenticationFailed, secretCookie)
					},
				}, nil
			},
			now:     func() time.Time { return fixedNow },
			logFunc: func(ctx context.Context, msg string) {},
		}

		cfg := &pb.Config{IdentityCookie: secretCookie}
		_, err := s.resolveWAVLinkForItem(context.Background(), cfg, &bcpb.Item{AlbumId: 3001})
		if !errors.Is(err, bandcamplib.ErrAuthenticationFailed) {
			t.Fatalf("expected ErrAuthenticationFailed, got %v", err)
		}
		if strings.Contains(err.Error(), secretCookie) {
			t.Errorf("error leaked secret cookie: %q", err.Error())
		}
		if cfg.GetIdentityCookie() != rotatedCookie {
			t.Errorf("cfg.IdentityCookie = %q, want %q", cfg.GetIdentityCookie(), rotatedCookie)
		}
		if saved := store.lastSavedConfig(t); saved.GetIdentityCookie() != rotatedCookie {
			t.Errorf("saved.IdentityCookie = %q, want %q", saved.GetIdentityCookie(), rotatedCookie)
		}
	})

	t.Run("stale metadata still missing RedownloadUrl returns ErrDownloadUnavailable", func(t *testing.T) {
		store := &dlConfigStore{}
		s := &Server{
			store: store,
			bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
				return &dlBandcampClient{
					identityCookie: secretCookie,
					onRefresh:      onRefresh,
					retrieveItemsFn: func(ctx context.Context, cb func(string)) ([]*bcpb.Item, error) {
						return []*bcpb.Item{{AlbumId: 4001, BandName: "Physical", AlbumTitle: "LP"}}, nil
					},
				}, nil
			},
			now:     func() time.Time { return fixedNow },
			logFunc: func(ctx context.Context, msg string) {},
		}

		cfg := &pb.Config{IdentityCookie: secretCookie}
		_, err := s.resolveWAVLinkForItem(context.Background(), cfg, &bcpb.Item{AlbumId: 4001})
		if !errors.Is(err, bandcamplib.ErrDownloadUnavailable) {
			t.Fatalf("expected ErrDownloadUnavailable, got %v", err)
		}
		// Item not found after refresh also returns ErrDownloadUnavailable.
		_, err = s.resolveWAVLinkForItem(context.Background(), cfg, &bcpb.Item{AlbumId: 9999})
		if !errors.Is(err, bandcamplib.ErrDownloadUnavailable) {
			t.Fatalf("expected ErrDownloadUnavailable for missing item, got %v", err)
		}
	})

	t.Run("upstream download errors propagate while persisting refreshed cookie and redacting secrets", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			dlErr  error
		}{
			{name: "ErrDownloadUnavailable", dlErr: bandcamplib.ErrDownloadUnavailable},
			{name: "ErrFormatUnavailable", dlErr: bandcamplib.ErrFormatUnavailable},
			{name: "ErrDownloadTimeout", dlErr: bandcamplib.ErrDownloadTimeout},
			{name: "transient error with secret", dlErr: fmt.Errorf("upstream 502 with %s", secretCookie)},
		} {
			t.Run(tc.name, func(t *testing.T) {
				store := &dlConfigStore{}
				s := &Server{
					store: store,
					bcFactory: func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
						return &dlBandcampClient{
							identityCookie: secretCookie,
							onRefresh:      onRefresh,
							getDownloadFn: func(ctx context.Context, redownloadURL string, format bandcamplib.Format, cb func(string)) (string, error) {
								cb(rotatedCookie)
								return "", tc.dlErr
							},
						}, nil
					},
					now:     func() time.Time { return fixedNow },
					logFunc: func(ctx context.Context, msg string) {},
				}

				cfg := &pb.Config{IdentityCookie: secretCookie}
				_, err := s.resolveWAVLinkForItem(context.Background(), cfg, &bcpb.Item{
					AlbumId:       5001,
					RedownloadUrl: albumRedownload,
				})
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if errors.Is(tc.dlErr, bandcamplib.ErrDownloadUnavailable) || errors.Is(tc.dlErr, bandcamplib.ErrFormatUnavailable) || errors.Is(tc.dlErr, bandcamplib.ErrDownloadTimeout) {
					if !errors.Is(err, tc.dlErr) {
						t.Errorf("errors.Is(%v, %v) = false", err, tc.dlErr)
					}
				}
				if strings.Contains(err.Error(), secretCookie) || strings.Contains(err.Error(), rotatedCookie) {
					t.Errorf("error leaked cookie: %q", err.Error())
				}
				if cfg.GetIdentityCookie() != rotatedCookie {
					t.Errorf("cfg.IdentityCookie = %q, want %q", cfg.GetIdentityCookie(), rotatedCookie)
				}
			})
		}
	})
}
