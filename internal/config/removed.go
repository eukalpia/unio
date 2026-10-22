package config

import (
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"
)

// removedSetting describes a setting that no longer exists, or that was
// renamed. A source that still sets it fails to load, and the error says what
// happened to the setting. Without this, an operator who upgrades would only
// see a generic unknown-key error, or a warning that the value is ignored.
type removedSetting struct {
	// yaml is the section and key as written in the YAML file, for example
	// cluster.insecure_internode.
	yaml string
	// env is the environment variable.
	env string
	// flag is the command-line flag without dashes.
	flag string
	// isBool reports whether the flag takes no value.
	isBool bool
	// message says what happened to the setting. It follows the name.
	message string
}

// removedSettings returns the settings that were removed or renamed.
func removedSettings() []removedSetting {
	return []removedSetting{
		{
			yaml:    "cluster.insecure_internode",
			env:     "UNIO_INSECURE_INTERNODE",
			flag:    "insecure-internode",
			isBool:  true,
			message: "was removed: internode traffic is always mutual TLS, so there is no plain HTTP option",
		},
	}
}

// checkRemovedYAML refuses a YAML file that still sets a removed key. The file
// is read loosely here, so that a syntax error is reported by the strict decode
// that follows, not by this check.
func checkRemovedYAML(path string, data []byte) error {
	var top map[string]any
	if err := yaml.Unmarshal(data, &top); err != nil {
		return nil
	}
	for _, r := range removedSettings() {
		section, key, _ := strings.Cut(r.yaml, ".")
		values, ok := top[section].(map[string]any)
		if !ok {
			continue
		}
		if _, set := values[key]; set {
			return fmt.Errorf("config file %s: %s %s", path, r.yaml, r.message)
		}
	}
	return nil
}

// checkRemovedEnv refuses a removed environment variable that is set to a
// non-empty value. An empty variable counts as unset, as everywhere else.
func checkRemovedEnv(env map[string]string) error {
	for _, r := range removedSettings() {
		if env[r.env] != "" {
			return fmt.Errorf("environment variable %s %s", r.env, r.message)
		}
	}
	return nil
}

// checkRemovedFlag refuses a removed command-line flag.
func checkRemovedFlag(name string) error {
	for _, r := range removedSettings() {
		if r.flag == name {
			return fmt.Errorf("flag --%s %s", name, r.message)
		}
	}
	return nil
}
