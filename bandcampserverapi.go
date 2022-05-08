package main

import (
	"fmt"
	"time"

	"golang.org/x/net/context"

	"github.com/brotherlogic/bandcamplib"
	"github.com/brotherlogic/bandcamplib/proto"

	pb "github.com/brotherlogic/bandcampserver/proto"
	rcpb "github.com/brotherlogic/recordcollection/proto"
)

//ClientUpdate on an updated record
func (s *Server) ClientUpdate(ctx context.Context, req *rcpb.ClientUpdateRequest) (*rcpb.ClientUpdateResponse, error) {
	config, err := s.loadConfig(ctx)
	if err != nil {
		return nil, err
	}

	for _, item := range config.Items {
		if _, ok := config.GetMapping()[item.GetAlbumId()]; !ok {
			s.RaiseIssue("Bandcamp entry is missing mapping", fmt.Sprintf("%v - %v (%v) is missing a mapping", item.GetBandName(), item.GetAlbumTitle(), item.GetAlbumId()))
			return &rcpb.ClientUpdateResponse{}, nil
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

	return nil, s.saveConfig(ctx, config)
}

func (s *Server) AddMapping(ctx context.Context, req *pb.AddMappingRequest) (*pb.AddMappingResponse, error) {
	config, err := s.loadConfig(ctx)
	if err != nil {
		return nil, err
	}

	config.Mapping[req.GetBandcampId()] = req.GetDiscogsId()

	return nil, s.saveConfig(ctx, config)
}
