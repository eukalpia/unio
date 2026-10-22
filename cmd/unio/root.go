package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"github.com/spf13/cobra"

	"github.com/eukalpia/unio/internal/config"
	"github.com/eukalpia/unio/internal/obs"
)

// app carries the output streams and the environment for one invocation. It is
// passed explicitly so that tests can run every command in-process.
type app struct {
	stdout io.Writer
	stderr io.Writer
	env    map[string]string
}

// newRootCmd builds the command tree. Command construction is the only place
// where cobra objects are created.
func newRootCmd(a app) *cobra.Command {
	root := &cobra.Command{
		Use:           "unio",
		Short:         "Unio is a distributed, S3-compatible object store",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(a.stdout)
	root.SetErr(a.stderr)
	root.AddCommand(
		newServerCmd(a),
		newInitCmd(a),
		newVersionCmd(a),
		newConfigCmd(a),
		newHealthcheckCmd(a),
	)
	return root
}

// execute runs the CLI with args and returns the process exit code.
func execute(ctx context.Context, args []string, a app) int {
	root := newRootCmd(a)
	root.SetArgs(args)
	if err := root.ExecuteContext(ctx); err != nil {
		_, _ = fmt.Fprintf(a.stderr, "unio: %v\n", err)
		return 1
	}
	return 0
}

// addConfigFlags registers one flag per configuration key, plus --config.
// Secrets are not flags on purpose.
func addConfigFlags(cmd *cobra.Command) {
	cmd.Flags().String("config", "", "path to the YAML configuration file (or set UNIO_CONFIG)")
	for _, spec := range config.FlagSpecs() {
		cmd.Flags().String(spec.Name, "", spec.Usage+" (env "+spec.Env+")")
		if spec.Bool {
			cmd.Flags().Lookup(spec.Name).NoOptDefVal = "true"
		}
		if spec.Hidden {
			// Accepted only so that config reports the removal by name.
			_ = cmd.Flags().MarkHidden(spec.Name)
		}
	}
}

// changedFlags returns the configuration flags the user set explicitly, keyed by
// flag name. Unset flags are omitted, so they do not override other sources.
func changedFlags(cmd *cobra.Command) (map[string]string, error) {
	out := make(map[string]string)
	for _, spec := range config.FlagSpecs() {
		if !cmd.Flags().Changed(spec.Name) {
			continue
		}
		v, err := cmd.Flags().GetString(spec.Name)
		if err != nil {
			return nil, fmt.Errorf("flag --%s: %w", spec.Name, err)
		}
		out[spec.Name] = v
	}
	return out, nil
}

// loadConfig builds the effective configuration for cmd from the file, the
// environment and the explicitly set flags, and returns the warnings.
func (a app) loadConfig(cmd *cobra.Command) (*config.Config, []string, error) {
	path, err := cmd.Flags().GetString("config")
	if err != nil {
		return nil, nil, err
	}
	flags, err := changedFlags(cmd)
	if err != nil {
		return nil, nil, err
	}
	return config.Load(cmd.Context(), config.Options{Path: path, Env: a.env, Flags: flags})
}

// newLogger builds the node logger from the log settings.
func newLogger(w io.Writer, lc config.LogConfig) (*slog.Logger, error) {
	level, err := obs.ParseLevel(lc.Level)
	if err != nil {
		return nil, err
	}
	return obs.NewLogger(w, lc.Format, level)
}
