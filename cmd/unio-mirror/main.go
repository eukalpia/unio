// Command unio-mirror copies buckets from an S3-compatible store, such as
// MinIO, to Unio and verifies the copy. See package mirror.
//
//	UNIO_MIRROR_FROM_ACCESS_KEY=... UNIO_MIRROR_FROM_SECRET_KEY=... \
//	UNIO_MIRROR_TO_ACCESS_KEY=...   UNIO_MIRROR_TO_SECRET_KEY=...   \
//	unio-mirror -from http://minio:9000 -to http://unio:9000 [-bucket b ...] [-verify]
//
// Credentials are read from the environment only, so they never appear in a
// process listing or a shell history. The exit status is 0 when the
// destination matches the source, 1 when objects failed or differ, and 2 on
// a usage or connection error.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/eukalpia/unio/internal/mirror"
)

type bucketList []string

func (b *bucketList) String() string { return strings.Join(*b, ",") }
func (b *bucketList) Set(v string) error {
	if v == "" {
		return errors.New("empty bucket name")
	}
	*b = append(*b, v)
	return nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("unio-mirror", flag.ContinueOnError)
	fs.SetOutput(stderr)
	from := fs.String("from", "", "source S3 endpoint URL, for example http://minio:9000 (required)")
	to := fs.String("to", "", "destination S3 endpoint URL, for example http://unio:9000 (required)")
	fromRegion := fs.String("from-region", "us-east-1", "signing region of the source")
	toRegion := fs.String("to-region", "us-east-1", "signing region of the destination")
	workers := fs.Int("workers", 8, "objects copied at once")
	verify := fs.Bool("verify", false, "compare only; write nothing")
	skipPolicies := fs.Bool("skip-policies", false, "do not copy or compare bucket policies")
	jsonOut := fs.Bool("json", false, "print the report as JSON")
	var buckets bucketList
	fs.Var(&buckets, "bucket", "mirror only this bucket (repeatable); default every bucket of the source")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *from == "" || *to == "" || fs.NArg() != 0 {
		fmt.Fprintln(stderr, "unio-mirror: -from and -to are required")
		fs.Usage()
		return 2
	}
	if *workers < 1 || *workers > 256 {
		fmt.Fprintln(stderr, "unio-mirror: -workers must be between 1 and 256")
		return 2
	}
	src, err := mirror.NewS3(mirror.Endpoint{URL: *from, Region: *fromRegion,
		AccessKey: getenv("UNIO_MIRROR_FROM_ACCESS_KEY"), SecretKey: getenv("UNIO_MIRROR_FROM_SECRET_KEY")})
	if err != nil {
		fmt.Fprintf(stderr, "unio-mirror: source: %v (UNIO_MIRROR_FROM_ACCESS_KEY, UNIO_MIRROR_FROM_SECRET_KEY)\n", err)
		return 2
	}
	dst, err := mirror.NewS3(mirror.Endpoint{URL: *to, Region: *toRegion,
		AccessKey: getenv("UNIO_MIRROR_TO_ACCESS_KEY"), SecretKey: getenv("UNIO_MIRROR_TO_SECRET_KEY")})
	if err != nil {
		fmt.Fprintf(stderr, "unio-mirror: destination: %v (UNIO_MIRROR_TO_ACCESS_KEY, UNIO_MIRROR_TO_SECRET_KEY)\n", err)
		return 2
	}
	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	rep, err := mirror.Run(ctx, src, dst, mirror.Options{
		Buckets: buckets, Workers: *workers, Verify: *verify, SkipPolicies: *skipPolicies, Logger: logger,
	})
	if *jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(rep)
	} else {
		mode := "mirrored"
		if *verify {
			mode = "verified"
		}
		fmt.Fprintf(stdout, "%s %d buckets, %d objects: %d copied (%d bytes), %d already equal, %d missing, %d different, %d failed, %d only in the destination; %d bucket policies in place\n",
			mode, rep.Buckets, rep.Objects, rep.Copied, rep.Bytes, rep.Skipped, rep.Missing, rep.Different, rep.Failed, rep.Extra, rep.Policies)
		for _, f := range rep.Failures {
			fmt.Fprintln(stdout, "  failed:", f)
		}
	}
	if err != nil {
		fmt.Fprintf(stderr, "unio-mirror: %v\n", err)
		return 2
	}
	if !rep.OK() {
		return 1
	}
	return 0
}
