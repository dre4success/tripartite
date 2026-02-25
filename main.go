package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dre4success/tripartite/adapter"
	"github.com/dre4success/tripartite/agent"
	"github.com/dre4success/tripartite/delegate"
	"github.com/dre4success/tripartite/logger"
	"github.com/dre4success/tripartite/meta"
	"github.com/dre4success/tripartite/models"
	"github.com/dre4success/tripartite/orchestrator"
	"github.com/dre4success/tripartite/preflight"
	"github.com/dre4success/tripartite/session"
	"github.com/dre4success/tripartite/store"
	"github.com/dre4success/tripartite/workspace"
)

func main() {
	if len(os.Args) < 2 {
		// No args → interactive meta session.
		runMeta(nil, nil)
		return
	}

	switch os.Args[1] {
	case "brainstorm":
		runBrainstorm(os.Args[2:])
	case "delegate":
		runDelegate(os.Args[2:])
	default:
		if strings.HasPrefix(os.Args[1], "-") {
			// Starts with flag → interactive mode with flags.
			runMeta(nil, os.Args[1:])
		} else {
			// Not a subcommand → treat as one-shot prompt.
			prompt := os.Args[1]
			runMeta(&prompt, os.Args[2:])
		}
	}
}

func runMeta(prompt *string, args []string) {
	fs := flag.NewFlagSet("meta", flag.ExitOnError)

	timeout := fs.Duration("timeout", 5*time.Minute, "Execution timeout")
	approval := fs.String("approval", "edit", "Approval mode: read|edit|full")
	modelList := fs.String("models", "claude,codex,gemini", "Adapters for brainstorm (comma-separated, colon model specs)")
	agentList := fs.String("agents", "claude", "Agents for delegate (comma-separated)")
	defaultAgent := fs.String("default-agent", "claude", "Default delegate agent")
	sandbox := fs.String("sandbox", "safe", "Delegate sandbox: safe|write|full")
	worktreeEnabled := fs.Bool("worktree", false, "Run meta-session delegate turns in isolated git worktrees")
	cycleEnabled := fs.Bool("cycle", false, "Enable task cycle state machine (experimental)")
	cycleLive := fs.String("cycle-live", "compact", "Cycle live updates: off|compact|verbose")
	runsDir := fs.String("runs-dir", "./runs", "Directory for run artifacts")
	debug := fs.Bool("debug", false, "Print structured diagnostics to stderr")
	allowAPIKeys := fs.Bool("allow-api-keys", false, "Don't fail if API key env vars are set")

	if args != nil {
		if err := fs.Parse(args); err != nil {
			os.Exit(1)
		}
	}
	log := logger.New(*debug)

	approvalLevel, err := adapter.ParseApprovalLevel(*approval)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	cycleLiveMode, err := meta.ParseLiveCycleVerbosity(*cycleLive)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Build adapters from --models.
	var adapters []adapter.Adapter
	for _, spec := range strings.Split(*modelList, ",") {
		spec = strings.TrimSpace(spec)
		if spec == "" {
			continue
		}
		parts := strings.SplitN(spec, ":", 2)
		name := parts[0]

		factory, ok := adapter.Registry[name]
		if !ok {
			fmt.Fprintf(os.Stderr, "Error: unknown model %q (available: claude, codex, gemini)\n", name)
			os.Exit(1)
		}
		a := factory()
		if len(parts) == 2 {
			resolved := models.ResolveModel(name, parts[1])
			a.SetModel(resolved)
		}
		adapters = append(adapters, a)
	}

	// Build agents from --agents.
	var agents []agent.Agent
	for _, name := range strings.Split(*agentList, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		factory, ok := agent.Registry[name]
		if !ok {
			fmt.Fprintf(os.Stderr, "Error: unknown agent %q (available: claude, codex, gemini)\n", name)
			os.Exit(1)
		}
		agents = append(agents, factory())
	}

	// Unified preflight: need at least 1 adapter or 1 agent.
	fmt.Println("Running preflight checks...")
	unified, err := preflight.CheckAll(adapters, agents, *allowAPIKeys)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Preflight failed: %v\n", err)
		os.Exit(1)
	}

	for name, reason := range unified.Adapters.Skipped {
		fmt.Printf("  [skip] adapter %s: %s\n", name, reason)
	}
	for name, reason := range unified.Agents.Skipped {
		fmt.Printf("  [skip] agent %s: %s\n", name, reason)
	}
	if len(unified.AdapterNames) > 0 {
		fmt.Printf("  [ready] adapters: %s\n", strings.Join(unified.AdapterNames, ", "))
	}
	if len(unified.AgentNames) > 0 {
		fmt.Printf("  [ready] agents: %s\n", strings.Join(unified.AgentNames, ", "))
	}
	fmt.Println()

	// Initialize artifact store.
	s, err := store.New(*runsDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create run directory: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()

	cfg := meta.Config{
		Adapters:     unified.Adapters.Ready,
		Approval:     approvalLevel,
		Agents:       unified.Agents.Ready,
		Sandbox:      *sandbox,
		Worktree:     *worktreeEnabled,
		Timeout:      *timeout,
		Store:        s,
		Logger:       log,
		DefaultAgent: *defaultAgent,
		CycleEnabled: *cycleEnabled,
		CycleLive:    cycleLiveMode,
	}

	if prompt != nil {
		// One-shot mode.
		allModels := append(unified.AdapterNames, unified.AgentNames...)
		mode := "meta-oneshot"
		if *cycleEnabled {
			mode = "meta-cycle-oneshot"
		}
		inputMeta := store.RunMeta{
			Prompt:    *prompt,
			Models:    allModels,
			Timeout:   timeout.String(),
			Timestamp: time.Now().Format(time.RFC3339),
			Mode:      mode,
		}
		if err := s.SaveInput(inputMeta); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to save input metadata: %v\n", err)
			os.Exit(1)
		}

		if *cycleEnabled {
			result, err := meta.RunOnceCycle(ctx, cfg, *prompt, 1)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				fmt.Printf("Run artifacts saved to: %s\n", s.RunDir)
				os.Exit(1)
			}
			cycleTurn := store.MetaSessionTurn{
				Prompt:     *prompt,
				Engine:     "cycle",
				CycleID:    result.CycleID,
				CycleState: string(result.FinalState),
			}
			if result.Decision != nil {
				cycleTurn.FinalText = result.Decision.Recommendation
			}
			if result.FinalState == "ABORTED" && cycleTurn.FinalText == "" {
				cycleTurn.Error = "cycle aborted"
			}
			if err := s.SaveMetaSessionSummary(inputMeta, []store.MetaSessionTurn{cycleTurn}); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to save summary: %v\n", err)
			}
			fmt.Printf("\n[cycle] %s completed (state: %s, elapsed: %.1fs)\n",
				result.CycleID, result.FinalState, result.Elapsed.Seconds())
			fmt.Printf("Run artifacts saved to: %s\n", s.RunDir)
			return
		}

		turn, err := meta.RunOnce(ctx, cfg, *prompt, nil, 1)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			fmt.Printf("Run artifacts saved to: %s\n", s.RunDir)
			os.Exit(1)
		}

		// Save single-turn meta session summary.
		var st store.MetaSessionTurn
		st.Prompt = turn.Prompt
		if turn.Brainstorm != nil {
			st.Engine = "brainstorm"
			st.Responses = turn.Brainstorm.Rounds
		} else if turn.Delegate != nil {
			st.Engine = "delegate"
			st.Agent = turn.Delegate.Agent
			st.FinalText = turn.Delegate.FinalText
		}
		if err := s.SaveMetaSessionSummary(inputMeta, []store.MetaSessionTurn{st}); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to save summary: %v\n", err)
		}

		fmt.Printf("\nRun artifacts saved to: %s\n", s.RunDir)
		return
	}

	// Interactive mode.
	allModels := append(unified.AdapterNames, unified.AgentNames...)
	inputMeta := store.RunMeta{
		Models:    allModels,
		Timeout:   timeout.String(),
		Timestamp: time.Now().Format(time.RFC3339),
		Mode:      "meta-interactive",
	}
	if err := s.SaveInput(inputMeta); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to save input metadata: %v\n", err)
		os.Exit(1)
	}

	if err := meta.Start(ctx, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Session error: %v\n", err)
		os.Exit(1)
	}
}

