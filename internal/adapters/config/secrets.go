package config

import (
	"fmt"
	"os"
	"strings"
)

// ResolveSecrets replaces secret references in provider and auth config.
// Supported local forms:
//
//	file:/path/to/secret
//	env:ENV_VAR
func ResolveSecrets(c *Config) error {
	if c == nil {
		return nil
	}
	for name, p := range c.Providers {
		v, err := resolveSecret(p.APIKey)
		if err != nil {
			return fmt.Errorf("config: provider %s api_key: %w", name, err)
		}
		p.APIKey = v
		c.Providers[name] = p
	}
	for id, key := range c.Auth.APIKeys {
		v, err := resolveSecret(key.Secret)
		if err != nil {
			return fmt.Errorf("config: auth key %s: %w", id, err)
		}
		key.Secret = v
		c.Auth.APIKeys[id] = key
	}
	return nil
}

func resolveSecret(raw string) (string, error) {
	switch {
	case strings.HasPrefix(raw, "file:"):
		b, err := os.ReadFile(strings.TrimPrefix(raw, "file:"))
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(b)), nil
	case strings.HasPrefix(raw, "env:"):
		return os.Getenv(strings.TrimPrefix(raw, "env:")), nil
	default:
		return raw, nil
	}
}
