package schema

// Identity carries the caller's tenant, team, user, and use-case context,
// normalized from request headers by the ingress layer.
// The hierarchy for budget enforcement is: tenant (org) → team → user.
type Identity struct {
	TenantID  string `json:"tenant_id"`
	TeamID    string `json:"team_id,omitempty"`
	UserID    string `json:"user_id,omitempty"`
	UseCaseID string `json:"use_case_id,omitempty"`
}
