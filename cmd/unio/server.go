package main

import (
	"fmt"
	"io"
	"log/slog"

	"github.com/spf13/cobra"
)

// newServerCmd builds "unio server", which loads and validates the
// configuration, logs its warnings, and starts the node.
func newServerCmd(a app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "server",
		Short: "run a Unio node",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, warnings, err := a.loadConfig(cmd)
			if err != nil {
				return err
			}
			logger, err := newLogger(a.stderr, cfg.Log)
			if err != nil {
				return err
			}
			reportWarnings(logger, warnings)
			return runServer(cmd.Context(), cfg, logger)
		},
	}
	addConfigFlags(cmd)
	return cmd
}

// reportWarnings logs every configuration warning.
func reportWarnings(logger *slog.Logger, warnings []string) {
	for _, warning := range warnings {
		logger.Warn("configuration warning", "detail", warning)
	}
}

// printWarnings writes configuration warnings to w, one per line.
func printWarnings(w io.Writer, warnings []string) {
	for _, warning := range warnings {
		_, _ = fmt.Fprintf(w, "warning: %s\n", warning)
	}
}
