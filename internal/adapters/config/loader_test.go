package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "ixr-*.yaml")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	f.Close()
	return f.Name()
}

func TestLoad_ValidConfig(t *testing.T) {
	path := writeTemp(t, `
server:
  port: 8080
providers:
  openai:
    api_key: sk-test
  anthropic:
    api_key: sk-ant-test
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Server.Port != 8080 {
		t.Errorf("port: got %d, want 8080", cfg.Server.Port)
	}
	if cfg.Providers["openai"].APIKey != "sk-test" {
		t.Errorf("openai api_key: got %q", cfg.Providers["openai"].APIKey)
	}
}

func TestLoad_EnvVarExpansion(t *testing.T) {
	t.Setenv("TEST_IXR_KEY", "sk-from-env")

	path := writeTemp(t, `
providers:
  openai:
    api_key: ${TEST_IXR_KEY}
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Providers["openai"].APIKey != "sk-from-env" {
		t.Errorf("api_key: got %q, want sk-from-env", cfg.Providers["openai"].APIKey)
	}
}

func TestLoad_DefaultPort(t *testing.T) {
	path := writeTemp(t, `providers:
  openai:
    api_key: sk-x
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Server.Port != 7000 {
		t.Errorf("default port: got %d, want 7000", cfg.Server.Port)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nonexistent.yaml"))
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestLoad_MalformedYAML(t *testing.T) {
	// Tabs at the start of a YAML mapping value are a parse error.
	path := writeTemp(t, "server:\n\tport: 8080")
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for malformed YAML, got nil")
	}
}

func TestDiscover_NoFile(t *testing.T) {
	// Override default paths to a temp dir where no file exists.
	orig := DefaultPaths
	DefaultPaths = []string{filepath.Join(t.TempDir(), "ixr.yaml")}
	defer func() { DefaultPaths = orig }()

	cfg, err := Discover()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg != nil {
		t.Error("expected nil config when no file found")
	}
}
