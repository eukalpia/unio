package chaos

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

const testProject = "unio-4x4"

// composeArgv is the full expected argv for a compose subcommand.
func composeArgv(file string, sub ...string) []string {
	return append([]string{"docker", "compose", "-p", testProject, "-f", file}, sub...)
}

func TestNewClusterValidation(t *testing.T) {
	file := filepath.Join(t.TempDir(), "cluster-4x4.yml")
	tests := []struct {
		name    string
		file    string
		project string
		nodes   int
		wantErr bool
	}{
		{"valid", file, testProject, 4, false},
		{"relative compose file", "deploy/cluster.yml", testProject, 4, true},
		{"empty compose file", "", testProject, 4, true},
		{"flag-like compose file", "-f", testProject, 4, true},
		{"project with space", file, "a b", 4, true},
		{"project with leading dash", file, "-x", 4, true},
		{"zero nodes", file, testProject, 0, true},
		{"node count beyond allow-list", file, testProject, 100, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewCluster(&fakeRunner{}, tt.file, tt.project, tt.nodes)
			if (err != nil) != tt.wantErr {
				t.Fatalf("NewCluster error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
	if _, err := NewCluster(nil, file, testProject, 4); err == nil {
		t.Error("nil runner accepted")
	}
}

func TestClusterNodes(t *testing.T) {
	c, err := NewCluster(&fakeRunner{}, filepath.Join(t.TempDir(), "c.yml"), testProject, 3)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"node1", "node2", "node3"}
	got := c.Nodes()
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("Nodes mismatch (-want +got):\n%s", diff)
	}
	got[0] = "tampered"
	if c.Nodes()[0] != "node1" {
		t.Fatal("Nodes returned internal slice")
	}
}

func TestClusterCommands(t *testing.T) {
	tests := []struct {
		name string
		run  func(c *Cluster) error
		sub  []string
	}{
		{
			name: "kill one node",
			run:  func(c *Cluster) error { return c.Kill(context.Background(), "node2") },
			sub:  []string{"kill", "-s", "SIGKILL", "node2"},
		},
		{
			name: "kill every node at once",
			run:  func(c *Cluster) error { return c.KillAll(context.Background()) },
			sub:  []string{"kill", "-s", "SIGKILL", "node1", "node2", "node3", "node4"},
		},
		{
			name: "start one node",
			run:  func(c *Cluster) error { return c.Start(context.Background(), "node3") },
			sub:  []string{"start", "node3"},
		},
		{
			name: "start every node",
			run:  func(c *Cluster) error { return c.StartAll(context.Background()) },
			sub:  []string{"start", "node1", "node2", "node3", "node4"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fr := &fakeRunner{}
			file := filepath.Join(t.TempDir(), "cluster-4x4.yml")
			c, err := NewCluster(fr, file, testProject, 4)
			if err != nil {
				t.Fatal(err)
			}
			if err := tt.run(c); err != nil {
				t.Fatal(err)
			}
			calls := fr.argvs()
			if len(calls) != 1 {
				t.Fatalf("got %d commands, want 1", len(calls))
			}
			if diff := cmp.Diff(composeArgv(file, tt.sub...), calls[0]); diff != "" {
				t.Fatalf("argv mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestClusterRejectsInvalidServices(t *testing.T) {
	tests := []struct {
		name string
		run  func(c *Cluster) error
	}{
		{"kill node0", func(c *Cluster) error { return c.Kill(context.Background(), "node0") }},
		{"kill node with leading zero", func(c *Cluster) error { return c.Kill(context.Background(), "node01") }},
		{"kill bare prefix", func(c *Cluster) error { return c.Kill(context.Background(), "node") }},
		{"kill with separator", func(c *Cluster) error { return c.Kill(context.Background(), "node2 node3") }},
		{"kill with shell meta", func(c *Cluster) error { return c.Kill(context.Background(), "node2;rm") }},
		{"kill empty", func(c *Cluster) error { return c.Kill(context.Background(), "") }},
		{"start path traversal", func(c *Cluster) error { return c.Start(context.Background(), "../node1") }},
		{"await invalid", func(c *Cluster) error { return c.AwaitState(context.Background(), "db", "exited") }},
		{"container id invalid", func(c *Cluster) error {
			_, err := c.ContainerID(context.Background(), "node1x")
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fr := &fakeRunner{}
			c, err := NewCluster(fr, filepath.Join(t.TempDir(), "c.yml"), testProject, 4)
			if err != nil {
				t.Fatal(err)
			}
			if err := tt.run(c); err == nil {
				t.Fatal("invalid service accepted")
			}
			if n := len(fr.argvs()); n != 0 {
				t.Fatalf("validation failed open: %d command(s) ran", n)
			}
		})
	}
}

func TestParseServiceStates(t *testing.T) {
	tests := []struct {
		name    string
		out     string
		want    []ServiceStatus
		wantErr bool
	}{
		{
			name: "array form",
			out:  `[{"Service":"node1","State":"running","Health":"healthy"},{"Service":"node2","State":"exited"}]`,
			want: []ServiceStatus{{Service: "node1", State: "running", Health: "healthy"}, {Service: "node2", State: "exited"}},
		},
		{
			name: "newline-delimited form",
			out:  "{\"Service\":\"node1\",\"State\":\"running\"}\n{\"Service\":\"node2\",\"State\":\"exited\"}\n",
			want: []ServiceStatus{{Service: "node1", State: "running"}, {Service: "node2", State: "exited"}},
		},
		{name: "no containers", out: "", want: nil},
		{name: "malformed", out: "not json", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseServiceStates([]byte(tt.out))
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Fatalf("states mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestStatusCommand(t *testing.T) {
	fr := &fakeRunner{reply: func([]string) ([]byte, error) {
		return []byte(`[{"Service":"node1","State":"running"}]`), nil
	}}
	file := filepath.Join(t.TempDir(), "c.yml")
	c, err := NewCluster(fr, file, testProject, 4)
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]ServiceStatus{{Service: "node1", State: "running"}}, got); diff != "" {
		t.Fatalf("Status mismatch (-want +got):\n%s", diff)
	}
	want := composeArgv(file, "ps", "--all", "--format", "json")
	if diff := cmp.Diff(want, fr.argvs()[0]); diff != "" {
		t.Fatalf("argv mismatch (-want +got):\n%s", diff)
	}
}

func TestContainerID(t *testing.T) {
	tests := []struct {
		name    string
		out     string
		want    string
		wantErr bool
	}{
		{name: "single id", out: "abcdef123456\n", want: "abcdef123456"},
		{name: "full length id", out: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef\n", want: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"},
		{name: "not running", out: "", wantErr: true},
		{name: "two containers", out: "abcdef123456\nfedcba654321\n", wantErr: true},
		{name: "uppercase", out: "ABCDEF123456\n", wantErr: true},
		{name: "shell metacharacters", out: "abc;rm -rf\n", wantErr: true},
		{name: "too short", out: "abc\n", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fr := &fakeRunner{reply: func([]string) ([]byte, error) { return []byte(tt.out), nil }}
			file := filepath.Join(t.TempDir(), "c.yml")
			c, err := NewCluster(fr, file, testProject, 4)
			if err != nil {
				t.Fatal(err)
			}
			got, err := c.ContainerID(context.Background(), "node1")
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("id = %q, want %q", got, tt.want)
			}
			if diff := cmp.Diff(composeArgv(file, "ps", "-q", "node1"), fr.argvs()[0]); diff != "" {
				t.Fatalf("argv mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestAwaitStatePollsUntilState(t *testing.T) {
	calls := 0
	fr := &fakeRunner{reply: func([]string) ([]byte, error) {
		calls++
		if calls == 1 {
			return []byte(`[{"Service":"node1","State":"running"}]`), nil
		}
		return []byte(`[{"Service":"node1","State":"exited"}]`), nil
	}}
	c, err := NewCluster(fr, filepath.Join(t.TempDir(), "c.yml"), testProject, 4)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.AwaitState(ctx, "node1", "exited"); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("polled %d times, want 2", calls)
	}
}

func TestAwaitStateTimesOut(t *testing.T) {
	fr := &fakeRunner{reply: func([]string) ([]byte, error) {
		return []byte(`[{"Service":"node1","State":"running"}]`), nil
	}}
	c, err := NewCluster(fr, filepath.Join(t.TempDir(), "c.yml"), testProject, 4)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	err = c.AwaitState(ctx, "node1", "exited")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want deadline exceeded", err)
	}
}
