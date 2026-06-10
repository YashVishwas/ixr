# ixr adaptive routing (phase 2)

The scoring engine in v1 uses deterministic, human-designed weights. v2 replaces them with a bandit algorithm that learns optimal routing from real traffic — without the engineer doing anything.

## the reward function

```
reward(model, request) =
    α * (1 / latency_ms)       // faster = better
  + β * (1 - cost_per_token)   // cheaper = better
  + γ * success_rate           // reliable = better
  + δ * quality_score          // better output = better (phase 2c)

where α, β, γ, δ are learned per intent
```

## algorithms

Two candidates are implemented and run in shadow mode before either goes live:

- **epsilon-greedy** — explore with probability ε, exploit with 1-ε. Simple, stable.
- **UCB (upper confidence bound)** — score = expected_reward + exploration_bonus. Naturally handles new models entering the pool.

The winner is chosen by minimizing cumulative regret on real traffic.

## regret metric

```
regret = sum(optimal_reward - chosen_reward) over all requests
```

Lower cumulative regret = the algorithm is learning faster. This is the north star metric for v2 routing quality.

## shadow routing

Before committing to a new model or routing algorithm, shadow routing evaluates it safely on real traffic without affecting callers:

```
normal flow:  call → primary model → response to caller
shadow flow:  call → primary model → response to caller (unchanged)
                   → shadow model  → response stored (not sent) → compare offline
```

Shadow routing is opt-in. Until the policy store is wired into ingress, callers
can enable a single shadow model per request with:

```http
X-IXR-Shadow-Model: claude-3-5-sonnet
X-IXR-Shadow-Timeout-MS: 30000
```

The primary response is returned unchanged. The shadow call runs asynchronously
with its own timeout and emits a `CallEvent` with `Shadow` metadata for
telemetry/offline comparison. Shadow routing errors are published on that event
and never fail the caller's primary request.
