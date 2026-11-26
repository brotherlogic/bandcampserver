package main

import (
	"context"
	"testing"

	"github.com/brotherlogic/bandcamplib"
	bcpb "github.com/brotherlogic/bandcamplib/proto"
	pb "github.com/brotherlogic/bandcampserver/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestConfigAndSetCookieProtoRoundTrip(t *testing.T) {
	cfg := &pb.Config{
		IdentityCookie:    "test-cookie-value",
		LastCookieRefresh: 1700000123,
		DownloadLinkFailures: map[int64]int32{
			101: 2,
			202: 1,
		},
		AuthAlertIssueId: 4999,
		Items: []*bcpb.Item{
			{
				BandName:      "Artist",
				AlbumTitle:    "Album",
				AlbumId:       101,
				RedownloadUrl: "https://bandcamp.com/download?id=101",
			},
		},
	}

	data, err := proto.Marshal(cfg)
	if err != nil {
		t.Fatalf("proto.Marshal(Config) failed: %v", err)
	}

	var got pb.Config
	if err := proto.Unmarshal(data, &got); err != nil {
		t.Fatalf("proto.Unmarshal(Config) failed: %v", err)
	}

	if got.GetIdentityCookie() != cfg.GetIdentityCookie() {
		t.Errorf("IdentityCookie = %q, want %q", got.GetIdentityCookie(), cfg.GetIdentityCookie())
	}
	if got.GetLastCookieRefresh() != cfg.GetLastCookieRefresh() {
		t.Errorf("LastCookieRefresh = %d, want %d", got.GetLastCookieRefresh(), cfg.GetLastCookieRefresh())
	}
	if got.GetAuthAlertIssueId() != cfg.GetAuthAlertIssueId() {
		t.Errorf("AuthAlertIssueId = %d, want %d", got.GetAuthAlertIssueId(), cfg.GetAuthAlertIssueId())
	}
	if len(got.GetDownloadLinkFailures()) != 2 || got.GetDownloadLinkFailures()[101] != 2 || got.GetDownloadLinkFailures()[202] != 1 {
		t.Errorf("DownloadLinkFailures = %v, want %v", got.GetDownloadLinkFailures(), cfg.GetDownloadLinkFailures())
	}
	if len(got.GetItems()) != 1 || got.GetItems()[0].GetRedownloadUrl() != "https://bandcamp.com/download?id=101" {
		t.Errorf("Items[0].GetRedownloadUrl() = %v, want %q", got.GetItems(), "https://bandcamp.com/download?id=101")
	}

	req := &pb.SetCookieRequest{Cookie: "cookie-123"}
	reqBytes, err := proto.Marshal(req)
	if err != nil {
		t.Fatalf("proto.Marshal(SetCookieRequest) failed: %v", err)
	}
	var gotReq pb.SetCookieRequest
	if err := proto.Unmarshal(reqBytes, &gotReq); err != nil {
		t.Fatalf("proto.Unmarshal(SetCookieRequest) failed: %v", err)
	}
	if gotReq.GetCookie() != "cookie-123" {
		t.Errorf("SetCookieRequest.GetCookie() = %q, want %q", gotReq.GetCookie(), "cookie-123")
	}

	resp := &pb.SetCookieResponse{}
	respBytes, err := proto.Marshal(resp)
	if err != nil {
		t.Fatalf("proto.Marshal(SetCookieResponse) failed: %v", err)
	}
	var gotResp pb.SetCookieResponse
	if err := proto.Unmarshal(respBytes, &gotResp); err != nil {
		t.Fatalf("proto.Unmarshal(SetCookieResponse) failed: %v", err)
	}
}

func TestBandcamplibSymbolsAccessible(t *testing.T) {
	if bandcamplib.FormatWAV == "" {
		t.Fatal("bandcamplib.FormatWAV is empty")
	}

	client, err := bandcamplib.NewClient(
		&bcpb.Config{User: 1, IdentityCookie: "abc"},
		bandcamplib.WithCookieRefreshCallback(func(string) {}),
	)
	if err != nil {
		t.Fatalf("bandcamplib.NewClient failed: %v", err)
	}
	if client == nil {
		t.Fatal("bandcamplib.NewClient returned nil client")
	}
	_ = client.RetrieveItems
	_ = client.GetDownloadURLForRedownloadURL

	sentinels := []error{
		bandcamplib.ErrAuthenticationRequired,
		bandcamplib.ErrAuthenticationFailed,
		bandcamplib.ErrDownloadUnavailable,
		bandcamplib.ErrFormatUnavailable,
		bandcamplib.ErrDownloadTimeout,
		bandcamplib.ErrNotFound,
	}
	for i, sentinel := range sentinels {
		if sentinel == nil {
			t.Errorf("sentinel error at index %d is nil", i)
		}
	}
}

func TestSetCookieStubUnimplemented(t *testing.T) {
	var _ pb.BandcampServerServiceServer = (*Server)(nil)

	_, err := (&Server{}).SetCookie(context.Background(), &pb.SetCookieRequest{Cookie: "val"})
	if status.Code(err) != codes.Unimplemented {
		t.Errorf("SetCookie status code = %v, want %v (err=%v)", status.Code(err), codes.Unimplemented, err)
	}
}
