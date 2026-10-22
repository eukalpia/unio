package config

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// envPrefix is the prefix of every environment variable Unio reads.
const envPrefix = "UNIO_"

// envConfigPath names the variable that gives the YAML file path.
const envConfigPath = "UNIO_CONFIG"

// maxSecretFileSize bounds how much a *_FILE secret may contain.
const maxSecretFileSize = 4 << 10

// secretSetting binds a secret to its environment variable. Secrets are
// accepted only from the environment, directly or through NAME_FILE.
type secretSetting struct {
	env string
	set func(c *Config, v string)
}

// secretSettings returns the secret keys. The table is rebuilt on each call.
func secretSettings() []secretSetting {
	return []secretSetting{
		{env: "UNIO_CLUSTER_SECRET", set: func(c *Config, v string) { c.Cluster.Secret = NewSecret(v) }},
		{env: "UNIO_ROOT_USER", set: func(c *Config, v string) { c.Auth.RootUser = NewSecret(v) }},
		{env: "UNIO_ROOT_PASSWORD", set: func(c *Config, v string) { c.Auth.RootPassword = NewSecret(v) }},
		{env: "UNIO_METRICS_TOKEN", set: func(c *Config, v string) { c.Metrics.Token = NewSecret(v) }},
	}
}

// applyEnv applies every non-secret UNIO_* variable that is set to a non-empty value.
// An empty variable counts as unset, so that unset compose substitutions do not
// clear values from the file.
func applyEnv(c *Config, env map[string]string) error {
	for _, s := range settings() {
		v := env[s.env]
		if v == "" {
			continue
		}
		if err := s.set(c, v); err != nil {
			return fmt.Errorf("environment variable %s: %w", s.env, err)
		}
	}
	return nil
}

// applySecrets applies the secret keys from the environment. Each secret
// comes either from NAME or from NAME_FILE, the path of a file holding the
// value (the form used with Docker and Compose secrets). Setting both is an
// error, so that a stale variable cannot silently win.
func applySecrets(ctx context.Context, c *Config, env map[string]string) error {
	for _, s := range secretSettings() {
		direct := env[s.env]
		filePath := env[s.env+"_FILE"]
		switch {
		case direct != "" && filePath != "":
			return fmt.Errorf("environment variables %s and %s_FILE are both set; set only one", s.env, s.env)
		case filePath != "":
			value, err := readSecretFile(ctx, filePath)
			if err != nil {
				return fmt.Errorf("environment variable %s_FILE: %w", s.env, err)
			}
			s.set(c, value)
		case direct != "":
			s.set(c, direct)
		}
	}
	return nil
}

// readSecretFile reads a secret from path and removes one trailing newline.
// The file must be a regular file of bounded size.
func readSecretFile(ctx context.Context, path string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("cannot open secret file %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("cannot stat secret file %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("secret file %s is not a regular file", path)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxSecretFileSize+1))
	if err != nil {
		return "", fmt.Errorf("cannot read secret file %s: %w", path, err)
	}
	if len(data) > maxSecretFileSize {
		return "", fmt.Errorf("secret file %s is larger than %d bytes", path, maxSecretFileSize)
	}
	value := strings.TrimSuffix(string(data), "\n")
	value = strings.TrimSuffix(value, "\r")
	return value, nil
}

// knownEnvNames returns every environment variable the loader understands.
func knownEnvNames() map[string]struct{} {
	known := map[string]struct{}{envConfigPath: {}}
	for _, s := range settings() {
		known[s.env] = struct{}{}
	}
	for _, s := range secretSettings() {
		known[s.env] = struct{}{}
		known[s.env+"_FILE"] = struct{}{}
	}
	return known
}

// unknownEnvWarnings reports UNIO_* variables that are not recognised. A
// misspelled name would otherwise be silently ignored, which is dangerous for
// security settings.
func unknownEnvWarnings(env map[string]string) []string {
	known := knownEnvNames()
	var names []string
	for k := range env {
		if strings.HasPrefix(k, envPrefix) {
			if _, ok := known[k]; !ok {
				names = append(names, k)
			}
		}
	}
	sort.Strings(names)
	warnings := make([]string, 0, len(names))
	for _, n := range names {
		warnings = append(warnings, fmt.Sprintf("unknown environment variable %s is ignored; check the spelling", n))
	}
	return warnings
}
