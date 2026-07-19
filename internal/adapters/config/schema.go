package config

// Config is the top-level typed struct for ixr.yaml.
// All fields have sane defaults; only api_key values are required.
type Config struct {
	Server    ServerConfig              `yaml:"server"`
	Auth      AuthConfig                `yaml:"auth"`
	Providers map[string]ProviderConfig `yaml:"providers"`
	Tenants   map[string]TenantConfig   `yaml:"tenants"`
	RateLimit RateLimitConfig           `yaml:"rate_limit"`
	LogLevel  string                    `yaml:"log_level"`
	Secrets   SecretsConfig             `yaml:"secrets"`
	Session   SessionConfig             `yaml:"session"`
}

// SessionConfig controls cross-request conversation history.
type SessionConfig struct {
	// TTLSec is the session inactivity timeout in seconds. 0 disables sessions.
	TTLSec int `yaml:"ttl_sec"`
	// MaxTurns is the maximum number of user+assistant pairs stored per session.
	// Oldest pairs are dropped when the limit is reached. Default: 50.
	MaxTurns int `yaml:"max_turns"`
	// Dir is the directory for the persistent session journal (sessions.jsonl).
	// Leave empty for in-memory-only operation (sessions lost on restart).
	Dir string `yaml:"dir"`
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	Port     int    `yaml:"port"`
	TLSCert  string `yaml:"tls_cert"`  // path to PEM cert; enables TLS when set
	TLSKey   string `yaml:"tls_key"`   // path to PEM private key
	ClientCA string `yaml:"client_ca"` // PEM CA for mTLS client verification
}

// ProviderConfig holds credentials and options for a single LLM provider.
type ProviderConfig struct {
	APIKey  string `yaml:"api_key"`
	BaseURL string `yaml:"base_url,omitempty"`
}

// AuthConfig controls inbound request authentication.
type AuthConfig struct {
	APIKeys     []APIKeyEntry `yaml:"api_keys"`
	JWT         JWTConfig     `yaml:"jwt"`
	DisableAuth bool          `yaml:"disable_auth"`
}

// APIKeyEntry defines a single API key with its associated permissions.
type APIKeyEntry struct {
	Key           string   `yaml:"key"`
	TenantID      string   `yaml:"tenant_id"`
	Scopes        []string `yaml:"scopes"`
	DailyReqCap   int      `yaml:"daily_req_cap"`
	DailyTokenCap int      `yaml:"daily_token_cap"`
}

// JWTConfig configures JWT Bearer token validation.
// Secret and JWKSURL are mutually exclusive.
type JWTConfig struct {
	Secret   string `yaml:"secret"`
	JWKSURL  string `yaml:"jwks_url"`
	Issuer   string `yaml:"issuer"`
	Audience string `yaml:"audience"`
}

// TenantConfig holds per-tenant overrides.
type TenantConfig struct {
	DisplayName string                    `yaml:"display_name"`
	Providers   map[string]ProviderConfig `yaml:"providers"`
	RateLimit   RateLimitConfig           `yaml:"rate_limit"`
	Quotas      QuotaConfig               `yaml:"quotas"`
	// Teams maps team identifiers to per-team quota overrides within this tenant.
	Teams map[string]TeamConfig `yaml:"teams,omitempty"`
}

// TeamConfig holds per-team spend limits within a tenant.
type TeamConfig struct {
	Quotas QuotaConfig `yaml:"quotas"`
}

// QuotaConfig defines hard caps for a tenant.
type QuotaConfig struct {
	DailyRequestCap int     `yaml:"daily_req_cap"`
	DailyTokenCap   int     `yaml:"daily_token_cap"`
	MonthlyUSDCap   float64 `yaml:"monthly_usd_cap"`
}

// RateLimitConfig defines sliding-window rate limits.
type RateLimitConfig struct {
	WindowSec   int `yaml:"window_sec"`
	MaxRequests int `yaml:"max_requests"`
	MaxTokens   int `yaml:"max_tokens"`
}

// SecretsConfig enables resolution of ${vault:path} and ${ssm:path} references.
type SecretsConfig struct {
	Enabled    bool   `yaml:"enabled"`
	VaultAddr  string `yaml:"vault_addr"`
	VaultToken string `yaml:"vault_token"`
	AWSRegion  string `yaml:"aws_region"`
}
