package main

import (
	"flag"
	"log"
	"os"
	"time"

	"github.com/brotherlogic/goserver/utils"

	pb "github.com/brotherlogic/bandcampserver/proto"
)

func main() {
	ctx, cancel := utils.ManualContext("bandcampserver-cli", time.Second*10)
	defer cancel()

	conn, err := utils.LFDialServer(ctx, "bandcampserver")
	if err != nil {
		log.Fatalf("Unable to dial: %v", err)
	}
	defer conn.Close()

	client := pb.NewBandcampServerServieClient(conn)

	switch os.Args[1] {
	case "token":
		tokenFlags := flag.NewFlagSet("Token", flag.ExitOnError)
		var token = tokenFlags.String("token", "", "Token")

		if err := tokenFlags.Parse(os.Args[2:]); err == nil {
			client.SetToken(ctx, &pb.SetTokenRequest{Token: *token})
		}
	}
}
