// Package config loads and validates ixr.yaml with environment variable interpolation.
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// DefaultPaths is the ordered list of locations searched when no explicit path is given.
var DefaultPaths = []string{"ixr.yaml", "/etc/ixr/ixr.yaml"}

// Load reads the config file at path, expands ${ENV_VAR} references, and returns
// the parsed Config. Returns an error if the file exists but cannot be parsed.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}

	expanded := os.Expand(string(raw), os.Getenv)

	var cfg Config
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}

	applyDefaults(&cfg)
	if err := ResolveSecrets(&cfg); err != nil {
		return nil, err
	}
	if err := Validate(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Discover tries each path in DefaultPaths and returns the first Config found.
// Returns nil, nil if no config file exists at any default location.
func Discover() (*Config, error) {
	for _, p := range DefaultPaths {
		if _, err := os.Stat(p); err == nil {
			return Load(p)
		}
	}
	return nil, nil
}

func applyDefaults(c *Config) {
	if c.Server.Port == 0 {
		c.Server.Port = 8080
	}
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
}
