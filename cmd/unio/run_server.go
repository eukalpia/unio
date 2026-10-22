package main

import (
	"context"
	"errors"

	"github.com/eukalpia/unio/internal/config"
)

// serverNotAvailable is the message returned by runServer until the node
// assembly exists. It is the only placeholder in the CLI.
const serverNotAvailable = "unio server: the node assembly (internal/server) is not yet available in this build; " +
	"the configuration is valid, but the node cannot start yet"

// runServer starts the node with the validated configuration and blocks until
// ctx is cancelled. The node assembly lives in internal/server, which is not
// implemented yet, so this returns a clear "not yet available" error. Wiring
// the real server replaces only this function.
func runServer(ctx context.Context, _ *config.Config) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return errors.New(serverNotAvailable)
}
