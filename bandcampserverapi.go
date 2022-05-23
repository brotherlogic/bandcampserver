package main

import (
	"fmt"
	"time"

	"golang.org/x/net/context"

	"github.com/brotherlogic/bandcamplib"
	"github.com/brotherlogic/bandcamplib/proto"

	pb "github.com/brotherlogic/bandcampserver/proto"
	rcgd "github.com/brotherlogic/godiscogs"
	rcpb "github.com/brotherlogic/recordcollection/proto"
)

//ClientUpdate on an updated record
func (s *Server) ClientUpdate(ctx context.Context, req *rcpb.ClientUpdateRequest) (*rcpb.ClientUpdateResponse, error) {
	config, err := s.loadConfig(ctx)
	if err != nil {
		return nil, err
	}

	for _, item := range config.Items {
		if val, ok := config.GetMapping()[item.GetAlbumId()]; !ok {
			s.CtxLog(ctx, fmt.Sprintf("%v is missing a mapping -> %v", item.GetAlbumId(), item))
			if config.IssueIds[item.GetAlbumId()] == 0 {
				issue, err := s.ImmediateIssue(ctx, fmt.Sprintf("Bandcamp entry for %v is missing mapping", item.GetBandName()), fmt.Sprintf("%v - %v (%v) is missing a mapping", item.GetBandName(), item.GetAlbumTitle(), item.GetAlbumId()))
				if err != nil {
					return nil, err
				}
				config.IssueIds[item.GetAlbumId()] = issue.GetNumber()
				return &rcpb.ClientUpdateResponse{}, s.saveConfig(ctx, config)
			}
			return &rcpb.ClientUpdateResponse{}, nil
		} else {
			found := false
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
					found = true
				}
			}

			if !found {
				if config.AddedDate[val] > 0 {
					s.RaiseIssue("Bad add", fmt.Sprintf("We've already added %v at %v", val, config.AddedDate[val]))
					return nil, fmt.Errorf("Oveerlap issue")
				}
				_, err := client.AddRecord(ctx, &rcpb.AddRecordRequest{
					ToAdd: &rcpb.Record{
						Release: &rcgd.Release{
							Id: val,
						},
						Metadata: &rcpb.ReleaseMetadata{
							Cost:           1,
							GoalFolder:     1782105,
							PurchaseBudget: "float",
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
	}

	return nil, fmt.Errorf("Unable to proecss bandacmp")
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
		s.DeleteIssue(ctx, config.IssueIds[req.GetBandcampId()])
	}

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
