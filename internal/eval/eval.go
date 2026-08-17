// Package eval is a small routing-quality harness: a golden question set
// scored across models/routing strategies (including "auto"), so whether
// auto-routing is actually delivering good-cheap answers can be measured
// directly instead of trusted from proxy signals (FinishReason, cost
// estimates) alone. The natural companion to the bandit's quality signal
// (plugins/feedback, plugins/banditreward) — those improve what the bandit
// optimizes for in production; this measures whether it's working.
//
// Deliberately a client-side, HTTP-driven tool (cmd/ixr-eval), not
// something wired into the request path or the plugin system — an eval run
// is an operator action against a running ixr instance, not a feature ixr
// itself needs at runtime.
package eval

import (
	"context"
	"io"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/YashVishwas/ixr/internal/domain/cost"
	"github.com/YashVishwas/ixr/pkg/schema"
)

// Question is one golden-set item. Scoring is intentionally simple
// (case-insensitive substring checks, not an LLM judge) — cheap,
// deterministic, and zero-cost to run repeatedly, matching ixr's own "no
// data leaves the process without explicit operator configuration"
// constraint (pkg/ixr doesn't get a required LLM-judge dependency, and
// neither does this). A question with no checks always passes; it still
// contributes latency/cost data, useful for "does auto route this category
// to something reasonably fast/cheap" even without a pass/fail bar.
type Question struct {
	ID             string   `yaml:"id" json:"id"`
	Prompt         string   `yaml:"prompt" json:"prompt"`
	Category       string   `yaml:"category,omitempty" json:"category,omitempty"`
	MustContain    []string `yaml:"must_contain,omitempty" json:"must_contain,omitempty"`
	MustNotContain []string `yaml:"must_not_contain,omitempty" json:"must_not_contain,omitempty"`
}

// Set is a golden question set loaded from YAML.
type Set struct {
	Questions []Question `yaml:"questions"`
}

// LoadSet parses a golden question set from r.
func LoadSet(r io.Reader) (*Set, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var set Set
	if err := yaml.Unmarshal(data, &set); err != nil {
		return nil, err
	}
	return &set, nil
}

// Score reports whether response satisfies q's must_contain/
// must_not_contain checks. Matching is case-insensitive substring, not
// exact/regex — golden-set answers vary in phrasing even when correct, and
// a harness that demands exact wording would mostly measure phrasing luck,
// not answer quality.
func Score(q Question, response string) bool {
	lower := strings.ToLower(response)
	for _, want := range q.MustContain {
		if !strings.Contains(lower, strings.ToLower(want)) {
			return false
		}
	}
	for _, unwanted := range q.MustNotContain {
		if strings.Contains(lower, strings.ToLower(unwanted)) {
			return false
		}
	}
	return true
}

// Result is the outcome of running one Question against one model.
type Result struct {
	Question  Question
	Model     string
	Response  string
	Passed    bool
	Error     string
	Latency   time.Duration
	Cost      schema.CostBreakdown
	PromptTok int
	OutputTok int
}

// ChatFunc sends prompt to model and returns ixr's normalized response —
// the same shape /v1/chat/completions returns, so cmd/ixr-eval's real
// implementation is just an HTTP POST + JSON decode. Injectable so Run is
// testable without a live ixr instance or real provider traffic.
type ChatFunc func(ctx context.Context, model, prompt string) (*schema.ResponseEnvelope, error)

// Run executes every question in set against every model in models via
// chat, scoring each response. A ChatFunc error produces a Result with
// Error set and Passed=false rather than aborting the run — one bad
// model/question pair shouldn't stop the rest of the eval.
func Run(ctx context.Context, set *Set, models []string, chat ChatFunc) []Result {
	results := make([]Result, 0, len(set.Questions)*len(models))
	for _, model := range models {
		for _, q := range set.Questions {
			start := time.Now()
			resp, err := chat(ctx, model, q.Prompt)
			latency := time.Since(start)

			r := Result{Question: q, Model: model, Latency: latency}
			if err != nil {
				r.Error = err.Error()
				results = append(results, r)
				continue
			}

			if len(resp.Choices) > 0 {
				r.Response = resp.Choices[0].Message.Content
			}
			r.Passed = Score(q, r.Response)
			r.PromptTok = resp.Usage.PromptTokens
			r.OutputTok = resp.Usage.CompletionTokens
			r.Cost = cost.ForUsage(resp.Model, r.PromptTok, r.OutputTok)
			results = append(results, r)
		}
	}
	return results
}

// ModelSummary aggregates Results for one model across the whole question set.
type ModelSummary struct {
	Model        string
	Total        int
	Passed       int
	Errored      int
	PassRate     float64
	AvgLatency   time.Duration
	TotalCostUSD float64
	AvgCostUSD   float64
}

// Summarize groups results by model. Order matches models' first
// appearance in results, so it reflects the order Run was given rather
// than being resorted alphabetically underneath the caller.
func Summarize(results []Result) []ModelSummary {
	order := []string{}
	byModel := map[string][]Result{}
	for _, r := range results {
		if _, ok := byModel[r.Model]; !ok {
			order = append(order, r.Model)
		}
		byModel[r.Model] = append(byModel[r.Model], r)
	}

	summaries := make([]ModelSummary, 0, len(order))
	for _, model := range order {
		rs := byModel[model]
		s := ModelSummary{Model: model, Total: len(rs)}
		var totalLatency time.Duration
		for _, r := range rs {
			if r.Error != "" {
				s.Errored++
				continue
			}
			if r.Passed {
				s.Passed++
			}
			totalLatency += r.Latency
			s.TotalCostUSD += r.Cost.TotalUSD
		}
		if s.Total > 0 {
			s.PassRate = float64(s.Passed) / float64(s.Total)
			s.AvgLatency = totalLatency / time.Duration(s.Total)
		}
		if scored := s.Total - s.Errored; scored > 0 {
			s.AvgCostUSD = s.TotalCostUSD / float64(scored)
		}
		summaries = append(summaries, s)
	}
	return summaries
}
