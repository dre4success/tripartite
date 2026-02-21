package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/dre4success/tripartite/adapter"
	"github.com/dre4success/tripartite/orchestrator"
	"github.com/dre4success/tripartite/preflight"
	"github.com/dre4success/tripartite/session"
	"github.com/dre4success/tripartite/store"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "brainstorm":
		runBrainstorm(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown subcommand: %s\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func runBrainstorm(args []string) {
	fs := flag.NewFlagSet("brainstorm", flag.ExitOnError)

	prompt := fs.String("p", "", "The prompt to send (omit for interactive mode)")
	timeout := fs.Duration("timeout", 120*time.Second, "Per-model execution timeout")
	allowAPIKeys := fs.Bool("allow-api-keys", false, "Don't fail if API key env vars are set")
	models := fs.String("models", "claude,codex,gemini", "Comma-separated list of models to use")
	runsDir := fs.String("runs-dir", "./runs", "Directory for run artifacts")

	fs.Parse(args)

	// Resolve adapters from model names.
	modelNames := strings.Split(*models, ",")
	var adapters []adapter.Adapter
	for _, name := range modelNames {
		name = strings.TrimSpace(name)
		factory, ok := adapter.Registry[name]
		if !ok {
			fmt.Fprintf(os.Stderr, "Error: unknown model %q (available: claude, codex, gemini)\n", name)
			os.Exit(1)
		}
		adapters = append(adapters, factory())
	}

	// Determine minimum models: 2 if multiple requested, 1 if single.
	minModels := 2
	if len(adapters) == 1 {
		minModels = 1
	}

	// Preflight checks.
	fmt.Println("Running preflight checks...")
	result, err := preflight.Check(adapters, *allowAPIKeys, minModels)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Preflight failed: %v\n", err)
		os.Exit(1)
	}

	for name, reason := range result.Skipped {
		fmt.Printf("  [skip] %s: %s\n", name, reason)
	}
	readyNames := make([]string, 0, len(result.Ready))
	for _, a := range result.Ready {
		readyNames = append(readyNames, a.Name())
	}
	fmt.Printf("  [ready] %s\n\n", strings.Join(readyNames, ", "))

	// Initialize artifact store.
	s, err := store.New(*runsDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create run directory: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()

	if *prompt == "" {
		// Interactive mode: enter REPL.
		meta := store.RunMeta{
			Models:    readyNames,
			Timeout:   timeout.String(),
			Timestamp: time.Now().Format(time.RFC3339),
			Mode:      "interactive",
		}
		if err := s.SaveInput(meta); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to save input metadata: %v\n", err)
			os.Exit(1)
		}

		if err := session.Start(ctx, session.Config{
			Adapters: result.Ready,
			Timeout:  *timeout,
			Store:    s,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "Session error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// One-shot mode: run single prompt and exit.
	meta := store.RunMeta{
		Prompt:    *prompt,
		Models:    readyNames,
		Timeout:   timeout.String(),
		Timestamp: time.Now().Format(time.RFC3339),
		Mode:      "one-shot",
	}
	if err := s.SaveInput(meta); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to save input metadata: %v\n", err)
		os.Exit(1)
	}

	allRounds, err := orchestrator.Run(ctx, orchestrator.Config{
		Prompt:   *prompt,
		Adapters: result.Ready,
		Timeout:  *timeout,
		Store:    s,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Orchestration error: %v\n", err)
		os.Exit(1)
	}

	if err := s.SaveSummary(meta, allRounds); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to save summary: %v\n", err)
	}

	fmt.Printf("\nRun artifacts saved to: %s\n", s.RunDir)
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "Tripartite — Multi-LLM Orchestrator CLI")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Usage:")
	fmt.Fprintln(os.Stderr, "  tripartite brainstorm -p \"your prompt here\" [flags]   # one-shot mode")
	fmt.Fprintln(os.Stderr, "  tripartite brainstorm [flags]                         # interactive mode")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Subcommands:")
	fmt.Fprintln(os.Stderr, "  brainstorm  Send a prompt to multiple AI CLIs for collaborative analysis")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Flags:")
	fmt.Fprintln(os.Stderr, "  -p string          The prompt to send (omit for interactive mode)")
	fmt.Fprintln(os.Stderr, "  --timeout duration  Per-model execution timeout (default 120s)")
	fmt.Fprintln(os.Stderr, "  --allow-api-keys   Don't fail if API key env vars are set")
	fmt.Fprintln(os.Stderr, "  --models string    Comma-separated models (default \"claude,codex,gemini\")")
	fmt.Fprintln(os.Stderr, "  --runs-dir string  Directory for run artifacts (default \"./runs\")")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Interactive commands:")
	fmt.Fprintln(os.Stderr, "  /quit, /exit       End the session")
	fmt.Fprintln(os.Stderr, "  /history           Show conversation turn count")
}
