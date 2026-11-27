package main

import (
	"testing"

	"github.com/brotherlogic/bandcamplib"
	bcpb "github.com/brotherlogic/bandcamplib/proto"
	pb "github.com/brotherlogic/bandcampserver/proto"
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

func TestDownloadJobAndPendingDownloadsProtoRoundTrip(t *testing.T) {
	cfg := &pb.Config{
		DownloadLinkFailures: map[int64]int32{
			101: 2,
		},
		PendingDownloads: map[int64]*pb.DownloadJob{
			1000: {
				DiscogsId:    11111,
				InstanceId:   22222,
				State:        pb.DownloadJobState_DOWNLOAD_JOB_STATE_UNSPECIFIED,
				Attempts:     0,
				LastAttempt:  0,
				AlertIssueId: 0,
				LastError:    "",
			},
			1001: {
				DiscogsId:    33333,
				InstanceId:   44444,
				State:        pb.DownloadJobState_DOWNLOAD_JOB_STATE_PENDING,
				Attempts:     1,
				LastAttempt:  1700000100,
				AlertIssueId: 5001,
				LastError:    "temporary bandcamp timeout",
			},
			1002: {
				DiscogsId:    55555,
				InstanceId:   66666,
				State:        pb.DownloadJobState_DOWNLOAD_JOB_STATE_SUBMITTED,
				Attempts:     2,
				LastAttempt:  1700000200,
				AlertIssueId: 5002,
				LastError:    "previous retryable error",
			},
			1003: {
				DiscogsId:    77777,
				InstanceId:   88888,
				State:        pb.DownloadJobState_DOWNLOAD_JOB_STATE_FAILED,
				Attempts:     3,
				LastAttempt:  1700000300,
				AlertIssueId: 5003,
				LastError:    "permanent track count mismatch",
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

	if got.GetDownloadLinkFailures()[101] != 2 {
		t.Errorf("DownloadLinkFailures[101] = %d, want 2", got.GetDownloadLinkFailures()[101])
	}

	if len(got.GetPendingDownloads()) != len(cfg.GetPendingDownloads()) {
		t.Fatalf("len(PendingDownloads) = %d, want %d", len(got.GetPendingDownloads()), len(cfg.GetPendingDownloads()))
	}

	for id, want := range cfg.GetPendingDownloads() {
		job, ok := got.GetPendingDownloads()[id]
		if !ok {
			t.Errorf("PendingDownloads[%d] missing after round-trip", id)
			continue
		}
		if job.GetDiscogsId() != want.GetDiscogsId() {
			t.Errorf("PendingDownloads[%d].DiscogsId = %d, want %d", id, job.GetDiscogsId(), want.GetDiscogsId())
		}
		if job.GetInstanceId() != want.GetInstanceId() {
			t.Errorf("PendingDownloads[%d].InstanceId = %d, want %d", id, job.GetInstanceId(), want.GetInstanceId())
		}
		if job.GetState() != want.GetState() {
			t.Errorf("PendingDownloads[%d].State = %v, want %v", id, job.GetState(), want.GetState())
		}
		if job.GetAttempts() != want.GetAttempts() {
			t.Errorf("PendingDownloads[%d].Attempts = %d, want %d", id, job.GetAttempts(), want.GetAttempts())
		}
		if job.GetLastAttempt() != want.GetLastAttempt() {
			t.Errorf("PendingDownloads[%d].LastAttempt = %d, want %d", id, job.GetLastAttempt(), want.GetLastAttempt())
		}
		if job.GetAlertIssueId() != want.GetAlertIssueId() {
			t.Errorf("PendingDownloads[%d].AlertIssueId = %d, want %d", id, job.GetAlertIssueId(), want.GetAlertIssueId())
		}
		if job.GetLastError() != want.GetLastError() {
			t.Errorf("PendingDownloads[%d].LastError = %q, want %q", id, job.GetLastError(), want.GetLastError())
		}
	}
}

func TestTriggerDownloadProtoRoundTrip(t *testing.T) {
	req := &pb.TriggerDownloadRequest{
		BandcampId: 12345,
		DiscogsId:  67890,
	}
	reqBytes, err := proto.Marshal(req)
	if err != nil {
		t.Fatalf("proto.Marshal(TriggerDownloadRequest) failed: %v", err)
	}
	var gotReq pb.TriggerDownloadRequest
	if err := proto.Unmarshal(reqBytes, &gotReq); err != nil {
		t.Fatalf("proto.Unmarshal(TriggerDownloadRequest) failed: %v", err)
	}
	if gotReq.GetBandcampId() != req.GetBandcampId() {
		t.Errorf("TriggerDownloadRequest.BandcampId = %d, want %d", gotReq.GetBandcampId(), req.GetBandcampId())
	}
	if gotReq.GetDiscogsId() != req.GetDiscogsId() {
		t.Errorf("TriggerDownloadRequest.DiscogsId = %d, want %d", gotReq.GetDiscogsId(), req.GetDiscogsId())
	}

	resp := &pb.TriggerDownloadResponse{
		BandcampId:    12345,
		DiscogsId:     67890,
		State:         pb.DownloadJobState_DOWNLOAD_JOB_STATE_PENDING,
		AlreadyActive: true,
		Message:       "download already queued",
	}
	respBytes, err := proto.Marshal(resp)
	if err != nil {
		t.Fatalf("proto.Marshal(TriggerDownloadResponse) failed: %v", err)
	}
	var gotResp pb.TriggerDownloadResponse
	if err := proto.Unmarshal(respBytes, &gotResp); err != nil {
		t.Fatalf("proto.Unmarshal(TriggerDownloadResponse) failed: %v", err)
	}
	if gotResp.GetBandcampId() != resp.GetBandcampId() {
		t.Errorf("TriggerDownloadResponse.BandcampId = %d, want %d", gotResp.GetBandcampId(), resp.GetBandcampId())
	}
	if gotResp.GetDiscogsId() != resp.GetDiscogsId() {
		t.Errorf("TriggerDownloadResponse.DiscogsId = %d, want %d", gotResp.GetDiscogsId(), resp.GetDiscogsId())
	}
	if gotResp.GetState() != resp.GetState() {
		t.Errorf("TriggerDownloadResponse.State = %v, want %v", gotResp.GetState(), resp.GetState())
	}
	if gotResp.GetAlreadyActive() != resp.GetAlreadyActive() {
		t.Errorf("TriggerDownloadResponse.AlreadyActive = %v, want %v", gotResp.GetAlreadyActive(), resp.GetAlreadyActive())
	}
	if gotResp.GetMessage() != resp.GetMessage() {
		t.Errorf("TriggerDownloadResponse.Message = %q, want %q", gotResp.GetMessage(), resp.GetMessage())
	}
}

func TestBandcampServerServiceInterface(t *testing.T) {
	var _ pb.BandcampServerServiceServer = (*Server)(nil)
}
