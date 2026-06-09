package scoring

// ShadowPolicy controls whether a use case should execute a shadow model.
type ShadowPolicy struct {
	Enabled bool
	Model   Candidate
}

// ShadowDecision tells the app layer whether to run a background shadow call.
type ShadowDecision struct {
	Enabled bool
	Model   Candidate
}

// PlanShadow returns a shadow route when policy enables one and it differs from primary.
func PlanShadow(primary Candidate, policy ShadowPolicy) ShadowDecision {
	if !policy.Enabled || policy.Model.Model == "" || policy.Model.Model == primary.Model {
		return ShadowDecision{}
	}
	return ShadowDecision{Enabled: true, Model: policy.Model}
}

// shadow orchestrates shadow routing: the primary model serves the caller while
// a shadow model processes the same request in the background for offline comparison.
// Shadow routing is opt-in per use-case, configured via the policy store.