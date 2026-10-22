package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/spf13/cobra"
)

// defaultHealthURL is the liveness endpoint of a node on its S3 port.
const defaultHealthURL = "http://127.0.0.1:9000/unio/health/live"

// healthTimeout bounds one probe, so a hung node fails the container health check.
const healthTimeout = 3 * time.Second

// newHealthcheckCmd builds "unio healthcheck", the probe used by the container
// health check. It exits 0 when the liveness endpoint answers 200, and 1 otherwise.
func newHealthcheckCmd(_ app) *cobra.Command {
	var target string
	cmd := &cobra.Command{
		Use:   "healthcheck",
		Short: "probe the liveness endpoint (used by container health checks)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return probe(cmd.Context(), target)
		},
	}
	cmd.Flags().StringVar(&target, "url", defaultHealthURL, "liveness URL to probe")
	return cmd
}

// probe performs one GET of url and returns an error unless the status is 200.
func probe(ctx context.Context, target string) error {
	ctx, cancel := context.WithTimeout(ctx, healthTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return fmt.Errorf("health probe: invalid URL: %w", err)
	}
	client := &http.Client{Timeout: healthTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("health probe %s failed: %w", target, err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health probe %s returned %d", target, resp.StatusCode)
	}
	return nil
}
