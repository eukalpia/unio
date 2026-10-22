// Command unio-bench measures an S3-compatible endpoint with a fixed set of
// workloads and writes the results as JSON, and optionally as Markdown. The
// same workloads run unchanged against Unio and MinIO. See bench/compare.md.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/eukalpia/unio/bench/internal/harness"
	"github.com/eukalpia/unio/bench/internal/objstore"
	"github.com/eukalpia/unio/bench/internal/report"
)

// Exit codes. Interruption is distinct so scripts can detect partial results.
const (
	exitOK          = 0
	exitFailure     = 1
	exitUsage       = 2
	exitInterrupted = 130
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// run executes the benchmark and returns the process exit code. Results are
// written even when the run fails part way, so whatever was measured is kept.
func run(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	cfg, err := parseConfig(args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return exitOK
	}
	if err != nil {
		fmt.Fprintf(stderr, "unio-bench: %v\n", err)
		return exitUsage
	}
	creds, err := readCredentials(getenv)
	if err != nil {
		fmt.Fprintf(stderr, "unio-bench: %v\n", err)
		return exitUsage
	}
	store, err := objstore.NewAWS(objstore.Options{
		Endpoint:    cfg.endpoint,
		Region:      cfg.region,
		PathStyle:   cfg.pathStyle,
		Credentials: creds,
		Concurrency: cfg.concurrency,
	})
	if err != nil {
		fmt.Fprintf(stderr, "unio-bench: %v\n", err)
		return exitUsage
	}

	rep, runErr := harness.Run(ctx, store, harness.Options{
		Endpoint:    cfg.endpoint,
		Region:      cfg.region,
		PathStyle:   cfg.pathStyle,
		Bucket:      cfg.bucket,
		Workloads:   cfg.workloads,
		Concurrency: cfg.concurrency,
		Warmup:      cfg.warmup,
		Duration:    cfg.duration,
		ListKeys:    cfg.listKeys,
		ListPage:    cfg.listPage,
		Seed:        cfg.seed,
		Progress:    stderr,
	})

	if err := writeResults(cfg, rep, stdout); err != nil {
		fmt.Fprintf(stderr, "unio-bench: write results: %v\n", err)
		return exitFailure
	}
	if runErr != nil {
		fmt.Fprintf(stderr, "unio-bench: %v\n", runErr)
		return exitFailure
	}
	if rep.Interrupted {
		fmt.Fprintln(stderr, "unio-bench: interrupted; partial results were written")
		return exitInterrupted
	}
	return exitOK
}

// writeResults writes the JSON report to cfg.out (or stdout) and, when
// requested, the Markdown report to cfg.markdown.
func writeResults(cfg config, rep report.Report, stdout io.Writer) error {
	if cfg.out == "" {
		if err := report.WriteJSON(stdout, rep); err != nil {
			return err
		}
	} else if err := writeFile(cfg.out, func(w io.Writer) error { return report.WriteJSON(w, rep) }); err != nil {
		return err
	}
	if cfg.markdown != "" {
		return writeFile(cfg.markdown, func(w io.Writer) error { return report.WriteMarkdown(w, rep) })
	}
	return nil
}

// writeFile creates path, runs write on it and reports the first error,
// including the error from closing the file.
func writeFile(path string, write func(io.Writer) error) error {
	// #nosec G304 -- the path is the operator's own report file, named by -out or -markdown; it is cleaned and only written
	f, err := os.Create(filepath.Clean(path))
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	werr := write(f)
	cerr := f.Close()
	if werr != nil {
		return fmt.Errorf("write %s: %w", path, werr)
	}
	if cerr != nil {
		return fmt.Errorf("close %s: %w", path, cerr)
	}
	return nil
}
