package main

import (
	"time"

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
	perc.Set(float64(len(config.GetMapping())) / float64(len(config.GetItems())))

	last14 := float64(0)
	for _, elem := range config.GetAddedDate() {
		if time.Since(time.Unix(elem, 0)) < time.Hour*24*14 {
			last14++
		}
	}
	compPerDay := last14 / 14
	togo := float64(len(config.GetItems()) - len(config.GetMapping()))
	days := togo / compPerDay
	ftime := time.Now().Add(time.Hour * time.Duration(24*days))
	end.Set(float64(ftime.Unix()))

}
