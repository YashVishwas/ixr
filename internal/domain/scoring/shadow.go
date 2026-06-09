package scoring

// Candidate is a routable provider/model pair used by shadow planning.
type Candidate struct {
	Model string
}

// ShadowPolicy configures shadow routing for a request.
type ShadowPolicy struct {
	Enabled bool
	Model   Candidate
}

// PlanShadow decides whether to run a shadow request alongside the primary.
// It returns the policy unchanged; callers check decision.Enabled before proceeding.
func PlanShadow(primary Candidate, policy ShadowPolicy) ShadowPolicy {
	if !policy.Enabled || policy.Model.Model == "" || policy.Model.Model == primary.Model {
		return ShadowPolicy{Enabled: false}
	}
	return policy
}
