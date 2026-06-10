package config

// Config is the top-level typed struct for ixr.yaml.
// All fields have sane defaults; only api_key values are required.
type Config struct {
	Server    ServerConfig              `yaml:"server"`
	Providers map[string]ProviderConfig `yaml:"providers"`
	Auth      AuthConfig                `yaml:"auth"`
	Limits    LimitsConfig              `yaml:"limits"`
	Tenants   map[string]TenantConfig   `yaml:"tenants"`
	Secrets   SecretsConfig             `yaml:"secrets"`
	LogLevel  string                    `yaml:"log_level"`
	Chains    map[string]ChainDef       `yaml:"chains,omitempty"`
}

// ChainDef describes a named multi-model pipeline declared in ixr.yaml.
// Invoke it at request time with the header X-IXR-Chain: <name>.
type ChainDef struct {
	Models  []string `yaml:"models"`
	Prompts []string `yaml:"prompts,omitempty"`
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	Port int `yaml:"port"`
}

// ProviderConfig holds credentials and options for a single LLM provider.
type ProviderConfig struct {
	APIKey  string `yaml:"api_key"`
	BaseURL string `yaml:"base_url,omitempty"`
}

// AuthConfig holds ingress auth settings.
type AuthConfig struct {
	RequireMTLS bool                    `yaml:"require_mtls,omitempty"`
	APIKeys     map[string]APIKeyConfig `yaml:"api_keys,omitempty"`
	JWT         JWTConfig               `yaml:"jwt,omitempty"`
}

type APIKeyConfig struct {
	Secret        string   `yaml:"secret"`
	Scopes        []string `yaml:"scopes,omitempty"`
	AllowedModels []string `yaml:"allowed_models,omitempty"`
	DailyUSD      float64  `yaml:"daily_usd,omitempty"`
}

type JWTConfig struct {
	Issuer   string   `yaml:"issuer,omitempty"`
	Audience string   `yaml:"audience,omitempty"`
	JWKSURL  string   `yaml:"jwks_url,omitempty"`
	Scopes   []string `yaml:"scopes,omitempty"`
}

type LimitsConfig struct {
	WindowSeconds int `yaml:"window_seconds,omitempty"`
	MaxRequests   int `yaml:"max_requests,omitempty"`
	MaxTokens     int `yaml:"max_tokens,omitempty"`
}

type TenantConfig struct {
	Providers map[string]ProviderConfig `yaml:"providers,omitempty"`
	Limits    LimitsConfig              `yaml:"limits,omitempty"`
}

type SecretsConfig struct {
	Files            map[string]string `yaml:"files,omitempty"`
	VaultAddr        string            `yaml:"vault_addr,omitempty"`
	AWSSecretsRegion string            `yaml:"aws_secrets_region,omitempty"`
	GCPProject       string            `yaml:"gcp_project,omitempty"`
}
