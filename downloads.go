package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/brotherlogic/bandcamplib"
	bcpb "github.com/brotherlogic/bandcamplib/proto"
	pb "github.com/brotherlogic/bandcampserver/proto"
	cdpb "github.com/brotherlogic/cdprocessor/proto"
	rcgd "github.com/brotherlogic/godiscogs/proto"
	rcpb "github.com/brotherlogic/recordcollection/proto"
)

const (
	maxDownloadAttempts   int32 = 3
	downloadRetryCooldown       = 1 * time.Minute
	downloadPollInterval        = 30 * time.Second
	digitalGoalFolder     int32 = 1782105
)

// selectNextEligibleDownloadJob returns the lowest-ID eligible PENDING or
// SUBMITTED download job whose retry cooldown or poll interval has elapsed.
func selectNextEligibleDownloadJob(config *pb.Config, now time.Time) (int64, *pb.DownloadJob, bool) {
	if config == nil || len(config.GetPendingDownloads()) == 0 {
		return 0, nil, false
	}

	ids := make([]int64, 0, len(config.GetPendingDownloads()))
	for id := range config.GetPendingDownloads() {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	for _, id := range ids {
		job := config.GetPendingDownloads()[id]
		if job == nil {
			continue
		}
		switch job.GetState() {
		case pb.DownloadJobState_DOWNLOAD_JOB_STATE_PENDING:
			if job.GetLastAttempt() == 0 || now.Sub(time.Unix(job.GetLastAttempt(), 0)) >= downloadRetryCooldown {
				return id, job, true
			}
		case pb.DownloadJobState_DOWNLOAD_JOB_STATE_SUBMITTED:
			if job.GetLastAttempt() == 0 || now.Sub(time.Unix(job.GetLastAttempt(), 0)) >= downloadPollInterval {
				return id, job, true
			}
		default:
			continue
		}
	}

	return 0, nil, false
}

// ensureDownloadInstanceID guarantees job.InstanceId > 0 by looking up an
// existing digital collection record or adding one to recordcollection.
func (s *Server) ensureDownloadInstanceID(ctx context.Context, config *pb.Config, job *pb.DownloadJob) error {
	if config == nil || job == nil {
		return fmt.Errorf("config and download job must not be nil")
	}
	if job.GetInstanceId() > 0 {
		return nil
	}
	if job.GetDiscogsId() <= 0 {
		return fmt.Errorf("invalid discogs_id %d for download job", job.GetDiscogsId())
	}

	if config.AddedDate == nil {
		config.AddedDate = make(map[int32]int64)
	}

	now := s.nowTime()
	rc := s.getRCClient()

	iids, err := rc.QueryRecords(ctx, &rcpb.QueryRecordsRequest{
		Query: &rcpb.QueryRecordsRequest_ReleaseId{ReleaseId: job.GetDiscogsId()},
	})
	if err != nil {
		return err
	}

	for _, iid := range iids.GetInstanceIds() {
		if iid <= 0 {
			continue
		}
		rec, err := rc.GetRecord(ctx, &rcpb.GetRecordRequest{InstanceId: iid})
		if err != nil {
			return err
		}
		if rec.GetRecord().GetMetadata().GetGoalFolder() == digitalGoalFolder {
			job.InstanceId = iid
			if config.AddedDate[job.GetDiscogsId()] == 0 {
				config.AddedDate[job.GetDiscogsId()] = now.Unix()
			}
			return s.saveConfig(ctx, config)
		}
	}

	resp, err := rc.AddRecord(ctx, &rcpb.AddRecordRequest{
		ToAdd: &rcpb.Record{
			Release: &rcgd.Release{
				Id: job.GetDiscogsId(),
			},
			Metadata: &rcpb.ReleaseMetadata{
				Cost:           1,
				GoalFolder:     digitalGoalFolder,
				PurchaseBudget: fmt.Sprintf("float%v", now.Year()),
				FiledUnder:     rcpb.ReleaseMetadata_FILE_DIGITAL,
				DateArrived:    now.Unix(),
			},
		},
	})
	if err != nil {
		return err
	}

	instanceID := resp.GetAdded().GetRelease().GetInstanceId()
	if instanceID <= 0 {
		return fmt.Errorf("AddRecord returned invalid instance_id %d for discogs release %d", instanceID, job.GetDiscogsId())
	}

	job.InstanceId = instanceID
	if config.AddedDate[job.GetDiscogsId()] == 0 {
		config.AddedDate[job.GetDiscogsId()] = now.Unix()
	}
	return s.saveConfig(ctx, config)
}

func findBandcampItem(config *pb.Config, bandcampID int64) *bcpb.Item {
	for _, item := range config.GetItems() {
		if item != nil && item.GetAlbumId() == bandcampID {
			return item
		}
	}
	return nil
}

func (s *Server) findOrRefreshBandcampItem(ctx context.Context, config *pb.Config, bandcampID int64) (*bcpb.Item, error) {
	if item := findBandcampItem(config, bandcampID); item != nil {
		return item, nil
	}
	if config == nil || strings.TrimSpace(config.GetIdentityCookie()) == "" {
		return nil, bandcamplib.ErrAuthenticationRequired
	}

	initialCookie := config.GetIdentityCookie()
	var refreshedItems []*bcpb.Item
	err := s.withBandcampClient(ctx, config, func(client bandcampClient) error {
		var retrieveErr error
		refreshedItems, retrieveErr = client.RetrieveItems(ctx)
		return retrieveErr
	})
	if err != nil {
		return nil, sanitizeError(err, initialCookie, config.GetIdentityCookie())
	}

	config.Items = refreshedItems
	if err := s.saveConfig(ctx, config); err != nil {
		return nil, fmt.Errorf("persist refreshed Bandcamp items: %w", sanitizeError(err, initialCookie, config.GetIdentityCookie()))
	}

	if item := findBandcampItem(config, bandcampID); item != nil {
		return item, nil
	}
	return nil, bandcamplib.ErrDownloadUnavailable
}

func buildDownloadFailureAlert(config *pb.Config, bandcampID int64, job *pb.DownloadJob, reason, quarantinePath string, cookies ...string) (string, string) {
	item := findBandcampItem(config, bandcampID)

	title := fmt.Sprintf("Bandcamp WAV download failed for Bandcamp item [%d]", bandcampID)
	artist := "Unknown Artist"
	album := "Unknown Album"
	if item != nil {
		if b := strings.TrimSpace(item.GetBandName()); b != "" {
			artist = b
		}
		if a := strings.TrimSpace(item.GetAlbumTitle()); a != "" {
			album = a
		}
		title = fmt.Sprintf("Bandcamp WAV download failed for %s - %s [%d]", artist, album, bandcampID)
	}

	safeReason := strings.TrimSpace(redactCookie(reason, cookies...))
	if safeReason == "" {
		safeReason = "Unknown download error"
	}
	safeQuarantine := strings.TrimSpace(redactCookie(quarantinePath, cookies...))

	var b strings.Builder
	fmt.Fprintf(&b, "Bandcamp WAV download failed.\n\n")
	fmt.Fprintf(&b, "- **Artist:** %s\n", artist)
	fmt.Fprintf(&b, "- **Album:** %s\n", album)
	fmt.Fprintf(&b, "- **Bandcamp ID:** %d\n", bandcampID)
	fmt.Fprintf(&b, "- **Discogs Release:** https://www.discogs.com/release/%d\n", job.GetDiscogsId())
	fmt.Fprintf(&b, "- **Failure Reason:** %s\n", safeReason)
	if safeQuarantine != "" {
		fmt.Fprintf(&b, "- **Quarantine Path:** `%s`\n", safeQuarantine)
	}
	fmt.Fprintf(&b, "\nTo retry manually once resolved:\n\n```sh\nbandcampserver_cli download -bandcamp %d\n```\n", bandcampID)

	return redactCookie(title, cookies...), redactCookie(b.String(), cookies...)
}

func (s *Server) ensureDownloadFailureAlert(ctx context.Context, config *pb.Config, bandcampID int64, job *pb.DownloadJob, reason, quarantinePath string, cookies ...string) error {
	if job == nil || job.GetAlertIssueId() > 0 {
		return nil
	}

	allCookies := append([]string{}, cookies...)
	if config != nil {
		allCookies = append(allCookies, config.GetIdentityCookie())
	}

	title, body := buildDownloadFailureAlert(config, bandcampID, job, reason, quarantinePath, allCookies...)
	issue, err := s.getIssueManager().ImmediateIssue(ctx, title, body, false, false)
	if err != nil {
		return fmt.Errorf("create download failure alert issue: %w", sanitizeError(err, allCookies...))
	}

	job.AlertIssueId = issue.GetNumber()
	return nil
}

func (s *Server) closeDownloadAlert(ctx context.Context, config *pb.Config, job *pb.DownloadJob, cookies ...string) error {
	if job == nil || job.GetAlertIssueId() <= 0 {
		return nil
	}

	allCookies := append([]string{}, cookies...)
	if config != nil {
		allCookies = append(allCookies, config.GetIdentityCookie())
	}

	err := s.getIssueManager().DeleteIssue(ctx, job.GetAlertIssueId())
	if err != nil && !isIssueAlreadyClosedOrNotFound(err) {
		return fmt.Errorf("delete download failure alert issue: %w", sanitizeError(err, allCookies...))
	}

	job.AlertIssueId = 0
	return nil
}

func (s *Server) completeDownloadJob(ctx context.Context, config *pb.Config, bandcampID int64, job *pb.DownloadJob, outcome string, cookies ...string) error {
	if err := s.closeDownloadAlert(ctx, config, job, cookies...); err != nil {
		s.log(ctx, fmt.Sprintf("Failed to close download alert for %d: %v", bandcampID, sanitizeError(err, cookies...)))
	}
	delete(config.PendingDownloads, bandcampID)
	downloadTransitionsTotal.WithLabelValues(outcome).Inc()
	updateDownloadMetrics(config)
	return sanitizeError(s.saveConfig(ctx, config), cookies...)
}

func (s *Server) recordRetryableDownloadFailure(ctx context.Context, config *pb.Config, bandcampID int64, job *pb.DownloadJob, now time.Time, reason string, cookies ...string) error {
	safeReason := strings.TrimSpace(redactCookie(reason, cookies...))
	if safeReason == "" {
		safeReason = "unknown download error"
	}

	job.Attempts++
	job.LastAttempt = now.Unix()
	job.LastError = safeReason

	if job.GetAttempts() >= maxDownloadAttempts {
		job.State = pb.DownloadJobState_DOWNLOAD_JOB_STATE_FAILED
		if err := s.ensureDownloadFailureAlert(ctx, config, bandcampID, job, safeReason, "", cookies...); err != nil {
			s.log(ctx, fmt.Sprintf("Failed to ensure download failure alert for %d: %v", bandcampID, sanitizeError(err, cookies...)))
		}
		downloadTransitionsTotal.WithLabelValues("failed_exhausted").Inc()
	} else {
		job.State = pb.DownloadJobState_DOWNLOAD_JOB_STATE_PENDING
		downloadTransitionsTotal.WithLabelValues("retryable_error").Inc()
	}

	updateDownloadMetrics(config)
	return sanitizeError(s.saveConfig(ctx, config), cookies...)
}

func (s *Server) recordPermanentDownloadFailure(ctx context.Context, config *pb.Config, bandcampID int64, job *pb.DownloadJob, now time.Time, reason, quarantinePath string, cookies ...string) error {
	safeReason := strings.TrimSpace(redactCookie(reason, cookies...))
	if safeReason == "" {
		safeReason = "permanent download failure"
	}
	safeQuarantine := strings.TrimSpace(redactCookie(quarantinePath, cookies...))

	job.State = pb.DownloadJobState_DOWNLOAD_JOB_STATE_FAILED
	job.LastAttempt = now.Unix()
	job.LastError = safeReason

	if err := s.ensureDownloadFailureAlert(ctx, config, bandcampID, job, safeReason, safeQuarantine, cookies...); err != nil {
		s.log(ctx, fmt.Sprintf("Failed to ensure permanent download failure alert for %d: %v", bandcampID, sanitizeError(err, cookies...)))
	}
	downloadTransitionsTotal.WithLabelValues("permanent_error").Inc()
	updateDownloadMetrics(config)
	return sanitizeError(s.saveConfig(ctx, config), cookies...)
}

func (s *Server) handleDownloadAuthPause(ctx context.Context, config *pb.Config, bandcampID int64, err error, cookies ...string) error {
	reason := redactCookie(err.Error(), cookies...)
	if alertErr := s.ensureAuthAlert(ctx, config, reason); alertErr != nil {
		s.log(ctx, fmt.Sprintf("Failed to ensure Bandcamp auth alert for download %d: %v", bandcampID, sanitizeError(alertErr, cookies...)))
	}
	downloadTransitionsTotal.WithLabelValues("auth_paused").Inc()
	s.log(ctx, fmt.Sprintf("Download paused pending Bandcamp auth for %d: %s", bandcampID, reason))
	return nil
}

// processNextDownloadJob advances at most one eligible download job per call.
func (s *Server) processNextDownloadJob(ctx context.Context, config *pb.Config) error {
	now := s.nowTime()
	bandcampID, job, ok := selectNextEligibleDownloadJob(config, now)
	if !ok {
		return nil
	}

	seenCookies := []string{config.GetIdentityCookie()}
	trackCookies := func() []string {
		if current := config.GetIdentityCookie(); current != "" {
			seenCookies = append(seenCookies, current)
		}
		return seenCookies
	}

	switch job.GetState() {
	case pb.DownloadJobState_DOWNLOAD_JOB_STATE_PENDING:
		return s.dispatchPendingDownloadJob(ctx, config, bandcampID, job, now, trackCookies)
	case pb.DownloadJobState_DOWNLOAD_JOB_STATE_SUBMITTED:
		return s.pollSubmittedDownloadJob(ctx, config, bandcampID, job, now, trackCookies)
	default:
		return nil
	}
}

func (s *Server) dispatchPendingDownloadJob(ctx context.Context, config *pb.Config, bandcampID int64, job *pb.DownloadJob, now time.Time, trackCookies func() []string) error {
	if err := s.ensureDownloadInstanceID(ctx, config, job); err != nil {
		return s.recordRetryableDownloadFailure(ctx, config, bandcampID, job, now, err.Error(), trackCookies()...)
	}

	item, err := s.findOrRefreshBandcampItem(ctx, config, bandcampID)
	cookies := trackCookies()
	if err != nil {
		if errors.Is(err, bandcamplib.ErrAuthenticationRequired) || errors.Is(err, bandcamplib.ErrAuthenticationFailed) {
			return s.handleDownloadAuthPause(ctx, config, bandcampID, err, cookies...)
		}
		return s.recordRetryableDownloadFailure(ctx, config, bandcampID, job, now, err.Error(), cookies...)
	}

	wavURL, err := s.resolveWAVLinkForItem(ctx, config, item)
	cookies = trackCookies()
	if err != nil {
		if errors.Is(err, bandcamplib.ErrAuthenticationRequired) || errors.Is(err, bandcamplib.ErrAuthenticationFailed) {
			return s.handleDownloadAuthPause(ctx, config, bandcampID, err, cookies...)
		}
		return s.recordRetryableDownloadFailure(ctx, config, bandcampID, job, now, err.Error(), cookies...)
	}
	if strings.TrimSpace(wavURL) == "" {
		return s.recordRetryableDownloadFailure(ctx, config, bandcampID, job, now, bandcamplib.ErrDownloadUnavailable.Error(), cookies...)
	}

	if clearErr := s.clearAuthAlert(ctx, config); clearErr != nil {
		s.log(ctx, fmt.Sprintf("Failed to clear Bandcamp auth alert: %v", sanitizeError(clearErr, cookies...)))
	}

	resp, err := s.getCDProcessorClient().DownloadRelease(ctx, &cdpb.DownloadReleaseRequest{
		ReleaseId:   job.GetDiscogsId(),
		InstanceId:  job.GetInstanceId(),
		DownloadUrl: wavURL,
	})
	if err != nil {
		return s.recordRetryableDownloadFailure(ctx, config, bandcampID, job, now, err.Error(), cookies...)
	}

	switch resp.GetState() {
	case cdpb.DownloadState_DOWNLOAD_ALREADY_EXISTS:
		return s.completeDownloadJob(ctx, config, bandcampID, job, "already_exists", cookies...)
	case cdpb.DownloadState_DOWNLOAD_COMPLETED:
		return s.completeDownloadJob(ctx, config, bandcampID, job, "completed", cookies...)
	case cdpb.DownloadState_DOWNLOAD_QUEUED, cdpb.DownloadState_DOWNLOAD_IN_PROGRESS:
		job.State = pb.DownloadJobState_DOWNLOAD_JOB_STATE_SUBMITTED
		job.LastAttempt = now.Unix()
		job.LastError = ""
		downloadTransitionsTotal.WithLabelValues("submitted").Inc()
		updateDownloadMetrics(config)
		return sanitizeError(s.saveConfig(ctx, config), cookies...)
	case cdpb.DownloadState_DOWNLOAD_FAILED_PERMANENT:
		reason := resp.GetMessage()
		if strings.TrimSpace(reason) == "" {
			reason = "cdprocessor returned DOWNLOAD_FAILED_PERMANENT"
		}
		return s.recordPermanentDownloadFailure(ctx, config, bandcampID, job, now, reason, "", cookies...)
	case cdpb.DownloadState_DOWNLOAD_FAILED_RETRYABLE:
		reason := resp.GetMessage()
		if strings.TrimSpace(reason) == "" {
			reason = "cdprocessor returned DOWNLOAD_FAILED_RETRYABLE"
		}
		return s.recordRetryableDownloadFailure(ctx, config, bandcampID, job, now, reason, cookies...)
	default:
		reason := fmt.Sprintf("unexpected cdprocessor DownloadRelease state %v: %s", resp.GetState(), resp.GetMessage())
		return s.recordRetryableDownloadFailure(ctx, config, bandcampID, job, now, reason, cookies...)
	}
}

func (s *Server) pollSubmittedDownloadJob(ctx context.Context, config *pb.Config, bandcampID int64, job *pb.DownloadJob, now time.Time, trackCookies func() []string) error {
	cookies := trackCookies()
	resp, err := s.getCDProcessorClient().GetDownloadStatus(ctx, &cdpb.GetDownloadStatusRequest{
		ReleaseId: job.GetDiscogsId(),
	})
	if err != nil {
		return s.recordRetryableDownloadFailure(ctx, config, bandcampID, job, now, err.Error(), cookies...)
	}

	switch resp.GetState() {
	case cdpb.DownloadState_DOWNLOAD_QUEUED, cdpb.DownloadState_DOWNLOAD_IN_PROGRESS:
		job.LastAttempt = now.Unix()
		updateDownloadMetrics(config)
		return sanitizeError(s.saveConfig(ctx, config), cookies...)
	case cdpb.DownloadState_DOWNLOAD_COMPLETED:
		return s.completeDownloadJob(ctx, config, bandcampID, job, "completed", cookies...)
	case cdpb.DownloadState_DOWNLOAD_ALREADY_EXISTS:
		return s.completeDownloadJob(ctx, config, bandcampID, job, "already_exists", cookies...)
	case cdpb.DownloadState_DOWNLOAD_FAILED_PERMANENT:
		reason := resp.GetErrorMessage()
		if strings.TrimSpace(reason) == "" {
			reason = "cdprocessor returned DOWNLOAD_FAILED_PERMANENT"
		}
		return s.recordPermanentDownloadFailure(ctx, config, bandcampID, job, now, reason, resp.GetQuarantinePath(), cookies...)
	case cdpb.DownloadState_DOWNLOAD_FAILED_RETRYABLE, cdpb.DownloadState_DOWNLOAD_NOT_FOUND:
		reason := resp.GetErrorMessage()
		if strings.TrimSpace(reason) == "" {
			reason = fmt.Sprintf("cdprocessor returned %v", resp.GetState())
		}
		return s.recordRetryableDownloadFailure(ctx, config, bandcampID, job, now, reason, cookies...)
	default:
		reason := fmt.Sprintf("unexpected cdprocessor GetDownloadStatus state %v: %s", resp.GetState(), resp.GetErrorMessage())
		return s.recordRetryableDownloadFailure(ctx, config, bandcampID, job, now, reason, cookies...)
	}
}
