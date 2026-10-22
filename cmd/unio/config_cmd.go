package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"

	"github.com/eukalpia/unio/internal/config"
)

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
