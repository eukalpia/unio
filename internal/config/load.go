package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"go.yaml.in/yaml/v3"
)

// maxConfigFileSize bounds the YAML configuration file.
const maxConfigFileSize = 1 << 20

// Options selects the sources that Load combines.
type Options struct {
	// Path is the YAML file to read. When empty, UNIO_CONFIG from Env is
	// used. When both are empty, no file is read.
	Path string
	// Env is the environment, usually built from os.Environ. It is only read.
	Env map[string]string
	// Flags holds the flags the user set explicitly, keyed by flag name
	// without dashes. Flags that were not set must be absent, so they do not
	// override other sources.
	Flags map[string]string
}

// Load builds the effective configuration from defaults, the YAML file, the
// environment and flags, in that order. It validates the result and returns
// the warnings along with the configuration. When validation fails, Load
// returns every problem it found in one error.
func Load(ctx context.Context, opts Options) (*Config, []string, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	cfg := Defaults()
	path := opts.Path
	if path == "" {
		path = opts.Env[envConfigPath]
	}
	if path != "" {
		if err := readConfigFile(ctx, path, &cfg); err != nil {
			return nil, nil, err
		}
	}
	if err := applyEnv(&cfg, opts.Env); err != nil {
		return nil, nil, err
	}
	if err := applySecrets(ctx, &cfg, opts.Env); err != nil {
		return nil, nil, err
	}
	if err := applyFlags(&cfg, opts.Flags); err != nil {
		return nil, nil, err
	}
	warnings := unknownEnvWarnings(opts.Env)
	validationWarnings, err := Validate(&cfg)
	warnings = append(warnings, validationWarnings...)
	if err != nil {
		return nil, warnings, err
	}
	drives, err := ExpandEndpoints(cfg.Cluster.Endpoints)
	if err != nil {
		return nil, warnings, fmt.Errorf("cluster.endpoints: %w", err)
	}
	cfg.Cluster.Drives = drives
	return &cfg, warnings, nil
}

// readConfigFile decodes the YAML file at path into cfg. Decoding is strict:
// unknown keys are errors, and the file must hold at most one document. An
// empty file is valid and changes nothing.
func readConfigFile(ctx context.Context, path string, cfg *Config) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return fmt.Errorf("cannot open config file %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxConfigFileSize+1))
	if err != nil {
		return fmt.Errorf("cannot read config file %s: %w", path, err)
	}
	if len(data) > maxConfigFileSize {
		return fmt.Errorf("config file %s is larger than %d bytes", path, maxConfigFileSize)
	}
	if err := checkRemovedYAML(path, data); err != nil {
		return err
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return fmt.Errorf("config file %s: %w", path, err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("config file %s: must contain exactly one YAML document", path)
	}
	return nil
}

// applyFlags applies explicitly set flags. Unknown names are errors, so a
// typo never silently falls back to a default.
func applyFlags(c *Config, flags map[string]string) error {
	names := make([]string, 0, len(flags))
	for name := range flags {
		names = append(names, name)
	}
	sort.Strings(names)
	byName := make(map[string]setting)
	for _, s := range settings() {
		byName[s.flag] = s
	}
	for _, name := range names {
		if err := checkRemovedFlag(name); err != nil {
			return err
		}
		s, ok := byName[name]
		if !ok {
			return fmt.Errorf("unknown flag --%s", name)
		}
		if err := s.set(c, flags[name]); err != nil {
			return fmt.Errorf("flag --%s: %w", name, err)
		}
	}
	return nil
}
