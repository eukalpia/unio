package chaos

import (
	"context"
	"sync"
)

// fakeRunner records every command it is asked to run and answers from an
// optional scripted reply. argv[0] is the binary name.
type fakeRunner struct {
	mu    sync.Mutex
	calls [][]string
	reply func(argv []string) ([]byte, error)
}

func (f *fakeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	argv := append([]string{name}, args...)
	f.mu.Lock()
	f.calls = append(f.calls, argv)
	reply := f.reply
	f.mu.Unlock()
	if reply == nil {
		return nil, nil
	}
	return reply(argv)
}

// argvs returns a copy of every recorded command line.
func (f *fakeRunner) argvs() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([][]string, len(f.calls))
	copy(out, f.calls)
	return out
}
