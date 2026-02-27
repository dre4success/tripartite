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
	cycleEnabled := fs.Bool("cycle", true, "Enable task cycle state machine (default: true)")
	cycleLive := fs.String("cycle-live", "compact", "Cycle live updates: off|compact|verbose")
	resumeRun := fs.String("resume", "", "Resume a prior cycle from an existing run directory (requires --cycle)")
	resumeSession := fs.String("resume-session", "", "Resume a prior interactive meta session from an existing run directory")
	resumeTurn := fs.Int("resume-turn", 0, "Turn number to resume within --resume run (default: latest cycle turn)")
	runsDir := fs.String("runs-dir", "./runs", "Directory for run artifacts")
	debug := fs.Bool("debug", false, "Print structured diagnostics to stderr")
	allowAPIKeys := fs.Bool("allow-api-keys", false, "Don't fail if API key env vars are set")

	if args != nil {
		if err := fs.Parse(args); err != nil {
			os.Exit(1)
		}
	}
	log := logger.New(*debug)
	resumePath := strings.TrimSpace(*resumeRun)
	resumeSessionPath := strings.TrimSpace(*resumeSession)
	if resumePath != "" && resumeSessionPath != "" {
		fmt.Fprintln(os.Stderr, "Error: --resume and --resume-session are mutually exclusive")
		os.Exit(1)
	}
	if resumePath != "" && !*cycleEnabled {
		fmt.Fprintln(os.Stderr, "Error: --resume requires --cycle")
		os.Exit(1)
	}
	if resumeSessionPath != "" && *cycleEnabled {
		fmt.Fprintln(os.Stderr, "Error: --resume-session is for non-cycle interactive meta sessions")
		os.Exit(1)
	}
	if resumePath != "" && prompt != nil {
		fmt.Fprintln(os.Stderr, "Error: --resume cannot be used with a one-shot prompt; start interactive meta session instead")
		os.Exit(1)
	}
	if resumeSessionPath != "" && prompt != nil {
		fmt.Fprintln(os.Stderr, "Error: --resume-session cannot be used with a one-shot prompt; start interactive meta session instead")
		os.Exit(1)
	}
	if *resumeTurn < 0 {
		fmt.Fprintln(os.Stderr, "Error: --resume-turn must be >= 0")
		os.Exit(1)
	}
	if *resumeTurn > 0 && resumePath == "" {
		fmt.Fprintln(os.Stderr, "Error: --resume-turn requires --resume")
		os.Exit(1)
	}

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

	// Initialize artifact store (new run) or attach to an existing run for resume.
	var s *store.Store
	resumeDir := resumePath
	if resumeDir == "" {
		resumeDir = resumeSessionPath
	}
	if resumeDir != "" {
		abs, err := filepath.Abs(resumeDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to resolve --resume path: %v\n", err)
			os.Exit(1)
		}
		info, err := os.Stat(abs)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to open --resume path: %v\n", err)
			os.Exit(1)
		}
		if !info.IsDir() {
			fmt.Fprintf(os.Stderr, "Error: --resume path is not a directory: %s\n", abs)
			os.Exit(1)
		}
		s = &store.Store{BaseDir: filepath.Dir(abs), RunDir: abs}
		fmt.Printf("Resuming existing run directory: %s\n\n", s.RunDir)
	} else {
		s, err = store.New(*runsDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to create run directory: %v\n", err)
			os.Exit(1)
		}
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
		ResumeCycle:  resumePath != "",
		ResumeTurn:   *resumeTurn,
	}
	if resumeSessionPath != "" {
		state, err := s.LoadMetaSessionState()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to load session state: %v\n", err)
			os.Exit(1)
		}
		cfg.InitialTurns, cfg.AgentSessions = meta.RestoreSessionState(state)
		fmt.Printf("Loaded meta session state: %d turn(s), %d agent session(s)\n\n", len(cfg.InitialTurns), len(cfg.AgentSessions))
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
			if result.DecisionAction != nil {
				cycleTurn.DecisionAction = result.DecisionAction.Action
				cycleTurn.DecisionActionSummary = result.DecisionAction.Summary
				if cycleTurn.Error == "" {
					cycleTurn.Error = result.DecisionAction.Error
				}
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
			st.DecisionAction = turn.Delegate.DecisionAction
			st.DecisionActionSummary = turn.Delegate.DecisionActionSummary
			st.Error = turn.Delegate.DecisionActionError
			if st.DecisionAction == "" && turn.Delegate.Worktree.Enabled && len(turn.Delegate.Worktree.Commits) > 0 {
				st.DecisionAction = "keep_proposal"
				st.DecisionActionSummary = "one-shot mode: kept delegate proposal without applying worktree branch; rerun interactive mode to /approve apply action"
			}
		}
		if err := s.SaveMetaSessionSummary(inputMeta, []store.MetaSessionTurn{st}); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to save summary: %v\n", err)
		}

		fmt.Printf("\nRun artifacts saved to: %s\n", s.RunDir)
		return
	}

	// Interactive mode.
	if resumeDir == "" {
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
