package main

import (
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"

	"github.com/eukalpia/unio/internal/config"
)

// insecureWarningPrefix marks the warning printed for insecure internode traffic.
const insecureWarningPrefix = "INSECURE INTERNODE TRAFFIC"

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
			reportWarnings(a.stderr, logger, warnings)
			return runServer(cmd.Context(), cfg)
		},
	}
	addConfigFlags(cmd)
	return cmd
}

// reportWarnings logs every configuration warning. The insecure internode
// warning is also printed as a banner, because it must not be missed.
func reportWarnings(w io.Writer, logger *slog.Logger, warnings []string) {
	for _, warning := range warnings {
		if strings.HasPrefix(warning, insecureWarningPrefix) {
			writeBanner(w, warning)
		}
		logger.Warn("configuration warning", "detail", warning)
	}
}

// printWarnings writes configuration warnings to w, one per line.
func printWarnings(w io.Writer, warnings []string) {
	for _, warning := range warnings {
		if strings.HasPrefix(warning, insecureWarningPrefix) {
			writeBanner(w, warning)
			continue
		}
		_, _ = fmt.Fprintf(w, "warning: %s\n", warning)
	}
}

// writeBanner prints a warning in a visible frame.
func writeBanner(w io.Writer, text string) {
	rule := strings.Repeat("!", 78)
	_, _ = fmt.Fprintf(w, "%s\n!! %s\n%s\n", rule, text, rule)
}

// newConfigCmd builds "unio config", which validates and prints the configuration.
func newConfigCmd(a app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "validate and print the effective configuration",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(newConfigValidateCmd(a), newConfigPrintCmd(a))
	return cmd
}

// newConfigValidateCmd builds "unio config validate".
func newConfigValidateCmd(a app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "check the configuration and report every error and warning",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, warnings, err := a.loadConfig(cmd)
			if err != nil {
				return err
			}
			printWarnings(a.stderr, warnings)
			_, err = fmt.Fprintln(a.stdout, "configuration is valid")
			return err
		},
	}
	addConfigFlags(cmd)
	return cmd
}

// newConfigPrintCmd builds "unio config print", which prints the effective
// configuration as YAML. Secrets are printed as [REDACTED].
func newConfigPrintCmd(a app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "print",
		Short: "print the effective configuration with secrets redacted",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, warnings, err := a.loadConfig(cmd)
			if err != nil {
				return err
			}
			printWarnings(a.stderr, warnings)
			out, err := renderConfig(cfg)
			if err != nil {
				return err
			}
			_, err = a.stdout.Write(out)
			return err
		},
	}
	addConfigFlags(cmd)
	return cmd
}

// renderConfig returns the YAML form of cfg, with every secret redacted by its MarshalYAML.
func renderConfig(cfg *config.Config) ([]byte, error) {
	out, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("render configuration: %w", err)
	}
	return out, nil
}
