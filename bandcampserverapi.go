package main

import (
	"errors"
	"fmt"
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

const maxDownloadLinkFailures int32 = 3

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
	for _, item := range config.Items {
		if val, ok := config.GetMapping()[item.GetAlbumId()]; !ok {
			// Skip processing when we've done 20 in a day
			if s.nowTime().Sub(time.Unix(config.GetLastProcess(), 0)) < time.Minute*2 || today >= 30 {
				s.log(ctx, fmt.Sprintf("We are Waiting for %v or %v", today, s.nowTime().Sub(time.Unix(config.GetLastProcess(), 0))))
				continue
			}
			albumID := item.GetAlbumId()
			s.log(ctx, fmt.Sprintf("%v is missing a mapping -> %v - %v (%v)", albumID, item.GetBandName(), item.GetAlbumTitle(), today))

			if config.IssueIds[albumID] == 0 {
				initialCookie := config.GetIdentityCookie()
				wavURL, err := s.resolveWAVLinkForItem(ctx, config, item)
				if err == nil && wavURL != "" {
					if clearErr := s.clearAuthAlert(ctx, config); clearErr != nil {
						s.log(ctx, fmt.Sprintf("Failed to clear Bandcamp auth alert: %v", sanitizeError(clearErr, initialCookie, config.GetIdentityCookie())))
					}
					title := fmt.Sprintf("Bandcamp entry for %v is missing mapping [%v]", item.GetBandName(), albumID)
					candidates := s.lookupDiscogsCandidates(ctx, rc, item, initialCookie, config.GetIdentityCookie())
					body := buildMissingMappingIssueBody(item, wavURL, maxDownloadLinkFailures, candidates, initialCookie, config.GetIdentityCookie())
					issue, issueErr := issues.ImmediateIssue(ctx, title, body, false, false)
					if issueErr != nil {
						return nil, sanitizeError(issueErr, initialCookie, config.GetIdentityCookie())
					}
					config.IssueIds[albumID] = issue.GetNumber()
					delete(config.DownloadLinkFailures, albumID)
					return &rcpb.ClientUpdateResponse{}, sanitizeError(s.saveConfig(ctx, config), initialCookie, config.GetIdentityCookie())
				}

				if errors.Is(err, bandcamplib.ErrAuthenticationRequired) || errors.Is(err, bandcamplib.ErrAuthenticationFailed) {
					sanitizedErr := sanitizeError(err, initialCookie, config.GetIdentityCookie())
					s.log(ctx, fmt.Sprintf("Failed to resolve WAV download link for album %v due to authentication error: %v", albumID, sanitizedErr))
					reason := "Bandcamp session cookie expired or was rejected while resolving WAV download link."
					if errors.Is(err, bandcamplib.ErrAuthenticationRequired) {
						reason = "Bandcamp session cookie is missing while resolving WAV download link."
					}
					if alertErr := s.ensureAuthAlert(ctx, config, reason); alertErr != nil {
						s.log(ctx, fmt.Sprintf("Failed to ensure Bandcamp auth alert: %v", sanitizeError(alertErr, initialCookie, config.GetIdentityCookie())))
					}
					return nil, sanitizedErr
				}

				if err == nil {
					err = bandcamplib.ErrDownloadUnavailable
				}
				failures := config.DownloadLinkFailures[albumID] + 1
				sanitizedErr := sanitizeError(err, initialCookie, config.GetIdentityCookie())
				if failures < maxDownloadLinkFailures {
					s.log(ctx, fmt.Sprintf("Failed to resolve WAV download link for album %v (attempt %d/%d): %v", albumID, failures, maxDownloadLinkFailures, sanitizedErr))
					config.DownloadLinkFailures[albumID] = failures
					if saveErr := s.saveConfig(ctx, config); saveErr != nil {
						return nil, sanitizeError(
							fmt.Errorf("persist download link failure count: %w (after link resolution error: %w)", saveErr, err),
							initialCookie,
							config.GetIdentityCookie(),
						)
					}
					return nil, sanitizedErr
				}

				s.log(ctx, fmt.Sprintf("Failed to resolve WAV download link for album %v (attempt %d/%d, falling back to linkless issue): %v", albumID, failures, maxDownloadLinkFailures, sanitizedErr))

				title := fmt.Sprintf("Bandcamp entry for %v is missing mapping [%v]", item.GetBandName(), albumID)
				candidates := s.lookupDiscogsCandidates(ctx, rc, item, initialCookie, config.GetIdentityCookie())
				body := buildMissingMappingIssueBody(item, "", maxDownloadLinkFailures, candidates, initialCookie, config.GetIdentityCookie())
				issue, issueErr := issues.ImmediateIssue(ctx, title, body, false, false)
				if issueErr != nil {
					return nil, sanitizeError(issueErr, initialCookie, config.GetIdentityCookie())
				}
				config.IssueIds[albumID] = issue.GetNumber()
				delete(config.DownloadLinkFailures, albumID)
				return &rcpb.ClientUpdateResponse{}, sanitizeError(s.saveConfig(ctx, config), initialCookie, config.GetIdentityCookie())
			}
			return &rcpb.ClientUpdateResponse{}, nil
		} else if val > 0 && config.AddedDate[val] == 0 {
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
					return &rcpb.ClientUpdateResponse{}, s.saveConfig(ctx, config)
				}
			}

			if config.AddedDate[val] > 0 {
				issues.RaiseIssue("Bad add", fmt.Sprintf("We've already added %v from %v at %v", val, item, config.AddedDate[val]))
				return nil, fmt.Errorf("Oveerlap issue")
			}
			_, err = rc.AddRecord(ctx, &rcpb.AddRecordRequest{
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

			config.AddedDate[val] = s.nowTime().Unix()
			return &rcpb.ClientUpdateResponse{}, s.saveConfig(ctx, config)
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

	config.Mapping[req.GetBandcampId()] = req.GetDiscogsId()
	delete(config.DownloadLinkFailures, req.GetBandcampId())

	if config.IssueIds[req.GetBandcampId()] > 0 {
		err := s.getIssueManager().DeleteIssue(ctx, config.IssueIds[req.GetBandcampId()])
		if err != nil {
			return nil, fmt.Errorf("unable to remove issue: %w", err)
		}
	}

	config.LastProcess = s.nowTime().Unix()

	return &pb.AddMappingResponse{}, s.saveConfig(ctx, config)
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
