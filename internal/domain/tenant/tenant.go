// Package tenant contains multi-tenant policy and credential selection.
package tenant

import "github.com/YashVishwas/ixr/internal/domain/policy"

// Credentials identifies a tenant-scoped upstream credential reference.
type Credentials struct {
	Provider string
	APIKey   string
	BaseURL  string
}

// Config is the effective tenant policy.
type Config struct {
	ID          string
	Limits      policy.RateLimit
	Credentials map[string]Credentials
}

// Store reads tenant configuration by ID.
type Store interface {
	Get(id string) (Config, bool)
}

// MemoryStore is a static tenant config store.
type MemoryStore struct {
	tenants map[string]Config
}

func NewMemoryStore(tenants []Config) *MemoryStore {
	m := map[string]Config{}
	for _, t := range tenants {
		m[t.ID] = t
	}
	return &MemoryStore{tenants: m}
}

func (s *MemoryStore) Get(id string) (Config, bool) {
	if s == nil {
		return Config{}, false
	}
	t, ok := s.tenants[id]
	return t, ok
}
