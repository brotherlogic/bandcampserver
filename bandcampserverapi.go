package main

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/brotherlogic/bandcamplib"
	bcpb "github.com/brotherlogic/bandcamplib/proto"

	pb "github.com/brotherlogic/bandcampserver/proto"
	rcgd "github.com/brotherlogic/godiscogs/proto"
	rcpb "github.com/brotherlogic/recordcollection/proto"
)

func (s *Server) Lookup(ctx context.Context, req *pb.LookupRequest) (*pb.LookupResponse, error) {
	config, err := s.loadConfig(ctx)
	if err != nil {
		return nil, err
	}

	for _, item := range config.Items {
		if item.AlbumId == req.GetBandcampId() {
			return &pb.LookupResponse{Bandcamp: item}, nil
		}
	}

	return nil, status.Errorf(codes.NotFound, "Could not find %v", req)
}

// ClientUpdate on an updated record
func (s *Server) ClientUpdate(ctx context.Context, req *rcpb.ClientUpdateRequest) (*rcpb.ClientUpdateResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	config, err := s.loadConfig(ctx)
	if err != nil {
		return nil, err
	}

	today := s.metrics(ctx, config)

	rc := s.getRCClient()
	issues := s.getIssueManager()

	record, err := rc.GetRecord(ctx, &rcpb.GetRecordRequest{InstanceId: req.GetInstanceId()})
	if err != nil {
		if status.Code(err) != codes.OutOfRange {
			return nil, err
		}
		return &rcpb.ClientUpdateResponse{}, nil
	}

	// Validate the config on each successful pass
	err = s.validate(ctx, config)
	if err != nil {
		return nil, err
	}

	// Check that this record is correct from bandcamp point of view
	if record.GetRecord().GetRelease().GetFolderId() == 1782105 {
		found := false
		for _, format := range record.GetRecord().GetRelease().GetFormats() {
			if format.GetName() == "File" {
				found = true
			}
		}

		if !found {
			// This can be deleted?
			issues.RaiseIssue("Can we delete", fmt.Sprintf("https://www.discogs.com/madeup/release/%v", record.GetRecord().GetRelease().GetId()))
		}
	}

	s.log(ctx, fmt.Sprintf("Running proc with %v done today", today))

	// Phase 1: Sync newly mapped items to recordcollection and capture instance_id.
	for _, item := range config.Items {
		if val, ok := config.GetMapping()[item.GetAlbumId()]; ok && val > 0 && config.AddedDate[val] == 0 {
			iids, err := rc.QueryRecords(ctx, &rcpb.QueryRecordsRequest{Query: &rcpb.QueryRecordsRequest_ReleaseId{ReleaseId: val}})
			if err != nil {
				return nil, err
			}
			for _, iid := range iids.GetInstanceIds() {
				rec, err := rc.GetRecord(ctx, &rcpb.GetRecordRequest{InstanceId: iid})
				if err != nil {
					return nil, err
				}

				if rec.GetRecord().GetMetadata().GetGoalFolder() == 1782105 {
					config.AddedDate[val] = s.nowTime().Unix()
					if job := config.PendingDownloads[item.GetAlbumId()]; job != nil && iid > 0 {
						job.InstanceId = iid
					}
					return &rcpb.ClientUpdateResponse{}, s.saveConfig(ctx, config)
				}
			}

			if config.AddedDate[val] > 0 {
				issues.RaiseIssue("Bad add", fmt.Sprintf("We've already added %v from %v at %v", val, item, config.AddedDate[val]))
				return nil, fmt.Errorf("Oveerlap issue")
			}
			addResp, err := rc.AddRecord(ctx, &rcpb.AddRecordRequest{
				ToAdd: &rcpb.Record{
					Release: &rcgd.Release{
						Id: val,
					},
					Metadata: &rcpb.ReleaseMetadata{
						Cost:           1,
						GoalFolder:     1782105,
						PurchaseBudget: fmt.Sprintf("float%v", s.nowTime().Year()),
						FiledUnder:     rcpb.ReleaseMetadata_FILE_DIGITAL,
						DateArrived:    s.nowTime().Unix(),
					},
				},
			})

			if err != nil {
				return nil, err
			}

			addedID := addResp.GetAdded().GetRelease().GetInstanceId()
			if addedID <= 0 {
				return nil, fmt.Errorf("AddRecord returned invalid instance_id %d for discogs release %d", addedID, val)
			}

			config.AddedDate[val] = s.nowTime().Unix()
			if job := config.PendingDownloads[item.GetAlbumId()]; job != nil {
				job.InstanceId = addedID
			}
			return &rcpb.ClientUpdateResponse{}, s.saveConfig(ctx, config)
		}
	}

	// Phase 2: Advance pending/submitted download jobs so unmapped items never starve downloads.
	if err := s.processNextDownloadJob(ctx, config); err != nil {
		return nil, err
	}

	// Phase 3: Open at most one missing-mapping issue only when no unmapped item already has an open issue.
	hasOpenMissingMappingIssue := false
	for _, item := range config.Items {
		albumID := item.GetAlbumId()
		if _, ok := config.GetMapping()[albumID]; !ok && config.IssueIds[albumID] != 0 {
			hasOpenMissingMappingIssue = true
			break
		}
	}

	if !hasOpenMissingMappingIssue {
		for _, item := range config.Items {
			albumID := item.GetAlbumId()
			if _, ok := config.GetMapping()[albumID]; ok {
				continue
			}
			// Skip processing when we've done 30 in a day or are within the 2-minute cooldown.
			if s.nowTime().Sub(time.Unix(config.GetLastProcess(), 0)) < time.Minute*2 || today >= 30 {
				s.log(ctx, fmt.Sprintf("We are Waiting for %v or %v", today, s.nowTime().Sub(time.Unix(config.GetLastProcess(), 0))))
				break
			}
			s.log(ctx, fmt.Sprintf("%v is missing a mapping -> %v - %v (%v)", albumID, item.GetBandName(), item.GetAlbumTitle(), today))

			cookie := config.GetIdentityCookie()
			title := fmt.Sprintf("Missing Discogs mapping for %v - %v [%v]", item.GetBandName(), item.GetAlbumTitle(), albumID)
			candidates := s.lookupDiscogsCandidates(ctx, rc, item, cookie)
			body, bcURLOutcome := buildMissingMappingIssueBodyWithOutcome(item, candidates, cookie)
			issue, issueErr := issues.ImmediateIssue(ctx, title, body, false, false)
			if issueErr != nil {
				return nil, sanitizeError(issueErr, cookie)
			}
			missingMappingBandcampURLTotal.WithLabelValues(string(bcURLOutcome)).Inc()
			config.IssueIds[albumID] = issue.GetNumber()
			config.LastProcess = s.nowTime().Unix()
			return &rcpb.ClientUpdateResponse{}, sanitizeError(s.saveConfig(ctx, config), cookie)
		}
	}

	return &rcpb.ClientUpdateResponse{}, nil
}

