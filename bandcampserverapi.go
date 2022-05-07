package main

import (
	"fmt"
	"time"

	"golang.org/x/net/context"

	rcpb "github.com/brotherlogic/recordcollection/proto"
	pb "github.com/brotherlogic/recordsorg/proto"
)

//ClientUpdate on an updated record
func (s *Server) ClientUpdate(ctx context.Context, req *rcpb.ClientUpdateRequest) (*rcpb.ClientUpdateResponse, error) {
	return nil, fmt.Errorf("Unable to proecss bandacmp")
}

func (s *Server) SetToken(ctx context.Context, req *pb.SetTokenRequest) (*pb.SetTokenResponse, error) {
	config, err := s.loadConfig(ctx)
	if err != nil {
		return nil, err
	}

	config.Token = req.GetToken()
	config.LastTokenRefresh = time.Now().Unix()

	return nil, s.saveConfig(ctx, config)
}
