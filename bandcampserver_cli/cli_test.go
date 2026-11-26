package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"google.golang.org/grpc"

	pb "github.com/brotherlogic/bandcampserver/proto"
	pbrc "github.com/brotherlogic/recordcollection/proto"
)

type fakeBandcampClient struct {
	pb.BandcampServerServiceClient
	setCookieReqs  []*pb.SetCookieRequest
	setTokenReqs   []*pb.SetTokenRequest
	addMappingReqs []*pb.AddMappingRequest
	lookupReqs     []*pb.LookupRequest
	resetCalled    bool

	setCookieErr  error
	setTokenErr   error
	addMappingErr error
	lookupErr     error
	resetErr      error
}

func (f *fakeBandcampClient) SetCookie(ctx context.Context, in *pb.SetCookieRequest, opts ...grpc.CallOption) (*pb.SetCookieResponse, error) {
	f.setCookieReqs = append(f.setCookieReqs, in)
	if f.setCookieErr != nil {
		return nil, f.setCookieErr
	}
	return &pb.SetCookieResponse{}, nil
}

func (f *fakeBandcampClient) SetToken(ctx context.Context, in *pb.SetTokenRequest, opts ...grpc.CallOption) (*pb.SetTokenResponse, error) {
	f.setTokenReqs = append(f.setTokenReqs, in)
	if f.setTokenErr != nil {
		return nil, f.setTokenErr
	}
	return &pb.SetTokenResponse{}, nil
}

func (f *fakeBandcampClient) AddMapping(ctx context.Context, in *pb.AddMappingRequest, opts ...grpc.CallOption) (*pb.AddMappingResponse, error) {
	f.addMappingReqs = append(f.addMappingReqs, in)
	if f.addMappingErr != nil {
		return nil, f.addMappingErr
	}
	return &pb.AddMappingResponse{}, nil
}

func (f *fakeBandcampClient) Reset(ctx context.Context, in *pb.ResetRequest, opts ...grpc.CallOption) (*pb.ResetResponse, error) {
	f.resetCalled = true
	if f.resetErr != nil {
		return nil, f.resetErr
	}
	return &pb.ResetResponse{}, nil
}

func (f *fakeBandcampClient) Lookup(ctx context.Context, in *pb.LookupRequest, opts ...grpc.CallOption) (*pb.LookupResponse, error) {
	f.lookupReqs = append(f.lookupReqs, in)
	if f.lookupErr != nil {
		return nil, f.lookupErr
	}
	return &pb.LookupResponse{}, nil
}

func newTestDial(client *fakeBandcampClient, dialCalled *bool) dialFunc {
	return func(ctx context.Context) (pb.BandcampServerServiceClient, pbrc.ClientUpdateServiceClient, func() error, error) {
		*dialCalled = true
		return client, nil, func() error { return nil }, nil
	}
}

func TestRunInvalidArgsDoNotDial(t *testing.T) {
	tests := []struct {
		name           string
		args           []string
		wantStderrSub  string
	}{
		{
			name:          "missing subcommand",
			args:          nil,
			wantStderrSub: "Usage:",
		},
		{
			name:          "unknown subcommand",
			args:          []string{"unknown"},
			wantStderrSub: "Usage:",
		},
		{
			name:          "cookie missing flag",
			args:          []string{"cookie"},
			wantStderrSub: "cookie",
		},
		{
			name:          "cookie empty value",
			args:          []string{"cookie", "-cookie", ""},
			wantStderrSub: "cookie",
		},
		{
			name:          "cookie whitespace value",
			args:          []string{"cookie", "-cookie", "  \t\n  "},
			wantStderrSub: "cookie",
		},
		{
			name:          "cookie unknown flag",
			args:          []string{"cookie", "-bogus"},
			wantStderrSub: "bogus",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeBandcampClient{}
			dialCalled := false
			var stdout, stderr bytes.Buffer

			code := run(context.Background(), tc.args, &stdout, &stderr, newTestDial(client, &dialCalled))
			if code == 0 {
				t.Fatalf("expected non-zero exit code, got 0 (stdout=%q stderr=%q)", stdout.String(), stderr.String())
			}
			if dialCalled {
				t.Fatalf("expected dial not to be called for invalid args %v", tc.args)
			}
			if len(client.setCookieReqs) != 0 {
				t.Fatalf("expected no SetCookie RPCs, got %d", len(client.setCookieReqs))
			}
			if !strings.Contains(stderr.String(), tc.wantStderrSub) {
				t.Fatalf("expected stderr %q to contain %q", stderr.String(), tc.wantStderrSub)
			}
		})
	}
}

