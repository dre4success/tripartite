package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/dre4success/tripartite/adapter"
	"github.com/dre4success/tripartite/logger"
	"github.com/dre4success/tripartite/runner"
	"github.com/dre4success/tripartite/store"
)

// Turn represents one user prompt and the 3 rounds of responses it produced.
type Turn struct {
	Prompt    string
	Responses [][]adapter.Response // [round1, round2, round3]
}

// Config holds the orchestrator's runtime configuration.
type Config struct {
	Prompt   string
	Adapters []adapter.Adapter
	Timeout  time.Duration
	Approval adapter.ApprovalLevel
	Store    *store.Store
	History  []Turn // prior conversation turns (empty for one-shot)
	TurnNum  int    // current turn number (0 = one-shot / first turn)
	Logger   *logger.Logger
}

// Run executes the full 3-round orchestration flow and returns all responses
// grouped by round.
func Run(ctx context.Context, cfg Config) ([][]adapter.Response, error) {
	allRounds := make([][]adapter.Response, 0, 3)

	// Build the initial prompt with conversation history prepended.
	initialPrompt := buildInitialPrompt(cfg.Prompt, cfg.History)

	// --- Round 1: Initial ---
	printRoundHeader(1, "Initial Response")
	r1 := fanOut(ctx, cfg.Adapters, initialPrompt, cfg.Timeout, cfg.Approval, 1, cfg.Logger)
	allRounds = append(allRounds, r1)
	saveTurnRound(cfg.Store, cfg.TurnNum, 1, r1)
	printResponses(r1)

	// Filter to only models that succeeded in round 1.
	r1ok := successfulResponses(r1)
	if len(r1ok) < 2 {
		fmt.Println("\n[!] Fewer than 2 successful responses — skipping cross-review and synthesis.")
		return allRounds, nil
	}
	r1Adapters := adaptersByName(cfg.Adapters, r1ok)

	// --- Round 2: Cross-Review (only successful models) ---
	printRoundHeader(2, "Cross-Review")
	r2 := fanOutReview(ctx, r1Adapters, r1ok, cfg.Timeout, cfg.Approval, 2, cfg.Logger)
	allRounds = append(allRounds, r2)
	saveTurnRound(cfg.Store, cfg.TurnNum, 2, r2)
	printResponses(r2)

	// Filter to only models that succeeded in round 2.
	r2ok := successfulResponses(r2)
	r2Adapters := adaptersByName(cfg.Adapters, r2ok)
	if len(r2Adapters) == 0 {
		r2Adapters = r1Adapters // fall back to round-1 survivors
	}

	// --- Round 3: Synthesis (only successful models) ---
	printRoundHeader(3, "Synthesis")
	r3 := fanOutSynthesis(ctx, r2Adapters, r1ok, r2ok, cfg.Timeout, cfg.Approval, 3, cfg.Logger)
	allRounds = append(allRounds, r3)
	saveTurnRound(cfg.Store, cfg.TurnNum, 3, r3)
	printResponses(r3)

	return allRounds, nil
}

// fanOut sends the same prompt to all adapters in parallel.
func fanOut(ctx context.Context, adapters []adapter.Adapter, prompt string, timeout time.Duration, approval adapter.ApprovalLevel, round int, log *logger.Logger) []adapter.Response {
	responses := make([]adapter.Response, len(adapters))
	var wg sync.WaitGroup

	for i, a := range adapters {
		wg.Add(1)
		go func(idx int, adp adapter.Adapter) {
			defer wg.Done()
			fmt.Printf("[r%d][start] %s\n", round, adp.Name())
			if log != nil {
				log.Debug("brainstorm round started", "round", round, "model", adp.Name(), "prompt_len", len(prompt))
			}
			responses[idx] = runner.Run(ctx, adp, prompt, timeout, approval)
			printLifecycle(round, responses[idx], log)
		}(i, a)
	}

	wg.Wait()
	return responses
}

// fanOutReview sends each model a prompt containing the other models' responses.
func fanOutReview(ctx context.Context, adapters []adapter.Adapter, round1 []adapter.Response, timeout time.Duration, approval adapter.ApprovalLevel, round int, log *logger.Logger) []adapter.Response {
	responses := make([]adapter.Response, len(adapters))
	var wg sync.WaitGroup

	for i, a := range adapters {
		wg.Add(1)
		go func(idx int, adp adapter.Adapter) {
			defer wg.Done()
			prompt := buildReviewPrompt(adp.Name(), round1)
			fmt.Printf("[r%d][start] %s\n", round, adp.Name())
			if log != nil {
				log.Debug("brainstorm review round started", "round", round, "model", adp.Name(), "prompt_len", len(prompt))
			}
			responses[idx] = runner.Run(ctx, adp, prompt, timeout, approval)
			printLifecycle(round, responses[idx], log)
		}(i, a)
	}

	wg.Wait()
	return responses
}