func (s *Server) SetToken(ctx context.Context, req *pb.SetTokenRequest) (*pb.SetTokenResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	config, err := s.loadConfig(ctx)
	if err != nil {
		return nil, err
	}

	config.Token = req.GetToken()
	config.LastTokenRefresh = s.nowTime().Unix()

	retrieveUnauthenticated := func() ([]*bcpb.Item, error) {
		client, err := s.getBandcampClientFactory()(&bcpb.Config{
			User:  bandcampUserID,
			Token: config.GetToken(),
		}, nil)
		if err != nil {
			return nil, err
		}
		return client.RetrieveItems(ctx)
	}

	var items []*bcpb.Item
	if strings.TrimSpace(config.GetIdentityCookie()) != "" {
		authErr := s.withBandcampClient(ctx, config, func(client bandcampClient) error {
			var retrieveErr error
			items, retrieveErr = client.RetrieveItems(ctx)
			return retrieveErr
		})
		if authErr == nil {
			if clearErr := s.clearAuthAlert(ctx, config); clearErr != nil {
				s.log(ctx, fmt.Sprintf("Failed to clear Bandcamp auth alert: %v", sanitizeError(clearErr, config.GetIdentityCookie())))
			}
		} else if errors.Is(authErr, bandcamplib.ErrAuthenticationFailed) {
			if alertErr := s.ensureAuthAlert(ctx, config, "Bandcamp session cookie expired while refreshing items."); alertErr != nil {
				s.log(ctx, fmt.Sprintf("Failed to ensure Bandcamp auth alert: %v", sanitizeError(alertErr, config.GetIdentityCookie())))
			}
			s.log(ctx, fmt.Sprintf("Bandcamp cookie auth failed during SetToken, falling back to unauthenticated retrieval: %v", sanitizeError(authErr, config.GetIdentityCookie())))
			items, err = retrieveUnauthenticated()
			if err != nil {
				return nil, sanitizeError(err, config.GetIdentityCookie())
			}
		} else {
			return nil, sanitizeError(authErr, config.GetIdentityCookie())
		}
	} else {
		items, err = retrieveUnauthenticated()
		if err != nil {
			return nil, err
		}
	}

	s.log(ctx, fmt.Sprintf("Read %v items", len(items)))

	config.Items = items
	updateAuthMetrics(config)

	return &pb.SetTokenResponse{}, sanitizeError(s.saveConfig(ctx, config), config.GetIdentityCookie())
}

