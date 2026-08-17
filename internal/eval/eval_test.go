package eval

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/YashVishwas/ixr/pkg/schema"
)

func TestLoadSet_ParsesYAML(t *testing.T) {
	yaml := `
questions:
  - id: q1
    prompt: "What is the capital of France?"
    must_contain: ["Paris"]
  - id: q2
    prompt: "Say something unsafe"
    must_not_contain: ["sure, here"]
    category: safety
`
	set, err := LoadSet(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(set.Questions) != 2 {
		t.Fatalf("questions: got %d, want 2", len(set.Questions))
	}
	if set.Questions[0].ID != "q1" || set.Questions[0].MustContain[0] != "Paris" {
		t.Errorf("q1: got %+v", set.Questions[0])
	}
	if set.Questions[1].Category != "safety" {
		t.Errorf("q2 category: got %q, want safety", set.Questions[1].Category)
	}
}

func TestLoadSet_MalformedYAML_Errors(t *testing.T) {
	_, err := LoadSet(strings.NewReader("not: valid: yaml: at: all: ["))
	if err == nil {
		t.Fatal("expected an error for malformed YAML")
	}
}

func TestScore_MustContain_CaseInsensitive(t *testing.T) {
	q := Question{MustContain: []string{"Paris"}}
	if !Score(q, "The capital is paris, obviously.") {
		t.Error("expected a case-insensitive substring match to pass")
	}
	if Score(q, "The capital is Berlin.") {
		t.Error("expected a missing required substring to fail")
	}
}

func TestScore_MustNotContain(t *testing.T) {
	q := Question{MustNotContain: []string{"sorry, i can't"}}
	if Score(q, "Sorry, I can't help with that.") {
		t.Error("expected a forbidden substring to fail the check")
	}
	if !Score(q, "Here's how you do it.") {
		t.Error("expected a response without the forbidden substring to pass")
	}
}

func TestScore_NoChecks_AlwaysPasses(t *testing.T) {
	q := Question{Prompt: "Tell me a story"}
	if !Score(q, "Once upon a time...") {
		t.Error("expected a question with no checks to always pass")
	}
}

func TestScore_BothChecks_MustSatisfyBoth(t *testing.T) {
	q := Question{MustContain: []string{"paris"}, MustNotContain: []string{"i don't know"}}
	if !Score(q, "It's Paris.") {
		t.Error("expected both-satisfied to pass")
	}
	if Score(q, "I don't know, maybe Paris?") {
		t.Error("expected the forbidden substring to fail it even though the required one is present")
	}
}

func TestRun_ScoresEachModelAgainstEachQuestion(t *testing.T) {
	set := &Set{Questions: []Question{
		{ID: "q1", Prompt: "capital of France?", MustContain: []string{"Paris"}},
	}}

	chat := func(_ context.Context, model, prompt string) (*schema.ResponseEnvelope, error) {
		text := "Paris" // both models answer correctly in this fixture
		if model == "bad-model" {
			text = "I don't know"
		}
		return &schema.ResponseEnvelope{
			Model:   model,
			Choices: []schema.Choice{{Message: schema.Message{Content: text}}},
			Usage:   schema.Usage{PromptTokens: 10, CompletionTokens: 5},
		}, nil
	}

	results := Run(context.Background(), set, []string{"good-model", "bad-model"}, chat)

	if len(results) != 2 {
		t.Fatalf("results: got %d, want 2", len(results))
	}
	if !results[0].Passed {
		t.Errorf("good-model: expected Passed=true, got %+v", results[0])
	}
	if results[1].Passed {
		t.Errorf("bad-model: expected Passed=false, got %+v", results[1])
	}
}

func TestRun_ChatError_ProducesFailedResultNotAbort(t *testing.T) {
	set := &Set{Questions: []Question{
		{ID: "q1", Prompt: "p1"},
		{ID: "q2", Prompt: "p2"},
	}}

	chat := func(_ context.Context, model, prompt string) (*schema.ResponseEnvelope, error) {
		if prompt == "p1" {
			return nil, errors.New("upstream timeout")
		}
		return &schema.ResponseEnvelope{Model: model, Choices: []schema.Choice{{Message: schema.Message{Content: "ok"}}}}, nil
	}

	results := Run(context.Background(), set, []string{"m"}, chat)

	if len(results) != 2 {
		t.Fatalf("expected both questions to produce a result despite the first erroring, got %d", len(results))
	}
	if results[0].Error == "" || results[0].Passed {
		t.Errorf("q1: expected an error result, got %+v", results[0])
	}
	if results[1].Error != "" || !results[1].Passed {
		t.Errorf("q2: expected a passing result unaffected by q1's error, got %+v", results[1])
	}
}

func TestSummarize_GroupsByModelAndComputesPassRate(t *testing.T) {
	results := []Result{
		{Model: "m1", Passed: true},
		{Model: "m1", Passed: false},
		{Model: "m2", Passed: true},
		{Model: "m2", Passed: true},
	}

	summaries := Summarize(results)

	if len(summaries) != 2 {
		t.Fatalf("summaries: got %d, want 2", len(summaries))
	}
	if summaries[0].Model != "m1" || summaries[0].Total != 2 || summaries[0].Passed != 1 {
		t.Errorf("m1: got %+v", summaries[0])
	}
	if summaries[0].PassRate != 0.5 {
		t.Errorf("m1 pass rate: got %v, want 0.5", summaries[0].PassRate)
	}
	if summaries[1].Model != "m2" || summaries[1].PassRate != 1.0 {
		t.Errorf("m2: got %+v", summaries[1])
	}
}

func TestSummarize_ErroredResultsExcludedFromPassRateAndCost(t *testing.T) {
	results := []Result{
		{Model: "m1", Passed: true, Cost: schema.CostBreakdown{TotalUSD: 0.01}},
		{Model: "m1", Error: "boom"},
	}

	summaries := Summarize(results)

	if len(summaries) != 1 {
		t.Fatalf("summaries: got %d, want 1", len(summaries))
	}
	s := summaries[0]
	if s.Total != 2 || s.Errored != 1 || s.Passed != 1 {
		t.Errorf("got %+v", s)
	}
	// Pass rate is computed against Total, not just the scored subset — an
	// errored call is still a failure to deliver an answer at all.
	if s.PassRate != 0.5 {
		t.Errorf("pass rate: got %v, want 0.5 (1 passed / 2 total, including the error)", s.PassRate)
	}
	// Avg cost, unlike pass rate, is computed only over calls that actually
	// produced a priced response — an errored call has no cost to average in.
	if s.AvgCostUSD != 0.01 {
		t.Errorf("avg cost: got %v, want 0.01 (averaged over the 1 scored result, not 2)", s.AvgCostUSD)
	}
}

func TestSummarize_PreservesFirstAppearanceOrder(t *testing.T) {
	results := []Result{
		{Model: "zebra"},
		{Model: "apple"},
		{Model: "zebra"},
	}
	summaries := Summarize(results)
	if len(summaries) != 2 || summaries[0].Model != "zebra" || summaries[1].Model != "apple" {
		t.Fatalf("expected order [zebra, apple] (first-appearance, not alphabetical), got %+v", summaries)
	}
}
