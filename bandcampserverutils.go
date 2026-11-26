package main

import (
	"time"

	"golang.org/x/net/context"

	pb "github.com/brotherlogic/bandcampserver/proto"
	rcpb "github.com/brotherlogic/recordcollection/proto"
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
	today = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "bandcampserver_today",
		Help: "The size of the tracking queue",
	})
	toGo = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "bandcampserver_togo",
		Help: "The size of the tracking queue",
	})
	perDay = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "bandcampserver_per_day",
		Help: "The size of the tracking queue",
	})
	completion = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "bandcampserver_completion"})
)

func (s *Server) metrics(ctx context.Context, config *pb.Config) int {
	dc := float64(0)
	for _, item := range config.GetItems() {
		if config.GetMapping()[item.GetAlbumId()] > 0 {
			dc++
		}
	}

	perc.Set(dc / float64(len(config.GetItems())))

	now := s.nowTime()

	last14 := float64(0)
	last24 := float64(0)
	for _, elem := range config.GetAddedDate() {
		if now.Sub(time.Unix(elem, 0)) < time.Hour*24*14 {
			last14++
		}
		if now.Sub(time.Unix(elem, 0)) < time.Hour*24 {
			last24++
		}
	}
	compPerDay := last14 / 14
	perDay.Set(compPerDay)

	togo := float64(0)
	for _, item := range config.GetItems() {
		if _, ok := config.GetMapping()[item.GetAlbumId()]; !ok {
			togo++
		}
	}
	toGo.Set(float64(togo))
	days := togo / compPerDay
	ftime := now.Add(time.Hour * time.Duration(24*days))
	end.Set(float64(ftime.Unix()))
	today.Set(float64(last24))

	dates.Set(float64(len(config.GetAddedDate())))

	// Use a two week window
	done := 0
	for _, item := range config.GetAddedDate() {
		if now.Sub(time.Unix(item, 0)) < time.Hour*24*14 {
			done++
		}
	}
	perDay := float64(done) / 14
	daysToGo := togo / perDay
	completion.Set(float64(now.Add(time.Hour * time.Duration(24*daysToGo)).Unix()))

	updateAuthMetrics(config)

	return int(last24)
}

func (s *Server) validate(ctx context.Context, config *pb.Config) error {
	now := s.nowTime()
	for id, mid := range config.GetMapping() {
		// Validate once per week
		if val, ok := config.GetLastValidateDate()[id]; !ok || now.Sub(time.Unix(val, 0)) > time.Hour*24*7 {
			res, err := s.getRCClient().QueryRecords(ctx, &rcpb.QueryRecordsRequest{Query: &rcpb.QueryRecordsRequest_ReleaseId{ReleaseId: mid}})
			if err != nil {
				return err
			}

			if len(res.GetInstanceIds()) == 0 {
				delete(config.Mapping, id)
			}

			config.LastValidateDate[id] = now.Unix()
			return nil
		}
	}

	return nil
}
