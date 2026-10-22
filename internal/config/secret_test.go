package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

const secretValue = "s3cr3t-value-that-must-never-appear-0123456789"

func TestSecretRedactsEveryRepresentation(t *testing.T) {
	s := NewSecret(secretValue)
	type holder struct {
		Key Secret
	}
	h := holder{Key: s}
	jsonOut, err := json.Marshal(h)
	if err != nil {
		t.Fatalf("json: %v", err)
	}
	yamlOut, err := yaml.Marshal(h)
	if err != nil {
		t.Fatalf("yaml: %v", err)
	}
	var logBuf bytes.Buffer
	slog.New(slog.NewJSONHandler(&logBuf, nil)).Info("event", "key", s)

	outputs := map[string]string{
		"%v":       fmt.Sprintf("%v", s),
		"%s":       fmt.Sprintf("%s", s),
		"%q":       fmt.Sprintf("%q", s),
		"%+v":      fmt.Sprintf("%+v", s),
		"%#v":      fmt.Sprintf("%#v", s),
		"struct":   fmt.Sprintf("%+v", h),
		"gostr":    fmt.Sprintf("%#v", h),
		"json":     string(jsonOut),
		"yaml":     string(yamlOut),
		"slog":     logBuf.String(),
		"stringer": s.String(),
	}
	for name, out := range outputs {
		if strings.Contains(out, secretValue) {
			t.Errorf("%s leaks the secret: %s", name, out)
		}
	}
	if !strings.Contains(outputs["%v"], Redacted) {
		t.Errorf("%%v = %q, want the redaction placeholder", outputs["%v"])
	}
}

func TestSecretExposeAndIsSet(t *testing.T) {
	var zero Secret
	if zero.IsSet() {
		t.Fatal("zero Secret must be unset")
	}
	if zero.String() != "" {
		t.Fatalf("unset Secret prints %q, want empty", zero.String())
	}
	s := NewSecret(secretValue)
	if !s.IsSet() || s.Expose() != secretValue {
		t.Fatalf("Expose() = %q, IsSet() = %v", s.Expose(), s.IsSet())
	}
}

func TestSecretRejectedInYAMLFile(t *testing.T) {
	type doc struct {
		Secret Secret `yaml:"secret"`
	}
	var d doc
	err := yaml.Unmarshal([]byte("secret: "+secretValue+"\n"), &d)
	if err == nil || !strings.Contains(err.Error(), "must not appear in the config file") {
		t.Fatalf("yaml secret error = %v, want a rejection", err)
	}
	if strings.Contains(err.Error(), secretValue) {
		t.Fatalf("the rejection leaks the value: %v", err)
	}
}