func (s *Server) AddMapping(ctx context.Context, req *pb.AddMappingRequest) (*pb.AddMappingResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	config, err := s.loadConfig(ctx)
	if err != nil {
		return nil, err
	}

	bcID := req.GetBandcampId()
	discogsID := req.GetDiscogsId()

	config.Mapping[bcID] = discogsID
	delete(config.DownloadLinkFailures, bcID)

	if config.IssueIds[bcID] > 0 {
		err := s.getIssueManager().DeleteIssue(ctx, config.IssueIds[bcID])
		if err != nil && !isIssueAlreadyClosedOrNotFound(err) {
			return nil, sanitizeError(fmt.Errorf("unable to remove issue: %w", err), config.GetIdentityCookie())
		}
		delete(config.IssueIds, bcID)
	}

	if discogsID > 0 {
		if existing := config.PendingDownloads[bcID]; existing != nil {
			if err := s.closeDownloadAlert(ctx, config, existing); err != nil {
				s.log(ctx, fmt.Sprintf("Failed to close download alert for %d: %v", bcID, sanitizeError(err, config.GetIdentityCookie())))
			}
		}
		config.PendingDownloads[bcID] = &pb.DownloadJob{
			DiscogsId:    discogsID,
			State:        pb.DownloadJobState_DOWNLOAD_JOB_STATE_PENDING,
			AlertIssueId: 0,
		}
	} else if job, ok := config.PendingDownloads[bcID]; ok {
		if job.GetAlertIssueId() > 0 {
			if err := s.getIssueManager().DeleteIssue(ctx, job.GetAlertIssueId()); err != nil && !isIssueAlreadyClosedOrNotFound(err) {
				return nil, sanitizeError(fmt.Errorf("unable to remove download alert issue: %w", err), config.GetIdentityCookie())
			}
		}
		delete(config.PendingDownloads, bcID)
	}

	config.LastProcess = s.nowTime().Unix()
	updateDownloadMetrics(config)

	return &pb.AddMappingResponse{}, sanitizeError(s.saveConfig(ctx, config), config.GetIdentityCookie())
}

func (s *Server) Reset(ctx context.Context, req *pb.ResetRequest) (*pb.ResetResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	config, err := s.loadConfig(ctx)
	if err != nil {
		return nil, err
	}

	config.IssueIds = make(map[int64]int32)

	return &pb.ResetResponse{}, s.saveConfig(ctx, config)
}

func (s *Server) SetCookie(ctx context.Context, req *pb.SetCookieRequest) (*pb.SetCookieResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	normalizedCookie, err := normalizeCookieInput(req.GetCookie())
	if err != nil {
		return nil, err
	}

	config, err := s.loadConfig(ctx)
	if err != nil {
		return nil, err
	}

	previousCookie := config.GetIdentityCookie()
	candidateCfg := &pb.Config{
		Token:          config.GetToken(),
		IdentityCookie: normalizedCookie,
	}
	tracker, err := s.newTrackedBandcampClient(candidateCfg)
	if err != nil {
		return nil, sanitizeError(err, req.GetCookie(), normalizedCookie, previousCookie)
	}

	items, err := tracker.client.RetrieveItems(ctx)
	refreshed := tracker.detectRefreshedCookie(normalizedCookie)
	if err != nil {
		return nil, sanitizeError(err, req.GetCookie(), normalizedCookie, refreshed, previousCookie)
	}

	effectiveCookie := normalizedCookie
	if refreshed != "" {
		effectiveCookie = refreshed
	}
	config.IdentityCookie = effectiveCookie
	config.LastCookieRefresh = s.nowTime().Unix()
	config.Items = items
	updateAuthMetrics(config)

	clearErr := s.clearAuthAlert(ctx, config)
	if err := s.saveConfig(ctx, config); err != nil {
		return nil, sanitizeError(err, req.GetCookie(), normalizedCookie, effectiveCookie, previousCookie)
	}
	if clearErr != nil {
		return nil, sanitizeError(clearErr, req.GetCookie(), normalizedCookie, effectiveCookie, previousCookie)
	}

	return &pb.SetCookieResponse{}, nil
}

