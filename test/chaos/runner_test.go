package chaos

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestExecRunner(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(helperEnv, "echo")

	tests := []struct {
		name       string
		args       []string
		wantStdout string
		wantErr    string
	}{
		{
			name:       "arguments are passed literally without a shell",
			args:       []string{"plain", "a b", "$(id)", "x;y", "--flag"},
			wantStdout: strings.Join([]string{"plain", "a b", "$(id)", "x;y", "--flag"}, helperArgSeparator),
		},
		{
			name:       "non-zero exit surfaces stderr and keeps stdout",
			args:       []string{"fail"},
			wantStdout: "fail",
			wantErr:    "boom",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := NewExecRunner().Run(context.Background(), exe, tt.args...)
			if got := string(out); got != tt.wantStdout {
				t.Fatalf("stdout = %q, want %q", got, tt.wantStdout)
			}
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestExecRunnerHonoursCancellation(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(helperEnv, "echo")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewExecRunner().Run(ctx, exe, "x"); err == nil {
		t.Fatal("expected an error from a cancelled context")
	}
}
