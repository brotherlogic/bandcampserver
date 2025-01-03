package main

import (
	"fmt"
	"time"

	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/brotherlogic/bandcamplib"
	"github.com/brotherlogic/bandcamplib/proto"

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

	return nil, status.Errorf(codes.NotFound, fmt.Sprintf("Could not find %v", req))
}

// ClientUpdate on an updated record
func (s *Server) ClientUpdate(ctx context.Context, req *rcpb.ClientUpdateRequest) (*rcpb.ClientUpdateResponse, error) {
	config, err := s.loadConfig(ctx)
	if err != nil {
		return nil, err
	}

	today := s.metrics(ctx, config)

	record, err := s.rcclient.GetRecord(ctx, &rcpb.GetRecordRequest{InstanceId: req.GetInstanceId()})
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
	if record.Record.GetRelease().GetFolderId() == 1782105 {
		found := false
		for _, format := range record.GetRecord().GetRelease().GetFormats() {
			if format.GetName() == "File" {
				found = true
			}
		}

		if !found {
			// This can be deleted?
			s.RaiseIssue("Can we delete", fmt.Sprintf("https://www.discogs.com/madeup/release/%v", record.GetRecord().GetRelease().GetId()))
		}
	}

	s.CtxLog(ctx, fmt.Sprintf("Running proc with %v done today", today))
	for _, item := range config.Items {
		if val, ok := config.GetMapping()[item.GetAlbumId()]; !ok {
			// Skip processing when we've done 4 in a day
			if today >= 3 || time.Since(time.Unix(config.GetLastProcess(), 0)) < time.Minute*10 {
				s.CtxLog(ctx, fmt.Sprintf("Waiting for %v or %v", today, time.Since(time.Unix(config.GetLastProcess(), 0))))
				continue
			}
			s.CtxLog(ctx, fmt.Sprintf("%v is missing a mapping -> %v (%v)", item.GetAlbumId(), item, today))

			if config.IssueIds[item.GetAlbumId()] == 0 {
				issue, err := s.ImmediateIssue(ctx, fmt.Sprintf("Bandcamp entry for %v is missing mapping [%v]", item.GetBandName(), item.GetAlbumId()), fmt.Sprintf("%v - %v (%v) is missing a mapping", item.GetBandName(), item.GetAlbumTitle(), item.GetAlbumId()), false, false)
				if err != nil {
					return nil, err
				}
				config.IssueIds[item.GetAlbumId()] = issue.GetNumber()
				return &rcpb.ClientUpdateResponse{}, s.saveConfig(ctx, config)
			}
			return &rcpb.ClientUpdateResponse{}, nil
		} else if val > 0 && config.AddedDate[val] == 0 {
			conn, err := s.FDialServer(ctx, "recordcollection")
			if err != nil {
				return nil, err
			}
			defer conn.Close()

			client := rcpb.NewRecordCollectionServiceClient(conn)
			iids, err := client.QueryRecords(ctx, &rcpb.QueryRecordsRequest{Query: &rcpb.QueryRecordsRequest_ReleaseId{val}})
			if err != nil {
				return nil, err
			}
			for _, iid := range iids.GetInstanceIds() {
				rec, err := client.GetRecord(ctx, &rcpb.GetRecordRequest{InstanceId: iid})
				if err != nil {
					return nil, err
				}

				if rec.GetRecord().GetMetadata().GetGoalFolder() == 1782105 {
					config.AddedDate[val] = time.Now().Unix()
					return &rcpb.ClientUpdateResponse{}, s.saveConfig(ctx, config)
				}
			}

			if config.AddedDate[val] > 0 {
				s.RaiseIssue("Bad add", fmt.Sprintf("We've already added %v from %v at %v", val, item, config.AddedDate[val]))
				return nil, fmt.Errorf("Oveerlap issue")
			}
			_, err = client.AddRecord(ctx, &rcpb.AddRecordRequest{
				ToAdd: &rcpb.Record{
					Release: &rcgd.Release{
						Id: val,
					},
					Metadata: &rcpb.ReleaseMetadata{
						Cost:           1,
						GoalFolder:     1782105,
						PurchaseBudget: fmt.Sprintf("float%v", time.Now().Year()),
						FiledUnder:     rcpb.ReleaseMetadata_FILE_DIGITAL,
						DateArrived:    time.Now().Unix(),
					},
				},
			})

			if err != nil {
				return nil, err
			}

			config.AddedDate[val] = time.Now().Unix()
			return &rcpb.ClientUpdateResponse{}, s.saveConfig(ctx, config)
		}
	}

	return &rcpb.ClientUpdateResponse{}, nil
}

func (s *Server) SetToken(ctx context.Context, req *pb.SetTokenRequest) (*pb.SetTokenResponse, error) {
	config, err := s.loadConfig(ctx)
	if err != nil {
		return nil, err
	}

	config.Token = req.GetToken()
	config.LastTokenRefresh = time.Now().Unix()

	items, err := bandcamplib.RetrieveItems(&proto.Config{User: 873256, Token: config.Token})
	if err != nil {
		return nil, err
	}

	s.CtxLog(ctx, fmt.Sprintf("Read %v items", len(items)))

	config.Items = items

	return &pb.SetTokenResponse{}, s.saveConfig(ctx, config)
}

func (s *Server) AddMapping(ctx context.Context, req *pb.AddMappingRequest) (*pb.AddMappingResponse, error) {
	config, err := s.loadConfig(ctx)
	if err != nil {
		return nil, err
	}

	config.Mapping[req.GetBandcampId()] = req.GetDiscogsId()

	if config.IssueIds[req.GetBandcampId()] > 0 {
		err := s.DeleteIssue(ctx, config.IssueIds[req.GetBandcampId()])
		if err != nil {
			return nil, fmt.Errorf("unable to remove issue: %w", err)
		}
	}

	config.LastProcess = time.Now().Unix()

	return &pb.AddMappingResponse{}, s.saveConfig(ctx, config)
}

func (s *Server) Reset(ctx context.Context, req *pb.ResetRequest) (*pb.ResetResponse, error) {
	config, err := s.loadConfig(ctx)
	if err != nil {
		return nil, err
	}

	config.IssueIds = make(map[int64]int32)

	return &pb.ResetResponse{}, s.saveConfig(ctx, config)
}
