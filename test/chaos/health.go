package chaos

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// readyPath is the node endpoint that answers 200 once the node is ready.
const readyPath = "/unio/health/ready"

// probeTimeout bounds one readiness probe.
const probeTimeout = 2 * time.Second

// WaitReady polls the readiness endpoint of endpoint every interval until it
// answers 200 OK. The deadline comes from ctx.
func WaitReady(ctx context.Context, endpoint string, interval time.Duration) error {
	if interval <= 0 {
		return errors.New("chaos: poll interval must be positive")
	}
	target := strings.TrimSuffix(endpoint, "/") + readyPath
	client := &http.Client{Timeout: probeTimeout}
	defer client.CloseIdleConnections()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		lastErr := probeReady(ctx, client, target)
		if lastErr == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s not ready: %w (last probe: %v)", endpoint, ctx.Err(), lastErr)
		case <-ticker.C:
		}
	}
}

func probeReady(ctx context.Context, client *http.Client, target string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}
