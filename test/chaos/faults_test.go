package chaos

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func newTestFaults(t *testing.T) (*Faults, *fakeRunner) {
	t.Helper()
	fr := &fakeRunner{}
	f, err := NewFaults(fr, testProject)
	if err != nil {
		t.Fatal(err)
	}
	return f, fr
}

func TestCorruptCommand(t *testing.T) {
	f, fr := newTestFaults(t)
	if err := f.Corrupt(context.Background(), "unio-4x4_node1-data1", "/data1/unio/shard/0001"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"docker", "run", "--rm", "--network", "none",
		"-v", "unio-4x4_node1-data1:/data1",
		helperImage,
		"sh", "-c", corruptScript, "_", "/data1/unio/shard/0001",
	}
	calls := fr.argvs()
	if len(calls) != 1 {
		t.Fatalf("got %d commands, want 1", len(calls))
	}
	if diff := cmp.Diff(want, calls[0]); diff != "" {
		t.Fatalf("argv mismatch (-want +got):\n%s", diff)
	}
}

func TestDeleteCommand(t *testing.T) {
	f, fr := newTestFaults(t)
	if err := f.Delete(context.Background(), "unio-4x4_node2-data3", "/data3/unio/shard/0002"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"docker", "run", "--rm", "--network", "none",
		"-v", "unio-4x4_node2-data3:/data3",
		helperImage,
		"rm", "-f", "--", "/data3/unio/shard/0002",
	}
	if diff := cmp.Diff(want, fr.argvs()[0]); diff != "" {
		t.Fatalf("argv mismatch (-want +got):\n%s", diff)
	}
}

func TestFaultInputValidation(t *testing.T) {
	const goodVolume = "unio-4x4_node1-data1"
	tests := []struct {
		name   string
		volume string
		path   string
	}{
		{"volume of another project", "other_node1-data1", "/data1/x"},
		{"node zero", "unio-4x4_node0-data1", "/data1/x"},
		{"node without number", "unio-4x4_node-data1", "/data1/x"},
		{"volume with mount option", "unio-4x4_node1-data1,ro", "/data1/x"},
		{"volume with colon", "unio-4x4_node1-data1:/", "/data1/x"},
		{"path on another drive", goodVolume, "/data2/x"},
		{"path outside data mounts", goodVolume, "/etc/passwd"},
		{"path traversal", goodVolume, "/data1/../etc/passwd"},
		{"path with shell metacharacters", goodVolume, "/data1/x;rm -rf /"},
		{"path naming a directory root", goodVolume, "/data1/"},
		{"path without drive prefix", goodVolume, "data1/x"},
	}
	for _, tt := range tests {
		t.Run("corrupt "+tt.name, func(t *testing.T) {
			f, fr := newTestFaults(t)
			if err := f.Corrupt(context.Background(), tt.volume, tt.path); err == nil {
				t.Fatal("invalid input accepted")
			}
			if n := len(fr.argvs()); n != 0 {
				t.Fatalf("%d command(s) ran", n)
			}
		})
		t.Run("delete "+tt.name, func(t *testing.T) {
			f, fr := newTestFaults(t)
			if err := f.Delete(context.Background(), tt.volume, tt.path); err == nil {
				t.Fatal("invalid input accepted")
			}
			if n := len(fr.argvs()); n != 0 {
				t.Fatalf("%d command(s) ran", n)
			}
		})
	}
}

// TestCorruptScriptBehaviour runs the real corruption template under sh on the
// host. It checks the byte-level effect that the fake-runner tests cannot see.
// The script needs GNU coreutils (stat -c), so the test is skipped elsewhere.
func TestCorruptScriptBehaviour(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not available")
	}
	runScript := func(t *testing.T, path string) ([]byte, error) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		// Test-only: the template is executed directly to check its logic.
		cmd := exec.CommandContext(ctx, sh, "-c", corruptScript, "_", path)
		return cmd.CombinedOutput()
	}

	t.Run("flips exactly one byte and keeps the size", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "shard")
		orig := bytes.Repeat([]byte{0x41}, 4096)
		if err := os.WriteFile(path, orig, 0o600); err != nil {
			t.Fatal(err)
		}
		if out, err := runScript(t, path); err != nil {
			if strings.Contains(string(out), "stat: unrecognized option") {
				t.Skip("corruption template needs GNU coreutils")
			}
			t.Fatalf("script failed: %v: %s", err, out)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(orig) {
			t.Fatalf("size changed from %d to %d", len(orig), len(got))
		}
		var changed []int
		for i := range got {
			if got[i] != orig[i] {
				changed = append(changed, i)
			}
		}
		if len(changed) != 1 {
			t.Fatalf("%d bytes changed, want exactly 1", len(changed))
		}
		if want := orig[changed[0]] + 1; got[changed[0]] != want {
			t.Fatalf("byte %d = %#x, want %#x", changed[0], got[changed[0]], want)
		}
	})

	failures := []struct {
		name  string
		setup func(t *testing.T) string
	}{
		{"missing file", func(t *testing.T) string { return filepath.Join(t.TempDir(), "absent") }},
		{"empty file", func(t *testing.T) string {
			p := filepath.Join(t.TempDir(), "empty")
			if err := os.WriteFile(p, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			return p
		}},
	}
	for _, tt := range failures {
		t.Run("refuses "+tt.name, func(t *testing.T) {
			path := tt.setup(t)
			if out, err := runScript(t, path); err == nil {
				t.Fatalf("script succeeded on %s: %s", tt.name, out)
			}
		})
	}
}

func TestNewFaultsValidatesProject(t *testing.T) {
	if _, err := NewFaults(&fakeRunner{}, "bad project"); err == nil {
		t.Fatal("invalid project accepted")
	}
	if _, err := NewFaults(nil, testProject); err == nil {
		t.Fatal("nil runner accepted")
	}
}