// fanOutSynthesis sends each model all round-1 and round-2 responses to synthesize.
func fanOutSynthesis(ctx context.Context, adapters []adapter.Adapter, round1, round2 []adapter.Response, timeout time.Duration, approval adapter.ApprovalLevel, round int, log *logger.Logger) []adapter.Response {
	responses := make([]adapter.Response, len(adapters))
	var wg sync.WaitGroup

	for i, a := range adapters {
		wg.Add(1)
		go func(idx int, adp adapter.Adapter) {
			defer wg.Done()
			prompt := buildSynthesisPrompt(round1, round2)
			fmt.Printf("[r%d][start] %s\n", round, adp.Name())
			if log != nil {
				log.Debug("brainstorm synthesis round started", "round", round, "model", adp.Name(), "prompt_len", len(prompt))
			}
			responses[idx] = runner.Run(ctx, adp, prompt, timeout, approval)
			printLifecycle(round, responses[idx], log)
		}(i, a)
	}

	wg.Wait()
	return responses
}

func printLifecycle(round int, resp adapter.Response, log *logger.Logger) {
	if resp.Error != "" || resp.ExitCode != 0 {
		errMsg := strings.TrimSpace(resp.Error)
		if errMsg == "" {
			errMsg = fmt.Sprintf("exit code %d", resp.ExitCode)
		}
		fmt.Printf("[r%d][error] %s: %s\n", round, resp.Model, errMsg)
		if log != nil {
			log.Warn("brainstorm round failed", "round", round, "model", resp.Model, "exit_code", resp.ExitCode, "error", errMsg)
		}
		return
	}

	fmt.Printf("[r%d][done] %s (%.1fs)\n", round, resp.Model, resp.Duration.Seconds())
	if log != nil {
		log.Debug("brainstorm round completed", "round", round, "model", resp.Model, "duration_sec", resp.Duration.Seconds(), "content_len", len(resp.Content))
	}
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

// adaptersByName returns the subset of adapters whose Name() matches a
// successful response, preserving the adapter order.
func adaptersByName(all []adapter.Adapter, responses []adapter.Response) []adapter.Adapter {
	names := make(map[string]bool, len(responses))
	for _, r := range responses {
		names[r.Model] = true
	}
	var out []adapter.Adapter
	for _, a := range all {
		if names[a.Name()] {
			out = append(out, a)
		}
	}
	return out
}

// maxHistoryTurns is the maximum number of prior turns included in the prompt.
// This prevents unbounded prompt growth in long interactive sessions, which
// could hit OS ARG_MAX limits or model context windows.
const maxHistoryTurns = 5

// buildInitialPrompt prepends conversation history to the user's current prompt
// so models see the full discussion thread. Only the last maxHistoryTurns turns
// are included; older turns are noted but omitted.
func buildInitialPrompt(prompt string, history []Turn) string {
	if len(history) == 0 {
		return prompt
	}

	var b strings.Builder
	b.WriteString("## Previous conversation\n\n")

	// Slide the window: only include the most recent turns.
	start := 0
	if len(history) > maxHistoryTurns {
		start = len(history) - maxHistoryTurns
		fmt.Fprintf(&b, "(Earlier %d turn(s) omitted for brevity)\n\n", start)
	}

	for i := start; i < len(history); i++ {
		turn := history[i]
		fmt.Fprintf(&b, "### Turn %d\n", i+1)
		fmt.Fprintf(&b, "User: %s\n\n", turn.Prompt)

		if len(turn.Responses) > 0 {
			// Show round-1 (initial) responses as the canonical model answers.
			for _, resp := range turn.Responses[0] {
				if resp.ExitCode == 0 && resp.Content != "" {
					fmt.Fprintf(&b, "%s: %s\n\n", resp.Model, resp.Content)
				}
			}
		}
	}

	fmt.Fprintf(&b, "### Current prompt\n%s", prompt)
	return b.String()
}

func saveTurnRound(s *store.Store, turnNum, round int, responses []adapter.Response) {
	if turnNum == 0 {
		// One-shot mode: use flat round-N directories (backwards compatible).
		for _, resp := range responses {
			if err := s.SaveResponse(round, resp); err != nil {
				fmt.Printf("[warn] failed to save %s round-%d artifact: %v\n", resp.Model, round, err)
			}
		}
		return
	}
	// Interactive mode: save under turn-N/round-N.
	for _, resp := range responses {
		if err := s.SaveTurnResponse(turnNum, round, resp); err != nil {
			fmt.Printf("[warn] failed to save %s turn-%d/round-%d artifact: %v\n", resp.Model, turnNum, round, err)
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
