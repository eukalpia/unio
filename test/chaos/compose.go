package chaos

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// dockerBinary is the only executable the harness starts for cluster control.
const dockerBinary = "docker"

// maxNodes caps the node count so service names stay inside the allow-list.
const maxNodes = 99

// awaitPollInterval is how often AwaitState polls the Compose status.
const awaitPollInterval = 200 * time.Millisecond

var (
	nodeServicePattern = regexp.MustCompile(`^node[1-9][0-9]*$`)
	containerIDPattern = regexp.MustCompile(`^[0-9a-f]{12,64}$`)
)

// ServiceStatus is the state of one Compose service's container.
type ServiceStatus struct {
	Service string
	State   string
	Health  string
}

// Cluster controls the node services of one Compose project. Every command is
// issued through a Runner with the Compose file and project given explicitly.
type Cluster struct {
	runner      Runner
	composeFile string
	project     string
	nodes       []string
}

// NewCluster returns a Cluster of nodeCount services named node1..nodeN.
// composeFile must be absolute and project must be a valid Compose project name.
func NewCluster(runner Runner, composeFile, project string, nodeCount int) (*Cluster, error) {
	if runner == nil {
		return nil, errors.New("chaos: nil runner")
	}
	if !filepath.IsAbs(composeFile) {
		return nil, fmt.Errorf("chaos: compose file %q must be an absolute path", composeFile)
	}
	if !projectPattern.MatchString(project) {
		return nil, fmt.Errorf("chaos: project %q must match %s", project, projectPattern)
	}
	if nodeCount < 1 || nodeCount > maxNodes {
		return nil, fmt.Errorf("chaos: node count %d outside 1..%d", nodeCount, maxNodes)
	}
	nodes := make([]string, nodeCount)
	for i := range nodes {
		nodes[i] = "node" + strconv.Itoa(i+1)
	}
	return &Cluster{runner: runner, composeFile: composeFile, project: project, nodes: nodes}, nil
}

// Nodes returns the node service names in order.
func (c *Cluster) Nodes() []string {
	return append([]string(nil), c.nodes...)
}

// Kill sends SIGKILL to one node service, the crash used by the scenarios.
func (c *Cluster) Kill(ctx context.Context, service string) error {
	if err := checkServices(service); err != nil {
		return err
	}
	return c.signal(ctx, service)
}

// KillAll sends SIGKILL to every node in a single command, so the nodes die
// as close to simultaneously as Docker allows.
func (c *Cluster) KillAll(ctx context.Context) error {
	return c.signal(ctx, c.nodes...)
}

// Start starts a stopped node service.
func (c *Cluster) Start(ctx context.Context, service string) error {
	if err := checkServices(service); err != nil {
		return err
	}
	_, err := c.docker(ctx, c.compose("start", service)...)
	return err
}

// StartAll starts every node service.
func (c *Cluster) StartAll(ctx context.Context) error {
	_, err := c.docker(ctx, c.compose(append([]string{"start"}, c.nodes...)...)...)
	return err
}

// Status lists the state of every container of the project, stopped ones included.
func (c *Cluster) Status(ctx context.Context) ([]ServiceStatus, error) {
	out, err := c.docker(ctx, c.compose("ps", "--all", "--format", "json")...)
	if err != nil {
		return nil, err
	}
	return parseServiceStates(out)
}

// AwaitState polls Status until service reports state or ctx is done. It is
// used to confirm that a kill took effect before the scenario relies on it.
func (c *Cluster) AwaitState(ctx context.Context, service, state string) error {
	if err := checkServices(service); err != nil {
		return err
	}
	ticker := time.NewTicker(awaitPollInterval)
	defer ticker.Stop()
	for {
		statuses, err := c.Status(ctx)
		if err != nil {
			return err
		}
		for _, s := range statuses {
			if s.Service == service && s.State == state {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("service %s did not reach state %q: %w", service, state, ctx.Err())
		case <-ticker.C:
		}
	}
}

// ContainerID returns the ID of the single running container of service.
func (c *Cluster) ContainerID(ctx context.Context, service string) (string, error) {
	if err := checkServices(service); err != nil {
		return "", err
	}
	out, err := c.docker(ctx, c.compose("ps", "-q", service)...)
	if err != nil {
		return "", err
	}
	ids := strings.Fields(string(out))
	if len(ids) != 1 {
		return "", fmt.Errorf("chaos: service %s has %d running containers, want 1", service, len(ids))
	}
	if !containerIDPattern.MatchString(ids[0]) {
		return "", fmt.Errorf("chaos: unexpected container id %q for service %s", ids[0], service)
	}
	return ids[0], nil
}

func (c *Cluster) signal(ctx context.Context, services ...string) error {
	if err := checkServices(services...); err != nil {
		return err
	}
	_, err := c.docker(ctx, c.compose(append([]string{"kill", "-s", "SIGKILL"}, services...)...)...)
	return err
}

// compose returns the argument list of `docker compose` for one subcommand.
func (c *Cluster) compose(sub ...string) []string {
	args := []string{"compose", "-p", c.project, "-f", c.composeFile}
	return append(args, sub...)
}

// docker runs the docker binary and wraps failures with the command line.
func (c *Cluster) docker(ctx context.Context, args ...string) ([]byte, error) {
	out, err := c.runner.Run(ctx, dockerBinary, args...)
	if err != nil {
		return nil, fmt.Errorf("docker %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

// checkServices rejects anything outside the node allow-list before it can
// reach a command line. At least one service is required.
func checkServices(services ...string) error {
	if len(services) == 0 {
		return errors.New("chaos: no service given")
	}
	for _, s := range services {
		if !nodeServicePattern.MatchString(s) {
			return fmt.Errorf("chaos: %q is not a node service", s)
		}
	}
	return nil
}

// composePSRow is one entry of `docker compose ps --format json`.
type composePSRow struct {
	Service string `json:"Service"`
	State   string `json:"State"`
	Health  string `json:"Health"`
}

// parseServiceStates accepts both output forms used by Compose releases: a
// single JSON array, and one JSON object per line.
func parseServiceStates(out []byte) ([]ServiceStatus, error) {
	trimmed := bytes.TrimSpace(out)
	var rows []composePSRow
	if len(trimmed) > 0 && trimmed[0] == '[' {
		if err := json.Unmarshal(trimmed, &rows); err != nil {
			return nil, fmt.Errorf("chaos: parse compose status: %w", err)
		}
	} else {
		dec := json.NewDecoder(bytes.NewReader(trimmed))
		for {
			var row composePSRow
			err := dec.Decode(&row)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return nil, fmt.Errorf("chaos: parse compose status: %w", err)
			}
			rows = append(rows, row)
		}
	}
	var states []ServiceStatus
	for _, r := range rows {
		states = append(states, ServiceStatus(r))
	}
	return states, nil
}