func TestRunCookieSuccess(t *testing.T) {
	const secretCookie = "secret-identity%3Dabc123"
	client := &fakeBandcampClient{}
	dialCalled := false
	var stdout, stderr bytes.Buffer

	code := run(context.Background(), []string{"cookie", "-cookie", secretCookie}, &stdout, &stderr, newTestDial(client, &dialCalled))
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr=%q)", code, stderr.String())
	}
	if !dialCalled {
		t.Fatalf("expected dial to be called")
	}
	if len(client.setCookieReqs) != 1 {
		t.Fatalf("expected 1 SetCookie call, got %d", len(client.setCookieReqs))
	}
	if got := client.setCookieReqs[0].GetCookie(); got != secretCookie {
		t.Fatalf("SetCookie Cookie = %q, want %q", got, secretCookie)
	}
	if stdout.Len() == 0 {
		t.Fatalf("expected confirmation status message on stdout")
	}
	if strings.Contains(stdout.String(), secretCookie) || strings.Contains(stderr.String(), secretCookie) {
		t.Fatalf("raw cookie leaked in output: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestRunCookieErrorRedactsSecret(t *testing.T) {
	const secretCookie = "secret-identity%3Dabc123"
	client := &fakeBandcampClient{
		setCookieErr: errors.New("rpc failed with " + secretCookie),
	}
	dialCalled := false
	var stdout, stderr bytes.Buffer

	code := run(context.Background(), []string{"cookie", "-cookie", secretCookie}, &stdout, &stderr, newTestDial(client, &dialCalled))
	if code == 0 {
		t.Fatalf("expected non-zero exit code on RPC error")
	}
	if !dialCalled {
		t.Fatalf("expected dial to be called")
	}
	if stderr.Len() == 0 {
		t.Fatalf("expected error message on stderr")
	}
	if strings.Contains(stdout.String(), secretCookie) || strings.Contains(stderr.String(), secretCookie) {
		t.Fatalf("raw cookie leaked in error output: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestRunExistingSubcommands(t *testing.T) {
	t.Run("lookup", func(t *testing.T) {
		client := &fakeBandcampClient{}
		dialCalled := false
		var stdout, stderr bytes.Buffer

		code := run(context.Background(), []string{"lookup", "-id", "42"}, &stdout, &stderr, newTestDial(client, &dialCalled))
		if code != 0 {
			t.Fatalf("expected exit code 0, got %d (stderr=%q)", code, stderr.String())
		}
		if !dialCalled || len(client.lookupReqs) != 1 || client.lookupReqs[0].GetBandcampId() != 42 {
			t.Fatalf("unexpected lookup state: dialCalled=%v reqs=%v", dialCalled, client.lookupReqs)
		}
	})

	t.Run("token", func(t *testing.T) {
		client := &fakeBandcampClient{}
		dialCalled := false
		var stdout, stderr bytes.Buffer

		code := run(context.Background(), []string{"token", "-token", "mytok"}, &stdout, &stderr, newTestDial(client, &dialCalled))
		if code != 0 {
			t.Fatalf("expected exit code 0, got %d (stderr=%q)", code, stderr.String())
		}
		if !dialCalled || len(client.setTokenReqs) != 1 || client.setTokenReqs[0].GetToken() != "mytok" {
			t.Fatalf("unexpected token state: dialCalled=%v reqs=%v", dialCalled, client.setTokenReqs)
		}
	})

	t.Run("mapping", func(t *testing.T) {
		client := &fakeBandcampClient{}
		dialCalled := false
		var stdout, stderr bytes.Buffer

		code := run(context.Background(), []string{"mapping", "-bandcamp", "10", "-discogs", "20"}, &stdout, &stderr, newTestDial(client, &dialCalled))
		if code != 0 {
			t.Fatalf("expected exit code 0, got %d (stderr=%q)", code, stderr.String())
		}
		if !dialCalled || len(client.addMappingReqs) != 1 {
			t.Fatalf("unexpected mapping state: dialCalled=%v reqs=%v", dialCalled, client.addMappingReqs)
		}
		if client.addMappingReqs[0].GetBandcampId() != 10 || client.addMappingReqs[0].GetDiscogsId() != 20 {
			t.Fatalf("unexpected mapping req: %+v", client.addMappingReqs[0])
		}
	})

	t.Run("reset", func(t *testing.T) {
		client := &fakeBandcampClient{}
		dialCalled := false
		var stdout, stderr bytes.Buffer

		code := run(context.Background(), []string{"reset"}, &stdout, &stderr, newTestDial(client, &dialCalled))
		if code != 0 {
			t.Fatalf("expected exit code 0, got %d (stderr=%q)", code, stderr.String())
		}
		if !dialCalled || !client.resetCalled {
			t.Fatalf("unexpected reset state: dialCalled=%v resetCalled=%v", dialCalled, client.resetCalled)
		}
	})
}
