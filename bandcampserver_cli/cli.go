package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/brotherlogic/goserver/utils"

	pb "github.com/brotherlogic/bandcampserver/proto"
	pbrc "github.com/brotherlogic/recordcollection/proto"
)

type dialFunc func(ctx context.Context) (pb.BandcampServerServiceClient, pbrc.ClientUpdateServiceClient, func() error, error)

func defaultDial(ctx context.Context) (pb.BandcampServerServiceClient, pbrc.ClientUpdateServiceClient, func() error, error) {
	conn, err := utils.LFDialServer(ctx, "bandcampserver")
	if err != nil {
		return nil, nil, nil, err
	}
	return pb.NewBandcampServerServiceClient(conn), pbrc.NewClientUpdateServiceClient(conn), conn.Close, nil
}

func sanitizeSecret(msg string, secrets ...string) string {
	for _, secret := range secrets {
		if secret != "" {
			msg = strings.ReplaceAll(msg, secret, "[REDACTED]")
		}
	}
	return msg
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: bandcampserver_cli <subcommand> [flags]")
	fmt.Fprintln(w, "Subcommands: lookup, token, cookie, mapping, download, reset, ping")
}

func withClient(ctx context.Context, stderr io.Writer, dial dialFunc, secrets []string, fn func(pb.BandcampServerServiceClient, pbrc.ClientUpdateServiceClient) int) int {
	client, sclient, closeFn, err := dial(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "Unable to dial: %s\n", sanitizeSecret(err.Error(), secrets...))
		return 1
	}
	if closeFn != nil {
		defer closeFn()
	}
	return fn(client, sclient)
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, dial dialFunc) int {
	if len(args) < 1 {
		printUsage(stderr)
		return 1
	}

	switch args[0] {
	case "lookup":
		lookupFlags := flag.NewFlagSet("Lookup", flag.ContinueOnError)
		lookupFlags.SetOutput(stderr)
		id := lookupFlags.Int64("id", -1, "ID")
		if err := lookupFlags.Parse(args[1:]); err != nil {
			return 1
		}
		return withClient(ctx, stderr, dial, nil, func(client pb.BandcampServerServiceClient, _ pbrc.ClientUpdateServiceClient) int {
			r, err := client.Lookup(ctx, &pb.LookupRequest{BandcampId: *id})
			fmt.Fprintf(stdout, "Lookup Response: %v -> %v\n", r, err)
			if err != nil {
				return 1
			}
			return 0
		})

	case "token":
		tokenFlags := flag.NewFlagSet("Token", flag.ContinueOnError)
		tokenFlags.SetOutput(stderr)
		token := tokenFlags.String("token", "", "Token")
		if err := tokenFlags.Parse(args[1:]); err != nil {
			return 1
		}
		return withClient(ctx, stderr, dial, nil, func(client pb.BandcampServerServiceClient, _ pbrc.ClientUpdateServiceClient) int {
			_, err := client.SetToken(ctx, &pb.SetTokenRequest{Token: *token})
			fmt.Fprintf(stdout, "Set token: %v\n", err)
			if err != nil {
				return 1
			}
			return 0
		})

	case "cookie":
		cookieFlags := flag.NewFlagSet("cookie", flag.ContinueOnError)
		cookieFlags.SetOutput(stderr)
		cookie := cookieFlags.String("cookie", "", "Bandcamp identity cookie value")
		if err := cookieFlags.Parse(args[1:]); err != nil {
			return 1
		}
		trimmed := strings.TrimSpace(*cookie)
		if trimmed == "" {
			fmt.Fprintln(stderr, "Usage: bandcampserver_cli cookie -cookie <value>")
			return 1
		}
		return withClient(ctx, stderr, dial, []string{*cookie, trimmed}, func(client pb.BandcampServerServiceClient, _ pbrc.ClientUpdateServiceClient) int {
			if _, err := client.SetCookie(ctx, &pb.SetCookieRequest{Cookie: trimmed}); err != nil {
				fmt.Fprintf(stderr, "Error setting cookie: %s\n", sanitizeSecret(err.Error(), *cookie, trimmed))
				return 1
			}
			fmt.Fprintln(stdout, "Bandcamp identity cookie updated successfully.")
			return 0
		})

	case "mapping":
		mappingFlags := flag.NewFlagSet("Mapping", flag.ContinueOnError)
		mappingFlags.SetOutput(stderr)
		bc := mappingFlags.Int64("bandcamp", -1, "Bandcamp in")
		dc := mappingFlags.Int("discogs", -1, "Discogs Id")
		if err := mappingFlags.Parse(args[1:]); err != nil {
			return 1
		}
		return withClient(ctx, stderr, dial, nil, func(client pb.BandcampServerServiceClient, _ pbrc.ClientUpdateServiceClient) int {
			if _, err := client.AddMapping(ctx, &pb.AddMappingRequest{BandcampId: *bc, DiscogsId: int32(*dc)}); err != nil {
				fmt.Fprintf(stderr, "Mapping problem: %v\n", err)
				return 1
			}
			return 0
		})

	case "download":
		downloadFlags := flag.NewFlagSet("download", flag.ContinueOnError)
		downloadFlags.SetOutput(stderr)
		bc := downloadFlags.Int64("bandcamp", 0, "Bandcamp album ID")
		dc := downloadFlags.Int("discogs", 0, "Discogs release ID")
		if err := downloadFlags.Parse(args[1:]); err != nil {
			return 1
		}
		var bandcampSet, discogsSet bool
		downloadFlags.Visit(func(f *flag.Flag) {
			switch f.Name {
			case "bandcamp":
				bandcampSet = true
			case "discogs":
				discogsSet = true
			}
		})
		const downloadUsage = "Usage: bandcampserver_cli download (-bandcamp <id> | -discogs <id>)"
		if downloadFlags.NArg() > 0 || bandcampSet == discogsSet {
			fmt.Fprintln(stderr, downloadUsage)
			return 1
		}
		if (bandcampSet && *bc <= 0) || (discogsSet && *dc <= 0) {
			fmt.Fprintln(stderr, "Error: ID must be a positive integer.")
			fmt.Fprintln(stderr, downloadUsage)
			return 1
		}
		req := &pb.TriggerDownloadRequest{}
		if bandcampSet {
			req.BandcampId = *bc
		} else {
			req.DiscogsId = int32(*dc)
		}
		return withClient(ctx, stderr, dial, nil, func(client pb.BandcampServerServiceClient, _ pbrc.ClientUpdateServiceClient) int {
			resp, err := client.TriggerDownload(ctx, req)
			if err != nil {
				fmt.Fprintf(stderr, "Error triggering download: %v\n", err)
				return 1
			}
			if resp.GetAlreadyActive() {
				fmt.Fprintf(stdout, "Download already active (bandcamp_id=%d, discogs_id=%d, state=%s): %s\n",
					resp.GetBandcampId(), resp.GetDiscogsId(), resp.GetState().String(), resp.GetMessage())
			} else {
				fmt.Fprintf(stdout, "Download queued (bandcamp_id=%d, discogs_id=%d, state=%s): %s\n",
					resp.GetBandcampId(), resp.GetDiscogsId(), resp.GetState().String(), resp.GetMessage())
			}
			return 0
		})

	case "reset":
		return withClient(ctx, stderr, dial, nil, func(client pb.BandcampServerServiceClient, _ pbrc.ClientUpdateServiceClient) int {
			if _, err := client.Reset(ctx, &pb.ResetRequest{}); err != nil {
				fmt.Fprintf(stderr, "Error in reset: %v\n", err)
				return 1
			}
			return 0
		})

	case "ping":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "Usage: bandcampserver_cli ping <instance_id>")
			return 1
		}
		id, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil {
			fmt.Fprintf(stderr, "Invalid instance id: %v\n", err)
			return 1
		}
		return withClient(ctx, stderr, dial, nil, func(_ pb.BandcampServerServiceClient, sclient pbrc.ClientUpdateServiceClient) int {
			if _, err := sclient.ClientUpdate(ctx, &pbrc.ClientUpdateRequest{InstanceId: id}); err != nil {
				fmt.Fprintf(stderr, "Error on GET: %v\n", err)
				return 1
			}
			return 0
		})

	default:
		fmt.Fprintf(stderr, "Unknown subcommand: %s\n", args[0])
		printUsage(stderr)
		return 1
	}
}

func main() {
	ctx, cancel := utils.ManualContext("bandcampserver-cli", time.Second*10)
	defer cancel()

	if code := run(ctx, os.Args[1:], os.Stdout, os.Stderr, defaultDial); code != 0 {
		os.Exit(code)
	}
}