func (s *Server) TriggerDownload(ctx context.Context, req *pb.TriggerDownloadRequest) (*pb.TriggerDownloadResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	reqBC := req.GetBandcampId()
	reqDiscogs := req.GetDiscogsId()
	if reqBC < 0 || reqDiscogs < 0 {
		return nil, status.Error(codes.InvalidArgument, "bandcamp_id and discogs_id must be non-negative")
	}
	if (reqBC == 0 && reqDiscogs == 0) || (reqBC != 0 && reqDiscogs != 0) {
		return nil, status.Error(codes.InvalidArgument, "exactly one of bandcamp_id or discogs_id must be specified")
	}

	config, err := s.loadConfig(ctx)
	if err != nil {
		return nil, err
	}

	var bandcampID int64
	var discogsID int32
	if reqBC > 0 {
		val, ok := config.Mapping[reqBC]
		if !ok {
			return nil, status.Errorf(codes.NotFound, "bandcamp_id %d is not mapped", reqBC)
		}
		if val <= 0 {
			return nil, status.Errorf(codes.FailedPrecondition, "bandcamp_id %d is skipped/ignored (discogs_id=%d)", reqBC, val)
		}
		bandcampID = reqBC
		discogsID = val
	} else {
		var matches []int64
		for bcID, val := range config.Mapping {
			if val == reqDiscogs {
				matches = append(matches, bcID)
			}
		}
		if len(matches) == 0 {
			return nil, status.Errorf(codes.NotFound, "discogs_id %d is not mapped", reqDiscogs)
		}
		if len(matches) > 1 {
			sort.Slice(matches, func(i, j int) bool { return matches[i] < matches[j] })
			return nil, status.Errorf(codes.FailedPrecondition, "discogs_id %d maps to multiple bandcamp items (%v); retry using -bandcamp <id>", reqDiscogs, matches)
		}
		bandcampID = matches[0]
		discogsID = reqDiscogs
	}

	existing := config.PendingDownloads[bandcampID]
	if existing != nil && (existing.GetState() == pb.DownloadJobState_DOWNLOAD_JOB_STATE_PENDING || existing.GetState() == pb.DownloadJobState_DOWNLOAD_JOB_STATE_SUBMITTED) {
		return &pb.TriggerDownloadResponse{
			BandcampId:    bandcampID,
			DiscogsId:     discogsID,
			State:         existing.GetState(),
			AlreadyActive: true,
			Message:       fmt.Sprintf("download job for bandcamp_id %d (discogs_id %d) is already %s", bandcampID, discogsID, existing.GetState().String()),
		}, nil
	}

	var instanceID int64
	if existing != nil {
		if err := s.closeDownloadAlert(ctx, config, existing); err != nil {
			s.log(ctx, fmt.Sprintf("Failed to close download alert for %d: %v", bandcampID, sanitizeError(err, config.GetIdentityCookie())))
		}
		if existing.GetDiscogsId() == discogsID {
			instanceID = existing.GetInstanceId()
		}
	}

	config.PendingDownloads[bandcampID] = &pb.DownloadJob{
		DiscogsId:    discogsID,
		InstanceId:   instanceID,
		State:        pb.DownloadJobState_DOWNLOAD_JOB_STATE_PENDING,
		Attempts:     0,
		LastAttempt:  0,
		LastError:    "",
		AlertIssueId: 0,
	}
	updateDownloadMetrics(config)

	if err := s.saveConfig(ctx, config); err != nil {
		return nil, sanitizeError(err, config.GetIdentityCookie())
	}

	return &pb.TriggerDownloadResponse{
		BandcampId:    bandcampID,
		DiscogsId:     discogsID,
		State:         pb.DownloadJobState_DOWNLOAD_JOB_STATE_PENDING,
		AlreadyActive: false,
		Message:       fmt.Sprintf("enqueued download job for bandcamp_id %d (discogs_id %d)", bandcampID, discogsID),
	}, nil
}