func runDelegate(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: tripartite delegate <agent> \"<prompt>\" [flags]")
		os.Exit(1)
	}

	agentName := strings.TrimSpace(args[0])
	prompt := strings.TrimSpace(args[1])
	if prompt == "" {
		fmt.Fprintln(os.Stderr, "Error: prompt must not be empty")
		os.Exit(1)
	}

	fs := flag.NewFlagSet("delegate", flag.ExitOnError)
	model := fs.String("model", "", "Model alias or ID for the selected agent")
	sandbox := fs.String("sandbox", "safe", "Sandbox level: safe|write|full")
	timeout := fs.Duration("timeout", 10*time.Minute, "Delegate execution timeout")
	runsDir := fs.String("runs-dir", "./runs", "Directory for run artifacts")
	worktreeEnabled := fs.Bool("worktree", false, "Run delegate task in an isolated git worktree")
	allowAPIKeys := fs.Bool("allow-api-keys", false, "Don't fail if API key env vars are set")
	if err := fs.Parse(args[2:]); err != nil {
		os.Exit(1)
	}

	factory, ok := agent.Registry[agentName]
	if !ok {
		fmt.Fprintf(os.Stderr, "Error: unknown agent %q (available: claude, codex, gemini)\n", agentName)
		os.Exit(1)
	}

	fmt.Println("Running delegate preflight checks...")
	result, err := preflight.CheckAgents([]agent.Agent{factory()}, *allowAPIKeys, 1)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Preflight failed: %v\n", err)
		os.Exit(1)
	}
	a := result.Ready[0]
	fmt.Printf("  [ready] %s\n\n", a.Name())

	s, err := store.New(*runsDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create run directory: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()
	runCwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to determine working directory: %v\n", err)
		os.Exit(1)
	}

	var wsInfo store.DelegateWorkspace
	if *worktreeEnabled {
		if err := preflight.CheckWorktreePrereqs(ctx, runCwd); err != nil {
			fmt.Fprintf(os.Stderr, "Preflight failed: %v\n", err)
			os.Exit(1)
		}
		taskID := filepath.Base(s.RunDir)
		info, err := workspace.Prepare(ctx, runCwd, taskID, a.Name())
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to prepare worktree: %v\n", err)
			os.Exit(1)
		}
		wsInfo = store.DelegateWorkspace{
			Enabled:      true,
			TaskID:       info.TaskID,
			WorktreePath: info.WorktreePath,
			Branch:       info.Branch,
			BaseCommit:   info.BaseCommit,
		}
		runCwd = info.WorktreePath
		fmt.Printf("Using worktree: %s\n", runCwd)
	}

	fmt.Printf("Delegating to %s...\n\n", a.Name())
	if err := delegate.Run(ctx, delegate.Config{
		Agent:    a,
		Prompt:   prompt,
		Model:    *model,
		Sandbox:  *sandbox,
		Timeout:  *timeout,
		Cwd:      runCwd,
		Store:    s,
		Worktree: wsInfo,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "\nDelegate error: %v\n", err)
		fmt.Printf("Run artifacts saved to: %s\n", s.RunDir)
		os.Exit(1)
	}

	fmt.Printf("\nRun artifacts saved to: %s\n", s.RunDir)
}

