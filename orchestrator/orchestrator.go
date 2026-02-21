package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/dre4success/tripartite/adapter"
	"github.com/dre4success/tripartite/runner"
	"github.com/dre4success/tripartite/store"
)

// Config holds the orchestrator's runtime configuration.
type Config struct {
	Prompt   string
	Adapters []adapter.Adapter
	Timeout  time.Duration
	Store    *store.Store
}

// Run executes the full 3-round orchestration flow and returns all responses
// grouped by round.
func Run(ctx context.Context, cfg Config) ([][]adapter.Response, error) {
	allRounds := make([][]adapter.Response, 0, 3)

	// --- Round 1: Initial ---
	printRoundHeader(1, "Initial Response")
	r1 := fanOut(ctx, cfg.Adapters, cfg.Prompt, cfg.Timeout)
	allRounds = append(allRounds, r1)
	saveRound(cfg.Store, 1, r1)
	printResponses(r1)

	// Need at least 2 successful responses for cross-review.
	successful := successfulResponses(r1)
	if len(successful) < 2 {
		fmt.Println("\n[!] Fewer than 2 successful responses — skipping cross-review and synthesis.")
		return allRounds, nil
	}

	// --- Round 2: Cross-Review ---
	printRoundHeader(2, "Cross-Review")
	r2 := fanOutReview(ctx, cfg.Adapters, r1, cfg.Timeout)
	allRounds = append(allRounds, r2)
	saveRound(cfg.Store, 2, r2)
	printResponses(r2)

	// --- Round 3: Synthesis ---
	printRoundHeader(3, "Synthesis")
	r3 := fanOutSynthesis(ctx, cfg.Adapters, r1, r2, cfg.Timeout)
	allRounds = append(allRounds, r3)
	saveRound(cfg.Store, 3, r3)
	printResponses(r3)

	return allRounds, nil
}

// fanOut sends the same prompt to all adapters in parallel.
func fanOut(ctx context.Context, adapters []adapter.Adapter, prompt string, timeout time.Duration) []adapter.Response {
	responses := make([]adapter.Response, len(adapters))
	var wg sync.WaitGroup

	for i, a := range adapters {
		wg.Add(1)
		go func(idx int, adp adapter.Adapter) {
			defer wg.Done()
			responses[idx] = runner.Run(ctx, adp, prompt, timeout)
		}(i, a)
	}

	wg.Wait()
	return responses
}

// fanOutReview sends each model a prompt containing the other models' responses.
func fanOutReview(ctx context.Context, adapters []adapter.Adapter, round1 []adapter.Response, timeout time.Duration) []adapter.Response {
	responses := make([]adapter.Response, len(adapters))
	var wg sync.WaitGroup

	for i, a := range adapters {
		wg.Add(1)
		go func(idx int, adp adapter.Adapter) {
			defer wg.Done()
			prompt := buildReviewPrompt(adp.Name(), round1)
			responses[idx] = runner.Run(ctx, adp, prompt, timeout)
		}(i, a)
	}

	wg.Wait()
	return responses
}

// fanOutSynthesis sends each model all round-1 and round-2 responses to synthesize.
func fanOutSynthesis(ctx context.Context, adapters []adapter.Adapter, round1, round2 []adapter.Response, timeout time.Duration) []adapter.Response {
	responses := make([]adapter.Response, len(adapters))
	var wg sync.WaitGroup

	for i, a := range adapters {
		wg.Add(1)
		go func(idx int, adp adapter.Adapter) {
			defer wg.Done()
			prompt := buildSynthesisPrompt(round1, round2)
			responses[idx] = runner.Run(ctx, adp, prompt, timeout)
		}(i, a)
	}

	wg.Wait()
	return responses
}

func buildReviewPrompt(currentModel string, round1 []adapter.Response) string {
	var b strings.Builder
	b.WriteString("You are reviewing responses from other AI models. ")
	b.WriteString("Identify strengths, weaknesses, gaps, and inaccuracies in each response. ")
	b.WriteString("Be specific and constructive.\n\n")

	for _, resp := range round1 {
		if resp.Model == currentModel {
			continue
		}
		b.WriteString(fmt.Sprintf("=== Response from %s ===\n%s\n\n", resp.Model, resp.Content))
	}

	b.WriteString("Provide your detailed review of the above responses.")
	return b.String()
}

func buildSynthesisPrompt(round1, round2 []adapter.Response) string {
	var b strings.Builder
	b.WriteString("Below are initial responses from multiple AI models, followed by cross-reviews. ")
	b.WriteString("Synthesize the best possible answer by combining the strongest points from all responses ")
	b.WriteString("and addressing the issues raised in the reviews.\n\n")

	b.WriteString("## Initial Responses\n\n")
	for _, resp := range round1 {
		b.WriteString(fmt.Sprintf("=== %s ===\n%s\n\n", resp.Model, resp.Content))
	}

	b.WriteString("## Cross-Reviews\n\n")
	for _, resp := range round2 {
		b.WriteString(fmt.Sprintf("=== Review by %s ===\n%s\n\n", resp.Model, resp.Content))
	}

	b.WriteString("Provide your final synthesized answer.")
	return b.String()
}

func successfulResponses(responses []adapter.Response) []adapter.Response {
	var out []adapter.Response
	for _, r := range responses {
		if r.ExitCode == 0 && r.Content != "" {
			out = append(out, r)
		}
	}
	return out
}

func saveRound(s *store.Store, round int, responses []adapter.Response) {
	for _, resp := range responses {
		if err := s.SaveResponse(round, resp); err != nil {
			fmt.Printf("[warn] failed to save %s round-%d artifact: %v\n", resp.Model, round, err)
		}
	}
}

func printRoundHeader(round int, label string) {
	fmt.Printf("\n%s\n", strings.Repeat("=", 60))
	fmt.Printf("  Round %d — %s\n", round, label)
	fmt.Printf("%s\n\n", strings.Repeat("=", 60))
}

func printResponses(responses []adapter.Response) {
	for _, resp := range responses {
		fmt.Printf("--- %s (%.1fs) ---\n", resp.Model, resp.Duration.Seconds())
		if resp.Error != "" {
			fmt.Printf("[ERROR] %s\n", resp.Error)
		}
		if resp.Content != "" {
			// Truncate very long output for terminal display.
			content := resp.Content
			if len(content) > 3000 {
				content = content[:3000] + "\n... (truncated, see full output in runs/)"
			}
			fmt.Println(content)
		}
		fmt.Println()
	}
}
