package config

import "fmt"

// Validate checks config invariants that should fail startup.
func Validate(c *Config) error {
	if c == nil {
		return nil
	}
	if c.Server.Port < 0 || c.Server.Port > 65535 {
		return fmt.Errorf("config: server.port out of range")
	}
	for name, key := range c.Auth.APIKeys {
		if key.Secret == "" {
			return fmt.Errorf("config: auth.api_keys.%s.secret is required", name)
		}
		if key.DailyUSD < 0 {
			return fmt.Errorf("config: auth.api_keys.%s.daily_usd cannot be negative", name)
		}
	}
	if c.Limits.WindowSeconds < 0 || c.Limits.MaxRequests < 0 || c.Limits.MaxTokens < 0 {
		return fmt.Errorf("config: limits cannot be negative")
	}
	return nil
}