func runBrainstorm(args []string) {
	fs := flag.NewFlagSet("brainstorm", flag.ExitOnError)

	prompt := fs.String("p", "", "The prompt to send (omit for interactive mode)")
	timeout := fs.Duration("timeout", 5*time.Minute, "Per-model execution timeout")
	approval := fs.String("approval", "edit", "Approval mode for brainstorm tool actions: read|edit|full")
	allowAPIKeys := fs.Bool("allow-api-keys", false, "Don't fail if API key env vars are set")
	modelList := fs.String("models", "claude,codex,gemini", "Comma-separated list of agent:model specs (e.g. claude:opus,codex:o3,gemini:3-flash)")
	runsDir := fs.String("runs-dir", "./runs", "Directory for run artifacts")
	debug := fs.Bool("debug", false, "Print structured diagnostics to stderr")

	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}
	log := logger.New(*debug)
	approvalLevel, err := adapter.ParseApprovalLevel(*approval)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Resolve adapters from model specs (e.g. "claude:opus", "codex", "gemini:3-flash").
	var adapters []adapter.Adapter
	for _, spec := range strings.Split(*modelList, ",") {
		spec = strings.TrimSpace(spec)
		parts := strings.SplitN(spec, ":", 2)
		name := parts[0]

		factory, ok := adapter.Registry[name]
		if !ok {
			fmt.Fprintf(os.Stderr, "Error: unknown model %q (available: claude, codex, gemini)\n", name)
			os.Exit(1)
		}
		a := factory()
		if len(parts) == 2 {
			resolved := models.ResolveModel(name, parts[1])
			a.SetModel(resolved)
		}
		adapters = append(adapters, a)
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
			Approval: approvalLevel,
			Store:    s,
			Logger:   log,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "Session error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// One-shot mode: run single prompt and exit.
	inputMeta := store.RunMeta{
		Prompt:    *prompt,
		Models:    readyNames,
		Timeout:   timeout.String(),
		Timestamp: time.Now().Format(time.RFC3339),
		Mode:      "one-shot",
	}
	if err := s.SaveInput(inputMeta); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to save input metadata: %v\n", err)
		os.Exit(1)
	}

	allRounds, err := orchestrator.Run(ctx, orchestrator.Config{
		Prompt:   *prompt,
		Adapters: result.Ready,
		Timeout:  *timeout,
		Approval: approvalLevel,
		Store:    s,
		Logger:   log,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Orchestration error: %v\n", err)
		os.Exit(1)
	}

	if err := s.SaveSummary(inputMeta, allRounds); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to save summary: %v\n", err)
	}

	fmt.Printf("\nRun artifacts saved to: %s\n", s.RunDir)
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "Tripartite — Meta-Agent Shell")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Usage:")
	fmt.Fprintln(os.Stderr, "  tripartite                                Interactive meta session")
	fmt.Fprintln(os.Stderr, "  tripartite \"<prompt>\" [flags]              One-shot auto mode")
	fmt.Fprintln(os.Stderr, "  tripartite brainstorm -p \"...\" [flags]     Multi-agent brainstorm (advanced)")
	fmt.Fprintln(os.Stderr, "  tripartite delegate <agent> \"...\" [flags]  Single-agent delegate (advanced)")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Meta Session Flags:")
	fmt.Fprintln(os.Stderr, "  --timeout duration      Execution timeout (default 5m)")
	fmt.Fprintln(os.Stderr, "  --approval string       Approval mode: read|edit|full (default \"edit\")")
	fmt.Fprintln(os.Stderr, "  --models string         Adapters for brainstorm (default \"claude,codex,gemini\")")
	fmt.Fprintln(os.Stderr, "  --agents string         Agents for delegate (default \"claude\")")
	fmt.Fprintln(os.Stderr, "  --default-agent string  Default delegate agent (default \"claude\")")
	fmt.Fprintln(os.Stderr, "  --sandbox string        Delegate sandbox: safe|write|full (default \"safe\")")
	fmt.Fprintln(os.Stderr, "  --worktree              Run meta-session delegate turns in isolated git worktrees")
	fmt.Fprintln(os.Stderr, "  --cycle                 Enable task cycle state machine (experimental)")
	fmt.Fprintln(os.Stderr, "  --runs-dir string       Directory for run artifacts (default \"./runs\")")
	fmt.Fprintln(os.Stderr, "  --debug                 Print structured diagnostics to stderr")
	fmt.Fprintln(os.Stderr, "  --allow-api-keys        Don't fail if API key env vars are set")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Interactive commands:")
	fmt.Fprintln(os.Stderr, "  /brainstorm <prompt>        Force multi-agent brainstorm")
	fmt.Fprintln(os.Stderr, "  /delegate [agent] <prompt>  Force single-agent delegate")
	fmt.Fprintln(os.Stderr, "  /history                    Show turn summaries")
	fmt.Fprintln(os.Stderr, "  /help                       Show available commands")
	fmt.Fprintln(os.Stderr, "  /quit, /exit                End session")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Brainstorm Flags:")
	fmt.Fprintln(os.Stderr, "  -p string               The prompt to send (omit for interactive mode)")
	fmt.Fprintln(os.Stderr, "  --timeout duration      Per-model execution timeout (default 5m)")
	fmt.Fprintln(os.Stderr, "  --approval string       Approval mode for tool actions: read|edit|full (default \"edit\")")
	fmt.Fprintln(os.Stderr, "  --allow-api-keys        Don't fail if API key env vars are set")
	fmt.Fprintln(os.Stderr, "  --models string         Comma-separated agent:model specs (default \"claude,codex,gemini\")")
	fmt.Fprintln(os.Stderr, "  --debug                 Print structured diagnostics to stderr")
	fmt.Fprintln(os.Stderr, "  --runs-dir string       Directory for run artifacts (default \"./runs\")")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Delegate Flags:")
	fmt.Fprintln(os.Stderr, "  --model string          Model alias or ID for selected agent")
	fmt.Fprintln(os.Stderr, "  --sandbox string        Sandbox level: safe|write|full (default \"safe\")")
	fmt.Fprintln(os.Stderr, "  --worktree              Run in isolated git worktree")
	fmt.Fprintln(os.Stderr, "  --timeout duration      Delegate timeout (default 10m)")
	fmt.Fprintln(os.Stderr, "  --allow-api-keys        Don't fail if API key env vars are set")
	fmt.Fprintln(os.Stderr, "  --runs-dir string       Directory for run artifacts (default \"./runs\")")
}
