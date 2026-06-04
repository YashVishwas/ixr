package config

import (
	"errors"
	"fmt"
)

// Validate checks a loaded Config for logical errors.
// Returns a combined error listing all violations (not just the first).
func Validate(cfg *Config) error {
	var errs []error

	if cfg.Server.Port < 1 || cfg.Server.Port > 65535 {
		errs = append(errs, fmt.Errorf("server.port %d is not in [1, 65535]", cfg.Server.Port))
	}

	if cfg.Auth.JWT.Secret != "" && cfg.Auth.JWT.JWKSURL != "" {
		errs = append(errs, errors.New("auth.jwt: secret and jwks_url are mutually exclusive"))
	}

	for i, k := range cfg.Auth.APIKeys {
		if k.Key == "" {
			errs = append(errs, fmt.Errorf("auth.api_keys[%d]: key must not be empty", i))
		}
	}

	if cfg.RateLimit.MaxRequests > 0 || cfg.RateLimit.MaxTokens > 0 {
		if cfg.RateLimit.WindowSec <= 0 {
			errs = append(errs, errors.New("rate_limit.window_sec must be > 0 when limits are set"))
		}
	}

	for name, pc := range cfg.Providers {
		if pc.APIKey == "" {
			errs = append(errs, fmt.Errorf("providers.%s: api_key must not be empty", name))
		}
	}

	if cfg.Server.TLSCert != "" && cfg.Server.TLSKey == "" {
		errs = append(errs, errors.New("server.tls_cert is set but server.tls_key is missing"))
	}
	if cfg.Server.TLSKey != "" && cfg.Server.TLSCert == "" {
		errs = append(errs, errors.New("server.tls_key is set but server.tls_cert is missing"))
	}

	return errors.Join(errs...)
}
