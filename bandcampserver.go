package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/brotherlogic/bandcamplib"
	"github.com/brotherlogic/goserver"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	bcpb "github.com/brotherlogic/bandcamplib/proto"
	pb "github.com/brotherlogic/bandcampserver/proto"
	dspb "github.com/brotherlogic/dstore/proto"
	pbgh "github.com/brotherlogic/githubcard/proto"
	pbg "github.com/brotherlogic/goserver/proto"
	"github.com/brotherlogic/goserver/utils"
	rcpb "github.com/brotherlogic/recordcollection/proto"

	recordcollection_client "github.com/brotherlogic/recordcollection/client"

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
	monthPerc = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "bandcampserver_month",
		Help: "The size of the tracking queue",
	})
	dates = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "bandcampserver_dates",
		Help: "The size of the tracking queue",
	})
	cookieAge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "bandcampserver_cookie_age",
		Help: "Unix timestamp of the last Bandcamp identity cookie refresh",
	})
	authValid = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "bandcampserver_auth_valid",
		Help: "Whether Bandcamp cookie authentication is currently valid",
	})
)

type configStore interface {
	Load(ctx context.Context, key string) ([]byte, error)
	Save(ctx context.Context, data []byte, key string) error
}

type bandcampClient interface {
	RetrieveItems(ctx context.Context) ([]*bcpb.Item, error)
	GetDownloadURLForRedownloadURL(ctx context.Context, redownloadURL string, format bandcamplib.Format) (string, error)
	IdentityCookie() string
}

type bandcampClientFactory func(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error)

type issueManager interface {
	ImmediateIssue(ctx context.Context, title, body string, printImmediately, print bool, labels ...string) (*pbgh.Issue, error)
	DeleteIssue(ctx context.Context, number int32) error
	RaiseIssue(title, body string, labels ...string)
}

type rcClient interface {
	GetRecord(ctx context.Context, req *rcpb.GetRecordRequest) (*rcpb.GetRecordResponse, error)
	QueryRecords(ctx context.Context, req *rcpb.QueryRecordsRequest) (*rcpb.QueryRecordsResponse, error)
	AddRecord(ctx context.Context, req *rcpb.AddRecordRequest) (*rcpb.AddRecordResponse, error)
}

type dstoreConfigStore struct {
	server *Server
}

func (d *dstoreConfigStore) Load(ctx context.Context, key string) ([]byte, error) {
	return d.server.load(ctx, key)
}

func (d *dstoreConfigStore) Save(ctx context.Context, data []byte, key string) error {
	return d.server.save(ctx, data, key)
}

func defaultBandcampClientFactory(cfg *bcpb.Config, onRefresh func(string)) (bandcampClient, error) {
	if onRefresh != nil {
		return bandcamplib.NewClient(cfg, bandcamplib.WithCookieRefreshCallback(onRefresh))
	}
	return bandcamplib.NewClient(cfg)
}

type prodRCClient struct {
	server *Server
}

func (p *prodRCClient) GetRecord(ctx context.Context, req *rcpb.GetRecordRequest) (*rcpb.GetRecordResponse, error) {
	return p.server.rcclient.GetRecord(ctx, req)
}

func (p *prodRCClient) QueryRecords(ctx context.Context, req *rcpb.QueryRecordsRequest) (*rcpb.QueryRecordsResponse, error) {
	return p.server.rcclient.QueryRecords(ctx, req)
}

func (p *prodRCClient) AddRecord(ctx context.Context, req *rcpb.AddRecordRequest) (*rcpb.AddRecordResponse, error) {
	conn, err := p.server.FDialServer(ctx, "recordcollection")
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	client := rcpb.NewRecordCollectionServiceClient(conn)
	return client.AddRecord(ctx, req)
}

// Server main server type
type Server struct {
	*goserver.GoServer
	mu        sync.Mutex
	rcclient  *recordcollection_client.RecordCollectionClient
	store     configStore
	bcFactory bandcampClientFactory
	issues    issueManager
	rc        rcClient
	now       func() time.Time
	logFunc   func(ctx context.Context, message string)
}

