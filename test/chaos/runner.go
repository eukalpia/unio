package chaos

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// waitDelay bounds how long a command may keep its output pipes open after
// its context is cancelled.
const waitDelay = 10 * time.Second

// Runner runs an external command and returns its standard output.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (stdout []byte, err error)
}

type execRunner struct{}

// NewExecRunner returns a Runner that starts processes with os/exec. Commands
// are never passed through a shell, so arguments are not subject to shell
// expansion.
func NewExecRunner() Runner {
	return execRunner{}
}

// Run starts name with args and waits for it to exit. On failure the error
// includes the command's stderr so the cause shows up in test output. Stdout
// is returned even when the command fails.
func (execRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	// #nosec G204 -- name is the docker CLI or a test binary chosen by this package's callers; args come from validated values and no shell is involved
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.WaitDelay = waitDelay
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return stdout.Bytes(), fmt.Errorf("%s: %w: %s", name, err, msg)
		}
		return stdout.Bytes(), fmt.Errorf("%s: %w", name, err)
	}
	return stdout.Bytes(), nil
}
