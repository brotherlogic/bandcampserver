package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/brotherlogic/goserver/utils"

	pb "github.com/brotherlogic/bandcampserver/proto"
		pbrc "github.com/brotherlogic/recordcollection/proto"
)

func main() {
	ctx, cancel := utils.ManualContext("bandcampserver-cli", time.Second*10)
	defer cancel()

	conn, err := utils.LFDialServer(ctx, "bandcampserver")
	if err != nil {
		log.Fatalf("Unable to dial: %v", err)
	}
	defer conn.Close()

	client := pb.NewBandcampServerServiceClient(conn)

	switch os.Args[1] {
	case "lookup":
		lookupFlags := flag.NewFlagSet("Lookup", flag.ExitOnError)
		var id = lookupFlags.Int64("id", -1, "ID")

		if err := lookupFlags.Parse(os.Args[2:]); err == nil {
			r, err := client.Lookup(ctx, &pb.LookupRequest{BandcampId: *id})
			fmt.Printf("Lookup Response: %v -> %v\n", r, err)
		}
	case "token":
		tokenFlags := flag.NewFlagSet("Token", flag.ExitOnError)
		var token = tokenFlags.String("token", "", "Token")

		if err := tokenFlags.Parse(os.Args[2:]); err == nil {
			_, err := client.SetToken(ctx, &pb.SetTokenRequest{Token: *token})
			fmt.Printf("Set token: %v\n", err)
		}
	case "mapping":
		mappingFlags := flag.NewFlagSet("Mapping", flag.ExitOnError)
		var bc = mappingFlags.Int64("bandcamp", -1, "Bandcamp in")
		var dc = mappingFlags.Int("discogs", -1, "Discogs Id")
		if err := mappingFlags.Parse(os.Args[2:]); err == nil {
			_, err := client.AddMapping(ctx, &pb.AddMappingRequest{BandcampId: *bc, DiscogsId: int32(*dc)})
			if err != nil {
				fmt.Printf("Mapping problem: %v", err)
			}
		}
	case "reset":
		_, err := client.Reset(ctx, &pb.ResetRequest{})
		if err != nil {
			log.Fatalf("Error in reset: %v", err)
		}
			case "ping":
		id, _ := strconv.ParseInt(os.Args[2], 10, 32)
		sclient := pbrc.NewClientUpdateServiceClient(conn)
		_, err = sclient.ClientUpdate(ctx, &pbrc.ClientUpdateRequest{InstanceId: int32(id)})
		if err != nil {
			log.Fatalf("Error on GET: %v", err)
		}

	}
}