// Init builds the server
func Init() *Server {
	s := &Server{
		GoServer: &goserver.GoServer{},
	}
	s.rcclient = &recordcollection_client.RecordCollectionClient{Gs: s.GoServer}
	s.store = &dstoreConfigStore{server: s}
	s.bcFactory = defaultBandcampClientFactory
	s.issues = s.GoServer
	s.rc = &prodRCClient{server: s}
	s.now = time.Now
	s.logFunc = s.GoServer.CtxLog
	return s
}

func (s *Server) nowTime() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *Server) log(ctx context.Context, message string) {
	if s.logFunc != nil {
		s.logFunc(ctx, message)
		return
	}
	if s.GoServer != nil {
		s.CtxLog(ctx, message)
	}
}

func (s *Server) getIssueManager() issueManager {
	if s.issues != nil {
		return s.issues
	}
	return s.GoServer
}

func (s *Server) getRCClient() rcClient {
	if s.rc != nil {
		return s.rc
	}
	return &prodRCClient{server: s}
}

func updateAuthMetrics(config *pb.Config) {
	cookieAge.Set(float64(config.GetLastCookieRefresh()))
	if strings.TrimSpace(config.GetIdentityCookie()) != "" && config.GetAuthAlertIssueId() == 0 {
		authValid.Set(1)
	} else {
		authValid.Set(0)
	}
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
	var data []byte
	var err error
	if s.store != nil {
		data, err = s.store.Load(ctx, CONFIG_KEY)
	} else {
		data, err = s.load(ctx, CONFIG_KEY)
	}

	config := &pb.Config{}
	if err != nil {
		if status.Convert(err).Code() != codes.InvalidArgument {
			return nil, err
		}
	} else {
		if err = proto.Unmarshal(data, config); err != nil {
			return nil, err
		}
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
	monthPerc.Set(float64(s.nowTime().Day()) / 30.0)

	if config.Mapping == nil {
		config.Mapping = make(map[int64]int32)
	}
	if config.IssueIds == nil {
		config.IssueIds = make(map[int64]int32)
	}
	if config.AddedDate == nil {
		config.AddedDate = make(map[int32]int64)
	}
	if config.LastValidateDate == nil {
		config.LastValidateDate = make(map[int64]int64)
	}
	if config.DownloadLinkFailures == nil {
		config.DownloadLinkFailures = make(map[int64]int32)
	}

	updateAuthMetrics(config)
	s.metrics(ctx, config)

	s.log(ctx, fmt.Sprintf("Got token: %v", config.GetToken()))

	return config, nil
}

func (s *Server) saveConfig(ctx context.Context, config *pb.Config) error {
	data, err := proto.Marshal(config)
	if err != nil {
		return err
	}
	if s.store != nil {
		return s.store.Save(ctx, data, CONFIG_KEY)
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

func (s *Server) refreshStartupToken(ctx context.Context, config *pb.Config, setToken func(context.Context, *pb.SetTokenRequest) (*pb.SetTokenResponse, error)) {
	if _, err := setToken(ctx, &pb.SetTokenRequest{Token: fmt.Sprintf("%v::a::", s.nowTime().Unix())}); err != nil {
		s.log(ctx, fmt.Sprintf("Startup temporary token refresh failed: %v", err))
	}
	if _, err := setToken(ctx, &pb.SetTokenRequest{Token: config.GetToken()}); err != nil {
		s.log(ctx, fmt.Sprintf("Startup token restore failed: %v", err))
	}
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
	server.PrepServer("bandcampserver")
	server.Register = server

	err := server.RegisterServerV2(false)
	if err != nil {
		return
	}

	// Preload metrics
	ctx, cancel := utils.ManualContext("bandcampserver-init", time.Minute)
	config, err := server.loadConfig(ctx)
	if err != nil {
		cancel()
		log.Fatalf("Unable to load initial cache: %v", err)
	}
	server.log(ctx, fmt.Sprintf("Loaded with %v items", len(config.GetItems())))
	server.metrics(ctx, config)

	server.refreshStartupToken(ctx, config, server.SetToken)
	cancel()

	fmt.Printf("%v", server.Serve())
}
