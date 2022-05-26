package main

import (
	"golang.org/x/net/context"

	pb "github.com/brotherlogic/bandcampserver/proto"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	perc = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "bandcampserver_perc",
		Help: "The size of the tracking queue",
	})
	end = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "bandcampserver_end",
		Help: "The size of the tracking queue",
	})
)

func (s *Server) metrics(ctx context.Context, config *pb.Config) {
	perc.Set(float64(len(config.GetMapping())) / float64(len(config.GetIssueIds())))
}
