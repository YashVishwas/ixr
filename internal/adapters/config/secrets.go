package config

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
)

var secretsClient = &http.Client{Timeout: 5 * time.Second}

// ExpandSecrets resolves ${vault:path} and ${ssm:path} references in all string
// fields of cfg when cfg.Secrets.Enabled is true.
// Falls back to the literal string on any error and logs a warning.
// Operates on a deep copy; never mutates the input.
func ExpandSecrets(cfg *Config) (*Config, error) {
	if !cfg.Secrets.Enabled {
		return cfg, nil
	}

	resolve := func(ref string) string {
		switch {
		case strings.HasPrefix(ref, "vault:"):
			path := strings.TrimPrefix(ref, "vault:")
			val, err := resolveVault(cfg.Secrets.VaultAddr, cfg.Secrets.VaultToken, path)
			if err != nil {
				slog.Warn("secrets: vault resolution failed", "path", path, "err", err)
				return "${vault:" + path + "}"
			}
			return val
		case strings.HasPrefix(ref, "ssm:"):
			path := strings.TrimPrefix(ref, "ssm:")
			val, err := resolveSSM(cfg.Secrets.AWSRegion, path)
			if err != nil {
				slog.Warn("secrets: ssm resolution failed", "path", path, "err", err)
				return "${ssm:" + path + "}"
			}
			return val
		default:
			return os.Getenv(ref)
		}
	}

	expand := func(s string) string {
		return os.Expand(s, resolve)
	}

	// Deep copy by re-marshalling through the expander.
	out := *cfg
	for name, pc := range out.Providers {
		pc.APIKey = expand(pc.APIKey)
		pc.BaseURL = expand(pc.BaseURL)
		out.Providers[name] = pc
	}
	for i, k := range out.Auth.APIKeys {
		out.Auth.APIKeys[i].Key = expand(k.Key)
	}
	out.Auth.JWT.Secret = expand(out.Auth.JWT.Secret)
	out.Secrets.VaultToken = expand(out.Secrets.VaultToken)

	return &out, nil
}

// resolveVault fetches a secret from HashiCorp Vault KV v2.
func resolveVault(addr, token, path string) (string, error) {
	if addr == "" {
		return "", fmt.Errorf("vault addr not configured")
	}
	url := strings.TrimRight(addr, "/") + "/v1/" + strings.TrimLeft(path, "/")
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("X-Vault-Token", token)

	resp, err := secretsClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("vault: status %d: %s", resp.StatusCode, b)
	}

	var payload struct {
		Data struct {
			Data map[string]string `json:"data"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("vault: decode response: %w", err)
	}
	// Return the "value" key; fall back to first key found.
	if v, ok := payload.Data.Data["value"]; ok {
		return v, nil
	}
	for _, v := range payload.Data.Data {
		return v, nil
	}
	return "", fmt.Errorf("vault: no data at %s", path)
}

// resolveSSM fetches a parameter from AWS SSM Parameter Store using raw HTTP.
func resolveSSM(region, paramName string) (string, error) {
	if region == "" {
		region = os.Getenv("AWS_DEFAULT_REGION")
	}
	if region == "" {
		return "", fmt.Errorf("ssm: AWS region not configured")
	}

	url := fmt.Sprintf("https://ssm.%s.amazonaws.com/", region)
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(
		`{"Name":"`+paramName+`","WithDecryption":true}`,
	))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", "AmazonSSM.GetParameter")

	resp, err := secretsClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("ssm: status %d: %s", resp.StatusCode, b)
	}

	var payload struct {
		Parameter struct {
			Value string `json:"Value"`
		} `json:"Parameter"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("ssm: decode response: %w", err)
	}
	return payload.Parameter.Value, nil
}
