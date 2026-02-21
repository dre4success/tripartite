package session

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/dre4success/tripartite/adapter"
	"github.com/dre4success/tripartite/logger"
	"github.com/dre4success/tripartite/orchestrator"
	"github.com/dre4success/tripartite/store"
)

// Config holds the configuration needed to start an interactive session.
type Config struct {
	Adapters []adapter.Adapter
	Timeout  time.Duration
	Approval adapter.ApprovalLevel
	Store    *store.Store
	Logger   *logger.Logger
}

// Start runs the interactive REPL loop. It reads prompts from stdin, runs the
// 3-round orchestration for each, and maintains conversation history so models
// can reference prior turns.
func Start(ctx context.Context, cfg Config) error {
	var turns []orchestrator.Turn

	modelNames := make([]string, len(cfg.Adapters))
	for i, a := range cfg.Adapters {
		modelNames[i] = a.Name()
	}

	fmt.Println("Tripartite interactive session")
	fmt.Printf("Models: %s\n", strings.Join(modelNames, ", "))
	fmt.Println("Type your prompt and press Enter. Commands: /quit, /exit, /history")
	fmt.Println()

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			break // EOF (e.g. Ctrl-D)
		}

		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		// Handle commands.
		switch strings.ToLower(input) {
		case "/quit", "/exit":
			fmt.Println("Ending session.")
			return saveAndFinish(cfg, modelNames, turns)
		case "/history":
			fmt.Printf("Session has %d turn(s).\n", len(turns))
			for i, t := range turns {
				fmt.Printf("  Turn %d: %s\n", i+1, truncate(t.Prompt, 80))
			}
			fmt.Println()
			continue
		}

		// Run orchestration for this turn.
		turnNum := len(turns) + 1
		fmt.Printf("\n--- Turn %d ---\n", turnNum)

		allRounds, err := orchestrator.Run(ctx, orchestrator.Config{
			Prompt:   input,
			Adapters: cfg.Adapters,
			Timeout:  cfg.Timeout,
			Approval: cfg.Approval,
			Store:    cfg.Store,
			History:  turns,
			TurnNum:  turnNum,
			Logger:   cfg.Logger,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Orchestration error: %v\n", err)
			fmt.Println("(You can try another prompt or /quit)")
			continue
		}

		turns = append(turns, orchestrator.Turn{
			Prompt:    input,
			Responses: allRounds,
		})

		fmt.Println()
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("reading stdin: %w", err)
	}

	// EOF reached — save and exit.
	fmt.Println("\nEnd of input. Ending session.")
	return saveAndFinish(cfg, modelNames, turns)
}

func saveAndFinish(cfg Config, modelNames []string, turns []orchestrator.Turn) error {
	if len(turns) == 0 {
		return nil
	}

	// Convert to store's SessionTurn type to avoid circular imports.
	storeTurns := make([]store.SessionTurn, len(turns))
	for i, t := range turns {
		storeTurns[i] = store.SessionTurn{
			Prompt:    t.Prompt,
			Responses: t.Responses,
		}
	}

	meta := store.RunMeta{
		Prompt:    turns[0].Prompt,
		Models:    modelNames,
		Timeout:   cfg.Timeout.String(),
		Timestamp: time.Now().Format(time.RFC3339),
		Mode:      "interactive",
	}

	if err := cfg.Store.SaveSessionSummary(meta, storeTurns); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to save session summary: %v\n", err)
	}

	fmt.Printf("Session artifacts saved to: %s\n", cfg.Store.RunDir)
	return nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}
