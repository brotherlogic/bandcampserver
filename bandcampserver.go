package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"time"

	"github.com/brotherlogic/goserver"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	pb "github.com/brotherlogic/bandcampserver/proto"
	dspb "github.com/brotherlogic/dstore/proto"
	pbg "github.com/brotherlogic/goserver/proto"
	"github.com/brotherlogic/goserver/utils"
	rcpb "github.com/brotherlogic/recordcollection/proto"

	google_protobuf "github.com/golang/protobuf/ptypes/any"
)

const (
	CONFIG_KEY = "github.com/brotherlogic/bandcampserver/config"
)

var (
	//Backlog - the print queue
	count = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "bandcampserver_count",
		Help: "The size of the tracking queue",
	})
	done = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "bandcampserver_done",
		Help: "The size of the tracking queue",
	})
	tokenAge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "bandcampserver_token_age",
		Help: "The size of the tracking queue",
	})
)

//Server main server type
type Server struct {
	*goserver.GoServer
}

// Init builds the server
func Init() *Server {
	s := &Server{
		GoServer: &goserver.GoServer{},
	}
	return s
}

// DoRegister does RPC registration
func (s *Server) DoRegister(server *grpc.Server) {
	rcpb.RegisterClientUpdateServiceServer(server, s)
	pb.RegisterBandcampServerServiceServer(server, s)
}

// ReportHealth alerts if we're not healthy
func (s *Server) ReportHealth() bool {
	return true
}

// Shutdown the server
func (s *Server) Shutdown(ctx context.Context) error {
	return nil
}

// Mote promotes/demotes this server
func (s *Server) Mote(ctx context.Context, master bool) error {
	return nil
}

// GetState gets the state of the server
func (s *Server) GetState() []*pbg.State {
	return []*pbg.State{
		{Key: "magic", Value: int64(12345)},
	}
}

func (s *Server) load(ctx context.Context, key string) ([]byte, error) {
	conn, err := s.FDialServer(ctx, "dstore")
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	client := dspb.NewDStoreServiceClient(conn)
	res, err := client.Read(ctx, &dspb.ReadRequest{Key: key})
	if err != nil {
		return nil, err
	}

	if res.GetConsensus() < 0.5 {
		return nil, fmt.Errorf("could not get read consensus (%v)", res.GetConsensus())
	}

	return res.GetValue().GetValue(), nil
}

func (s *Server) loadConfig(ctx context.Context) (*pb.Config, error) {
	data, err := s.load(ctx, CONFIG_KEY)
	if err != nil {
		if status.Convert(err).Code() == codes.InvalidArgument {
			return &pb.Config{}, nil
		}
		return nil, err
	}

	config := &pb.Config{}
	err = proto.Unmarshal(data, config)
	if err != nil {
		return nil, err
	}

	dc := 0
	for _, item := range config.GetItems() {
		if config.GetMapping()[item.GetAlbumId()] > 0 {
			dc++
		}
	}

	done.Set(float64(dc))
	count.Set(float64(len(config.GetItems())))
	tokenAge.Set(float64(config.GetLastTokenRefresh()))

	if config.Mapping == nil {
		config.Mapping = make(map[int64]int32)
	}
	if config.IssueIds == nil {
		config.IssueIds = make(map[int64]int32)
	}

	return config, nil
}

func (s *Server) saveConfig(ctx context.Context, config *pb.Config) error {
	data, err := proto.Marshal(config)
	if err != nil {
		return err
	}
	return s.save(ctx, data, CONFIG_KEY)
}

func (s *Server) save(ctx context.Context, data []byte, key string) error {
	conn, err := s.FDialServer(ctx, "dstore")
	if err != nil {
		return err
	}
	defer conn.Close()

	client := dspb.NewDStoreServiceClient(conn)
	res, err := client.Write(ctx, &dspb.WriteRequest{Key: key, Value: &google_protobuf.Any{Value: data}})
	if err != nil {
		return err
	}

	if res.GetConsensus() < 0.5 {
		return fmt.Errorf("could not get write consensus (%v)", res.GetConsensus())
	}

	return nil
}

func (s *Server) loadRecord(ctx context.Context, id int32) (*rcpb.Record, error) {
	conn, err := s.FDialServer(ctx, "recordcollection")
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	client := rcpb.NewRecordCollectionServiceClient(conn)

	resp, err := client.GetRecord(ctx, &rcpb.GetRecordRequest{InstanceId: id})
	if err != nil {
		return nil, err
	}
	return resp.GetRecord(), nil
}

func main() {
	var quiet = flag.Bool("quiet", false, "Show all output")
	flag.Parse()

	//Turn off logging
	if *quiet {
		log.SetFlags(0)
		log.SetOutput(ioutil.Discard)
	}
	server := Init()
	server.PrepServer()
	server.Register = server

	err := server.RegisterServerV2("bandcampserver", false, true)
	if err != nil {
		return
	}

	// Preload metrics
	ctx, cancel := utils.ManualContext("bandcampserver-init", time.Minute)
	_, err = server.loadConfig(ctx)
	if err != nil {
		cancel()
		log.Fatalf("Unable to load initial cache: %v", err)
	}
	cancel()

	fmt.Printf("%v", server.Serve())
}
