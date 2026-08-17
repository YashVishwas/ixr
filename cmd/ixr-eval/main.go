// Command ixr-eval runs a golden question set against a running ixr
// instance across one or more models (including "auto", to measure
// auto-routing itself rather than just its component models) and reports
// pass rate, latency, and cost per model — see internal/eval.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/YashVishwas/ixr/internal/eval"
	"github.com/YashVishwas/ixr/pkg/schema"
)

func main() {
	baseURL := flag.String("base-url", "http://localhost:7000/v1", "ixr base URL")
	questionsPath := flag.String("questions", "eval/golden.yaml", "path to a golden question set YAML file")
	models := flag.String("models", "auto", "comma-separated model names to evaluate (e.g. gpt-4o,claude-sonnet-4-6,auto)")
	apiKey := flag.String("api-key", os.Getenv("IXR_API_KEY"), "bearer token, if ixr auth is enabled (default: $IXR_API_KEY)")
	timeout := flag.Duration("timeout", 60*time.Second, "per-request timeout")
	jsonOut := flag.Bool("json", false, "print raw JSON results instead of a summary table")
	flag.Parse()

	f, err := os.Open(*questionsPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ixr-eval: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()

	set, err := eval.LoadSet(f)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ixr-eval: parsing %s: %v\n", *questionsPath, err)
		os.Exit(1)
	}
	if len(set.Questions) == 0 {
		fmt.Fprintf(os.Stderr, "ixr-eval: %s has no questions\n", *questionsPath)
		os.Exit(1)
	}

	modelList := strings.Split(*models, ",")
	for i, m := range modelList {
		modelList[i] = strings.TrimSpace(m)
	}

	client := &http.Client{Timeout: *timeout}
	chat := httpChatFunc(client, *baseURL, *apiKey)

	results := eval.Run(context.Background(), set, modelList, chat)

	if *jsonOut {
		_ = json.NewEncoder(os.Stdout).Encode(results)
		return
	}
	printSummary(os.Stdout, eval.Summarize(results))
}

// httpChatFunc builds an eval.ChatFunc that POSTs to {baseURL}/chat/completions
// — the same OpenAI-compatible endpoint any ixr caller uses, so this
// exercises the real request path (routing, plugins, cost accounting)
// rather than calling internal Go APIs directly.
func httpChatFunc(client *http.Client, baseURL, apiKey string) eval.ChatFunc {
	return func(ctx context.Context, model, prompt string) (*schema.ResponseEnvelope, error) {
		reqBody, err := json.Marshal(schema.RequestEnvelope{
			Model:    model,
			Messages: []schema.Message{{Role: "user", Content: prompt}},
		})
		if err != nil {
			return nil, err
		}

		url := strings.TrimRight(baseURL, "/") + "/chat/completions"
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
		if err != nil {
			return nil, err
		}
		httpReq.Header.Set("Content-Type", "application/json")
		if apiKey != "" {
			httpReq.Header.Set("Authorization", "Bearer "+apiKey)
		}

		httpResp, err := client.Do(httpReq)
		if err != nil {
			return nil, err
		}
		defer httpResp.Body.Close()

		body, err := io.ReadAll(httpResp.Body)
		if err != nil {
			return nil, err
		}
		if httpResp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("status %d: %s", httpResp.StatusCode, body)
		}

		var resp schema.ResponseEnvelope
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("decode response: %w", err)
		}
		return &resp, nil
	}
}

func printSummary(w io.Writer, summaries []eval.ModelSummary) {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "MODEL\tPASS RATE\tPASSED\tERRORED\tAVG LATENCY\tAVG COST\tTOTAL COST")
	for _, s := range summaries {
		fmt.Fprintf(tw, "%s\t%.0f%%\t%d/%d\t%d\t%s\t$%.6f\t$%.6f\n",
			s.Model, s.PassRate*100, s.Passed, s.Total, s.Errored,
			s.AvgLatency.Round(time.Millisecond), s.AvgCostUSD, s.TotalCostUSD)
	}
	_ = tw.Flush()
}
