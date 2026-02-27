package meta

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/dre4success/tripartite/adapter"
	"github.com/dre4success/tripartite/agent"
	"github.com/dre4success/tripartite/cycle"
	"github.com/dre4success/tripartite/display"
	"github.com/dre4success/tripartite/logger"
	"github.com/dre4success/tripartite/orchestrator"
	"github.com/dre4success/tripartite/preflight"
	"github.com/dre4success/tripartite/router"
	"github.com/dre4success/tripartite/store"
	"github.com/dre4success/tripartite/stream"
	"github.com/dre4success/tripartite/workspace"
)

// Turn captures the result of one meta session turn.
type Turn struct {
	Prompt     string
	Route      router.Result
	Brainstorm *BrainstormResult
	Delegate   *DelegateResult
	Cycle      *CycleResult
}

// BrainstormResult holds the 3-round orchestration output.
type BrainstormResult struct {
	Rounds [][]adapter.Response
}

// DelegateResult holds the streaming delegation output.
type DelegateResult struct {
	Agent      string
	EventCount int
	FinalText  string
}

// CycleResult holds a summarized outcome from the cycle state machine.
type CycleResult struct {
	CycleID        string
	FinalState     string
	Recommendation string
	Elapsed        time.Duration
	Error          string
}

// Config holds the configuration for a meta session.
type Config struct {
	Adapters     []adapter.Adapter
	Approval     adapter.ApprovalLevel
	Agents       []agent.Agent
	Sandbox      string
	Worktree     bool
	Timeout      time.Duration
	Store        *store.Store
	Logger       *logger.Logger
	DefaultAgent string
	CycleEnabled bool
	CycleLive    LiveCycleVerbosity
	ResumeCycle  bool
	ResumeTurn   int
}

// Start launches the interactive meta session REPL.
func Start(ctx context.Context, cfg Config) error {
	var turns []Turn

	adapterNames := make([]string, len(cfg.Adapters))
	for i, a := range cfg.Adapters {
		adapterNames[i] = a.Name()
	}
	agentNames := make([]string, len(cfg.Agents))
	for i, a := range cfg.Agents {
		agentNames[i] = a.Name()
	}
	effectiveCycleLive := cfg.CycleLive
	if effectiveCycleLive == "" {
		effectiveCycleLive = LiveCycleCompact
	}

	fmt.Println("Tripartite meta session")
	fmt.Printf("Adapters: %s\n", strings.Join(adapterNames, ", "))
	fmt.Printf("Agents: %s\n", strings.Join(agentNames, ", "))
	if cfg.CycleEnabled {
		fmt.Println("Mode: cycle state machine (experimental)")
		fmt.Printf("Live updates: %s\n", effectiveCycleLive)
		fmt.Println("Commands: /status, /board, /timeline, /live, /resume, /clarify, /approve, /deny, /stop, /history, /help, /quit")
	} else {
		fmt.Println("Commands: /brainstorm, /delegate, /history, /help, /quit")
	}
	fmt.Println()

	// Shared approval/clarification brokers and status provider for cycle ↔ REPL coordination.
	var broker *cycle.ApprovalBroker
	var clarifier *cycle.ClarificationBroker
	var statusProvider *cycle.StatusProvider
	if cfg.CycleEnabled {
		broker = cycle.NewApprovalBroker()
		clarifier = cycle.NewClarificationBroker()
		statusProvider = cycle.NewStatusProvider()
	}

	// Channels for async cycle results.
	type cycleResult struct {
		result *cycle.Result
		err    error
	}
	var cycleDone chan cycleResult
	var cycleCancel context.CancelFunc
	var cycleRunning bool
	var cyclePrompt string
	var cycleLiveStop chan struct{}
	cycleLiveMode := effectiveCycleLive

	stopCycleLiveWatcher := func() {
		if cycleLiveStop != nil {
			close(cycleLiveStop)
			cycleLiveStop = nil
		}
	}
	startOrRestartCycleLiveWatcher := func() {
		stopCycleLiveWatcher()
		if !cycleRunning || statusProvider == nil || cycleLiveMode == LiveCycleOff {
			return
		}
		cycleLiveStop = make(chan struct{})
		startCycleLiveWatcher(cycleLiveStop, statusProvider, cycleLiveMode)
	}
	startCycleLoop := func(prompt string, turnNum int, resume bool) {
		cycleDone = make(chan cycleResult, 1)
		var cycleCtx context.Context
		cycleCtx, cycleCancel = context.WithCancel(ctx)
		cycleRunning = true

		cycleCfg := buildCycleConfig(cfg, prompt, turnNum, broker, clarifier, statusProvider)
		go func() {
			var (
				result *cycle.Result
				err    error
			)
			if resume {
				result, err = cycle.RunResume(cycleCtx, cycleCfg)
			} else {
				result, err = cycle.Run(cycleCtx, cycleCfg)
			}
			cycleDone <- cycleResult{result: result, err: err}
		}()
		startOrRestartCycleLiveWatcher()
	}

	if cfg.CycleEnabled && cfg.ResumeCycle {
		turnNum := cfg.ResumeTurn
		startCycleLoop("", turnNum, true)

		cyclePrompt = "[resume]"
		if turnNum > 0 {
			cyclePrompt = fmt.Sprintf("[resume turn %d]", turnNum)
		}
		fmt.Printf("[cycle] Resuming prior cycle from: %s\n", cfg.Store.RunDir)
		if turnNum > 0 {
			fmt.Printf("[cycle] Requested turn: %d\n", turnNum)
		} else {
			fmt.Println("[cycle] Requested turn: latest cycle turn")
		}
		fmt.Println("[cycle] Use /status for progress, /approve|/deny for approvals, and /clarify for clarification tickets.")
	}

	consumeCycleResult := func(cr cycleResult) {
		cycleRunning = false
		stopCycleLiveWatcher()
		if statusProvider != nil {
			statusProvider.Clear()
		}
		summary := handleCycleResult(cr.result, cr.err)
		if summary != nil {
			turns = append(turns, Turn{
				Prompt: cyclePrompt,
				Route: router.Result{
					Reason: "cycle state machine",
				},
				Cycle: summary,
			})
		}
		cyclePrompt = ""
		cycleCancel = nil
	}

	scanner := bufio.NewScanner(os.Stdin)
	for {
		if cycleRunning {
			fmt.Print("[cycle] > ")
		} else {
			fmt.Print("> ")
		}
		if !scanner.Scan() {
			break
		}

		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			// Check if a cycle finished while we were waiting.
			if cycleRunning {
				select {
				case cr := <-cycleDone:
					consumeCycleResult(cr)
				default:
				}
			}
			continue
		}

		cmd, arg := parseSlashCommand(input)

		switch cmd {
		case "quit", "exit":
			if cycleRunning && cycleCancel != nil {
				stopCycleLiveWatcher()
				cycleCancel()
			}
			fmt.Println("Ending session.")
			return saveMetaSession(cfg, adapterNames, agentNames, turns)

		case "history":
			fmt.Printf("Session has %d turn(s).\n", len(turns))
			for i, t := range turns {
				engine := "brainstorm"
				if t.Delegate != nil {
					engine = "delegate → " + t.Delegate.Agent
				} else if t.Cycle != nil {
					engine = "cycle"
				}
				fmt.Printf("  Turn %d [%s]: %s\n", i+1, engine, truncate(t.Prompt, 70))
			}
			fmt.Println()
			continue

		case "help":
			printHelp()
			if cfg.CycleEnabled {
				printCycleHelp()
			}
			continue

		case "status":
			if !cfg.CycleEnabled {
				fmt.Println("Cycle mode not enabled. Use --cycle flag.")
				continue
			}
			if !cycleRunning {
				fmt.Println("No cycle is currently running.")
				continue
			}
			printCycleStatus(statusProvider, broker, clarifier)
			continue

		case "board":
			if !cfg.CycleEnabled {
				fmt.Println("Cycle mode not enabled. Use --cycle flag.")
				continue
			}
			if !cycleRunning {
				fmt.Println("No cycle is currently running.")
				continue
			}
			printCycleBoard(statusProvider)
			continue

		case "timeline":
			if !cfg.CycleEnabled {
				fmt.Println("Cycle mode not enabled. Use --cycle flag.")
				continue
			}
			if !cycleRunning {
				fmt.Println("No cycle is currently running.")
				continue
			}
			limit := 10
			if arg != "" {
				n, err := strconv.Atoi(strings.TrimSpace(arg))
				if err != nil || n <= 0 {
					fmt.Println("Usage: /timeline [count]")
					continue
				}
				limit = n
			}
			printCycleTimeline(statusProvider, limit)
			continue

		case "live":
			if !cfg.CycleEnabled {
				fmt.Println("Cycle mode not enabled. Use --cycle flag.")
				continue
			}
			modeArg := strings.TrimSpace(arg)
			if modeArg == "" {
				fmt.Printf("Cycle live mode: %s\n", cycleLiveMode)
				fmt.Println("Usage: /live <off|compact|verbose>")
				continue
			}
			mode, err := ParseLiveCycleVerbosity(modeArg)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				continue
			}
			if mode == cycleLiveMode {
				fmt.Printf("Cycle live mode unchanged: %s\n", mode)
				continue
			}
			cycleLiveMode = mode
			if cycleRunning {
				startOrRestartCycleLiveWatcher()
			}
			fmt.Printf("Cycle live mode set to: %s\n", cycleLiveMode)
			continue

		case "resume":
			if !cfg.CycleEnabled {
				fmt.Println("Cycle mode not enabled. Use --cycle flag.")
				continue
			}
			if cycleRunning {
				fmt.Println("A cycle is currently running. Use /stop first.")
				continue
			}
			turnNum, err := parseResumeTurnArg(arg)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				fmt.Println("Usage: /resume [turn]")
				continue
			}
			startCycleLoop("", turnNum, true)

			cyclePrompt = "[resume]"
			if turnNum > 0 {
				cyclePrompt = fmt.Sprintf("[resume turn %d]", turnNum)
			}
			fmt.Printf("[cycle] Resuming prior cycle from: %s\n", cfg.Store.RunDir)
			if turnNum > 0 {
				fmt.Printf("[cycle] Requested turn: %d\n", turnNum)
			} else {
				fmt.Println("[cycle] Requested turn: latest cycle turn")
			}
			fmt.Println("[cycle] Use /status for progress, /approve|/deny for approvals, and /clarify for clarification tickets.")
			continue

		case "clarify":
			if !cfg.CycleEnabled || clarifier == nil {
				fmt.Println("Cycle mode not enabled.")
				continue
			}
			arg = strings.TrimSpace(arg)
			if arg == "" {
				fmt.Println("Usage: /clarify [ticket-id] <answer>")
				continue
			}
			ticketID, answer, err := parseClarifyArg(arg)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				fmt.Println("Usage: /clarify [ticket-id] <answer>")
				continue
			}
			if ticketID == "" {
				pending := clarifier.Pending()
				if len(pending) == 0 {
					fmt.Println("No pending clarifications.")
					continue
				}
				ticketID = pending[0].TicketID
			}
			if err := clarifier.Resolve(ticketID, answer); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			} else {
				fmt.Printf("Clarified: %s\n", ticketID)
			}
			continue

		case "approve":
			if !cfg.CycleEnabled || broker == nil {
				fmt.Println("Cycle mode not enabled.")
				continue
			}
			ticketID := strings.TrimSpace(arg)
			if ticketID == "" {
				// Auto-approve the first pending ticket.
				pending := broker.Pending()
				if len(pending) == 0 {
					fmt.Println("No pending approvals.")
					continue
				}
				ticketID = pending[0].TicketID
			}
			if err := broker.Resolve(ticketID, true, ""); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			} else {
				fmt.Printf("Approved: %s\n", ticketID)
			}
			continue

		case "deny":
			if !cfg.CycleEnabled || broker == nil {
				fmt.Println("Cycle mode not enabled.")
				continue
			}
			ticketID := strings.TrimSpace(arg)
			if ticketID == "" {
				pending := broker.Pending()
				if len(pending) == 0 {
					fmt.Println("No pending approvals.")
					continue
				}
				ticketID = pending[0].TicketID
			}
			if err := broker.Resolve(ticketID, false, ""); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			} else {
				fmt.Printf("Denied: %s\n", ticketID)
			}
			continue

		case "stop":
			if !cfg.CycleEnabled {
				fmt.Println("Cycle mode not enabled.")
				continue
			}
			if !cycleRunning {
				fmt.Println("No cycle is currently running.")
				continue
			}
			if cycleCancel != nil {
				cycleCancel()
				fmt.Println("Cycle stop requested.")
			}
			continue

		case "brainstorm":
			if cycleRunning {
				fmt.Println("A cycle is currently running. Use /stop first.")
				continue
			}
			if arg == "" {
				fmt.Println("Usage: /brainstorm <prompt>")
				continue
			}
			turn, err := runOnceForced(ctx, cfg, arg, turns, len(turns)+1, router.IntentBrainstorm, "", false)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				continue
			}
			turns = append(turns, *turn)
			continue

		case "delegate":
			if cycleRunning {
				fmt.Println("A cycle is currently running. Use /stop first.")
				continue
			}
			if arg == "" {
				fmt.Println("Usage: /delegate [agent] <prompt>")
				continue
			}
			agentName, prompt, explicitAgent := parseDelegateArg(arg, cfg)
			if prompt == "" {
				fmt.Println("Usage: /delegate [agent] <prompt>")
				continue
			}
			turn, err := runOnceForced(ctx, cfg, prompt, turns, len(turns)+1, router.IntentDelegate, agentName, explicitAgent)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				continue
			}
			turns = append(turns, *turn)
			continue

		default:
			if cycleRunning {
				// Check if cycle finished.
				select {
				case cr := <-cycleDone:
					consumeCycleResult(cr)
				default:
					fmt.Println("A cycle is currently running. Use /status, /approve, /deny, or /stop.")
					continue
				}
			}

			if cfg.CycleEnabled {
				// Launch cycle asynchronously.
				turnNum := len(turns) + 1
				startCycleLoop(input, turnNum, false)

				cyclePrompt = input
				fmt.Printf("[cycle] Started for: %q\n", truncate(input, 60))
				fmt.Println("[cycle] Use /status for progress, /approve|/deny for approvals, and /clarify for clarification tickets.")
				continue
			}

			// Legacy path — route automatically.
			turn, err := RunOnce(ctx, cfg, input, turns, len(turns)+1)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				fmt.Println("(You can try another prompt or /quit)")
				continue
			}
			turns = append(turns, *turn)
		}
	}

	if cycleRunning && cycleCancel != nil {
		stopCycleLiveWatcher()
		cycleCancel()
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("reading stdin: %w", err)
	}

	fmt.Println("\nEnd of input. Ending session.")
	return saveMetaSession(cfg, adapterNames, agentNames, turns)
}

// RunOnceCycle processes a single prompt through the cycle state machine.
// Used for one-shot --cycle mode.
func RunOnceCycle(ctx context.Context, cfg Config, prompt string, turnNum int) (*cycle.Result, error) {
	cycleCfg := buildCycleConfig(cfg, prompt, turnNum, nil, nil, nil)
	return cycle.Run(ctx, cycleCfg)
}

// RunResumeCycle resumes a previously persisted cycle in the configured run directory.
func RunResumeCycle(ctx context.Context, cfg Config, turnNum int) (*cycle.Result, error) {
	cycleCfg := buildCycleConfig(cfg, "", turnNum, nil, nil, nil)
	return cycle.RunResume(ctx, cycleCfg)
}

func buildCycleConfig(
	cfg Config,
	prompt string,
	turnNum int,
	broker *cycle.ApprovalBroker,
	clarifier *cycle.ClarificationBroker,
	statusProvider *cycle.StatusProvider,
) cycle.Config {
	return cycle.Config{
		Prompt:       prompt,
		Adapters:     cfg.Adapters,
		Approval:     cfg.Approval,
		Agents:       cfg.Agents,
		Sandbox:      cfg.Sandbox,
		Worktree:     cfg.Worktree,
		Timeout:      cfg.Timeout,
		Store:        cfg.Store,
		Logger:       cfg.Logger,
		DefaultAgent: cfg.DefaultAgent,
		TurnNum:      turnNum,
		Guards:       cycle.DefaultGuards(),
		Broker:       broker,
		Clarifier:    clarifier,
		Status:       statusProvider,
	}
}

// RunOnce processes a single prompt through the router and dispatches to the
// appropriate engine. Used by both the REPL (per-turn) and one-shot mode.
func RunOnce(ctx context.Context, cfg Config, prompt string, history []Turn, turnNum int) (*Turn, error) {
	route := router.Classify(prompt, router.Config{DefaultAgent: cfg.DefaultAgent})
	route = adjustRouteForAvailability(route, cfg.DefaultAgent, adapterNames(cfg.Adapters), agentNames(cfg.Agents))
	return dispatch(ctx, cfg, prompt, history, turnNum, route)
}

// runOnceForced bypasses the router and forces a specific intent.
func runOnceForced(ctx context.Context, cfg Config, prompt string, history []Turn, turnNum int, intent router.Intent, agentName string, explicitAgent bool) (*Turn, error) {
	route := router.Result{
		Intent: intent,
		Agent:  agentName,
		Reason: "forced via slash command",
	}
	if intent == router.IntentDelegate {
		if agentName == "" {
			route.Agent = cfg.DefaultAgent
		}
		// Preserve explicit user-chosen agents even if unavailable so the error is clear.
		// Only auto-reselect when the target was implicit (default/fallback behavior).
		if !explicitAgent {
			if selected, changed := pickAvailableAgent(route.Agent, cfg.DefaultAgent, agentNames(cfg.Agents)); selected != "" {
				route.Agent = selected
				if changed {
					route.Reason += fmt.Sprintf("; selected available agent %q", selected)
				}
			}
		}
		if explicitAgent && route.Agent != "" {
			route.Reason += fmt.Sprintf("; explicit agent %q", route.Agent)
		}
	}
	return dispatch(ctx, cfg, prompt, history, turnNum, route)
}

func dispatch(ctx context.Context, cfg Config, prompt string, history []Turn, turnNum int, route router.Result) (*Turn, error) {
	turn := &Turn{
		Prompt: prompt,
		Route:  route,
	}

	switch route.Intent {
	case router.IntentBrainstorm:
		printRoute("brainstorm", route, prompt)
		result, err := runBrainstorm(ctx, cfg, prompt, history, turnNum)
		if err != nil {
			return nil, err
		}
		turn.Brainstorm = result
		return turn, nil

	case router.IntentDelegate:
		printRoute("delegate", route, prompt)
		result, err := runDelegate(ctx, cfg, prompt, route.Agent, turnNum)
		if err != nil {
			return nil, err
		}
		turn.Delegate = result
		return turn, nil

	default:
		return nil, fmt.Errorf("unknown intent: %s", route.Intent)
	}
}

// parseSlashCommand splits input into command and argument.
// Returns ("", input) for non-slash input.
func parseSlashCommand(input string) (cmd, arg string) {
	if !strings.HasPrefix(input, "/") {
		return "", input
	}

	parts := strings.SplitN(input[1:], " ", 2)
	cmd = strings.ToLower(parts[0])
	if len(parts) > 1 {
		arg = strings.TrimSpace(parts[1])
	}
	return cmd, arg
}

func parseResumeTurnArg(arg string) (int, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return 0, nil // latest cycle turn
	}
	n, err := strconv.Atoi(arg)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("resume turn must be a positive integer")
	}
	return n, nil
}

func parseClarifyArg(arg string) (ticketID, answer string, err error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return "", "", fmt.Errorf("clarification answer is required")
	}

	parts := strings.SplitN(arg, " ", 2)
	first := strings.TrimSpace(parts[0])
	if strings.HasPrefix(first, "cq-") {
		if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
			return "", "", fmt.Errorf("clarification answer is required")
		}
		return first, strings.TrimSpace(parts[1]), nil
	}

	return "", arg, nil
}

// parseDelegateArg parses "/delegate [agent] <prompt>".
// If the first word is any known agent name (installed or not), it is treated as an explicit agent target.
// Otherwise the default agent is used and the full arg is treated as the prompt.
func parseDelegateArg(arg string, cfg Config) (agentName, prompt string, explicitAgent bool) {
	parts := strings.SplitN(arg, " ", 2)
	firstName := parts[0]

	if _, ok := agent.Registry[firstName]; ok {
		if len(parts) > 1 {
			return firstName, strings.TrimSpace(parts[1]), true
		}
		return firstName, "", true
	}

	// First word is not a known agent — treat entire arg as prompt.
	return cfg.DefaultAgent, arg, false
}

func runBrainstorm(ctx context.Context, cfg Config, prompt string, history []Turn, turnNum int) (*BrainstormResult, error) {
	if len(cfg.Adapters) == 0 {
		return nil, fmt.Errorf("no adapters available for brainstorm")
	}

	orchHistory := toOrchestratorHistory(history)
	allRounds, err := orchestrator.Run(ctx, orchestrator.Config{
		Prompt:   prompt,
		Adapters: cfg.Adapters,
		Timeout:  cfg.Timeout,
		Approval: cfg.Approval,
		Store:    cfg.Store,
		History:  orchHistory,
		TurnNum:  turnNum,
		Logger:   cfg.Logger,
	})
	if err != nil {
		return nil, err
	}

	return &BrainstormResult{Rounds: allRounds}, nil
}

func runDelegate(ctx context.Context, cfg Config, prompt string, agentName string, turnNum int) (*DelegateResult, error) {
	var a agent.Agent
	for _, ag := range cfg.Agents {
		if ag.Name() == agentName {
			a = ag
			break
		}
	}
	if a == nil {
		return nil, fmt.Errorf("agent %q not found in available agents", agentName)
	}

	resolvedModel := agent.ResolveModel(a.Name(), a.DefaultModel())

	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get working directory: %w", err)
	}
	runCwd := cwd
	if turnNum < 1 {
		turnNum = 1
	}

	var wsInfo store.DelegateWorkspace
	if cfg.Worktree {
		if err := preflight.CheckWorktreePrereqs(ctx, cwd); err != nil {
			return nil, err
		}

		taskID := fmt.Sprintf("meta-t%d-%d", turnNum, time.Now().UnixNano())
		if cfg.Store != nil && cfg.Store.RunDir != "" {
			taskID = fmt.Sprintf("%s-t%d-%d", filepath.Base(cfg.Store.RunDir), turnNum, time.Now().UnixNano())
		}

		info, err := workspace.Prepare(ctx, cwd, taskID, a.Name())
		if err != nil {
			return nil, fmt.Errorf("failed to prepare worktree: %w", err)
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
		if cfg.Store != nil {
			if err := cfg.Store.SaveMetaTurnDelegateWorkspace(turnNum, wsInfo); err != nil {
				fmt.Printf("[warn] failed to save workspace metadata: %v\n", err)
			}
		}
	}

	var cancel context.CancelFunc
	if cfg.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, cfg.Timeout)
		defer cancel()
	}

	var finalText strings.Builder
	eventCount := 0

	runErr := stream.Run(ctx, a, prompt, agent.StreamOpts{
		Model:   resolvedModel,
		Sandbox: cfg.Sandbox,
		Cwd:     runCwd,
	}, stream.Callbacks{
		OnEvent: func(ev agent.Event) {
			eventCount++
			display.PrintEvent(ev)
			if cfg.Store != nil {
				if err := cfg.Store.SaveMetaTurnDelegateEvent(turnNum, ev); err != nil {
					fmt.Printf("[warn] failed to save event: %v\n", err)
				}
			}
			if ev.Type == agent.EventText {
				if s, ok := ev.Data.(string); ok {
					finalText.WriteString(s)
				}
			}
		},
		OnRawLine: func(line []byte) {
			if cfg.Store != nil {
				if err := cfg.Store.SaveMetaTurnDelegateRawLine(turnNum, line); err != nil {
					fmt.Printf("[warn] failed to save raw line: %v\n", err)
				}
			}
		},
		OnStderrLine: func(line []byte) {
			fmt.Printf("[%s][stderr] %s\n", a.Name(), string(line))
			if cfg.Store != nil {
				if err := cfg.Store.SaveMetaTurnDelegateStderrLine(turnNum, line); err != nil {
					fmt.Printf("[warn] failed to save stderr: %v\n", err)
				}
			}
		},
		OnParseError: func(line []byte, err error) {
			fmt.Printf("[%s][parse-error] %v\n", a.Name(), err)
		},
	})

	if wsInfo.Enabled {
		inspectCtx, inspectCancel := context.WithTimeout(context.Background(), 5*time.Second)
		head, commits, err := workspace.Inspect(inspectCtx, wsInfo.WorktreePath, wsInfo.BaseCommit)
		inspectCancel()
		if err != nil {
			fmt.Printf("[warn] failed to inspect worktree commits: %v\n", err)
		} else {
			wsInfo.HeadCommit = head
			wsInfo.Commits = make([]store.DelegateCommit, 0, len(commits))
			for _, c := range commits {
				wsInfo.Commits = append(wsInfo.Commits, store.DelegateCommit{
					SHA:     c.SHA,
					Subject: c.Subject,
				})
			}
			if cfg.Store != nil {
				if err := cfg.Store.SaveMetaTurnDelegateWorkspace(turnNum, wsInfo); err != nil {
					fmt.Printf("[warn] failed to save workspace metadata: %v\n", err)
				}
			}
		}
	}

	if runErr != nil {
		return nil, runErr
	}

	return &DelegateResult{
		Agent:      agentName,
		EventCount: eventCount,
		FinalText:  finalText.String(),
	}, nil
}

func printRoute(kind string, route router.Result, prompt string) {
	label := kind
	if kind == "delegate" && route.Agent != "" {
		label = fmt.Sprintf("%s → %s", kind, route.Agent)
	}
	if route.Reason != "" {
		fmt.Printf("[route] %s (%s) — %q\n", label, route.Reason, truncate(prompt, 60))
		return
	}
	fmt.Printf("[route] %s — %q\n", label, truncate(prompt, 60))
}

func adjustRouteForAvailability(route router.Result, defaultAgent string, adapters, agents []string) router.Result {
	switch route.Intent {
	case router.IntentBrainstorm:
		if len(adapters) > 0 {
			return route
		}
		if len(agents) == 0 {
			return route
		}
		agentName, changed := pickAvailableAgent(route.Agent, defaultAgent, agents)
		route.Intent = router.IntentDelegate
		route.Agent = agentName
		if route.Reason == "" {
			route.Reason = "fallback to delegate (no brainstorm adapters ready)"
		} else {
			route.Reason += "; fallback to delegate (no brainstorm adapters ready)"
		}
		if changed {
			route.Reason += fmt.Sprintf("; selected available agent %q", agentName)
		}
		return route

	case router.IntentDelegate:
		if len(agents) > 0 {
			agentName, changed := pickAvailableAgent(route.Agent, defaultAgent, agents)
			route.Agent = agentName
			if changed {
				if route.Reason == "" {
					route.Reason = fmt.Sprintf("selected available agent %q", agentName)
				} else {
					route.Reason += fmt.Sprintf("; selected available agent %q", agentName)
				}
			}
			return route
		}
		if len(adapters) == 0 {
			return route
		}
		route.Intent = router.IntentBrainstorm
		route.Agent = ""
		if route.Reason == "" {
			route.Reason = "fallback to brainstorm (no delegate agents ready)"
		} else {
			route.Reason += "; fallback to brainstorm (no delegate agents ready)"
		}
		return route
	}
	return route
}

func adapterNames(adapters []adapter.Adapter) []string {
	names := make([]string, 0, len(adapters))
	for _, a := range adapters {
		names = append(names, a.Name())
	}
	return names
}

func agentNames(agents []agent.Agent) []string {
	names := make([]string, 0, len(agents))
	for _, a := range agents {
		names = append(names, a.Name())
	}
	return names
}

func pickAvailableAgent(preferred, defaultAgent string, agents []string) (string, bool) {
	if len(agents) == 0 {
		return "", false
	}
	if preferred != "" && containsName(agents, preferred) {
		return preferred, false
	}
	if defaultAgent != "" && containsName(agents, defaultAgent) {
		return defaultAgent, preferred != defaultAgent
	}
	return agents[0], preferred != agents[0]
}

func containsName(names []string, target string) bool {
	for _, n := range names {
		if n == target {
			return true
		}
	}
	return false
}

// toOrchestratorHistory converts meta session turns to orchestrator turns so
// brainstorm models can see context from prior turns (including delegate output).
func toOrchestratorHistory(turns []Turn) []orchestrator.Turn {
	out := make([]orchestrator.Turn, 0, len(turns))
	for _, t := range turns {
		ot := orchestrator.Turn{Prompt: t.Prompt}

		if t.Brainstorm != nil {
			ot.Responses = t.Brainstorm.Rounds
		} else if t.Delegate != nil {
			// Wrap delegate output as a synthetic single-round response
			// so the orchestrator can include it in prompt context.
			ot.Responses = [][]adapter.Response{{
				{
					Model:   t.Delegate.Agent,
					Content: t.Delegate.FinalText,
				},
			}}
		} else if t.Cycle != nil {
			content := t.Cycle.Recommendation
			if content == "" && t.Cycle.Error != "" {
				content = "[cycle error] " + t.Cycle.Error
			}
			if content != "" {
				ot.Responses = [][]adapter.Response{{
					{
						Model:   "cycle",
						Content: content,
					},
				}}
			}
		}

		out = append(out, ot)
	}
	return out
}

func saveMetaSession(cfg Config, adapterNames, agentNames []string, turns []Turn) error {
	if len(turns) == 0 {
		return nil
	}

	storeTurns := make([]store.MetaSessionTurn, len(turns))
	for i, t := range turns {
		st := store.MetaSessionTurn{
			Prompt: t.Prompt,
		}
		if t.Brainstorm != nil {
			st.Engine = "brainstorm"
			st.Responses = t.Brainstorm.Rounds
		} else if t.Delegate != nil {
			st.Engine = "delegate"
			st.Agent = t.Delegate.Agent
			st.FinalText = t.Delegate.FinalText
		} else if t.Cycle != nil {
			st.Engine = "cycle"
			st.CycleID = t.Cycle.CycleID
			st.CycleState = t.Cycle.FinalState
			st.FinalText = t.Cycle.Recommendation
			st.Error = t.Cycle.Error
		}
		storeTurns[i] = st
	}

	allModels := append(adapterNames, agentNames...)
	meta := store.RunMeta{
		Prompt:    turns[0].Prompt,
		Models:    allModels,
		Timeout:   cfg.Timeout.String(),
		Timestamp: time.Now().Format(time.RFC3339),
		Mode:      "meta",
	}

	if err := cfg.Store.SaveMetaSessionSummary(meta, storeTurns); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to save meta session summary: %v\n", err)
	}

	fmt.Printf("Session artifacts saved to: %s\n", cfg.Store.RunDir)
	return nil
}

func handleCycleResult(result *cycle.Result, err error) *CycleResult {
	if err != nil {
		fmt.Fprintf(os.Stderr, "[cycle] Error: %v\n", err)
		return &CycleResult{
			FinalState: "ABORTED",
			Error:      err.Error(),
		}
	}
	if result == nil {
		fmt.Println("[cycle] Cycle completed (no result).")
		return nil
	}
	fmt.Printf("\n[cycle] Completed: %s (state: %s, elapsed: %.1fs)\n",
		result.CycleID, result.FinalState, result.Elapsed.Seconds())
	recommendation := ""
	if result.Decision != nil && result.Decision.Recommendation != "" {
		recommendation = result.Decision.Recommendation
		fmt.Println(recommendation)
	}
	return &CycleResult{
		CycleID:        result.CycleID,
		FinalState:     string(result.FinalState),
		Recommendation: recommendation,
		Elapsed:        result.Elapsed,
	}
}

func printHelp() {
	fmt.Println("Meta session commands:")
	fmt.Println("  /brainstorm <prompt>       Force multi-agent brainstorm")
	fmt.Println("  /delegate [agent] <prompt> Force single-agent delegate")
	fmt.Println("  /history                   Show turn summaries")
	fmt.Println("  /help                      Show this help")
	fmt.Println("  /quit, /exit               End session")
	fmt.Println()
	fmt.Println("Or just type a prompt — it will be auto-routed.")
	fmt.Println()
}

func printCycleHelp() {
	fmt.Println("Cycle commands:")
	fmt.Println("  /status                    Show current cycle state")
	fmt.Println("  /board                     Show current transcript-backed phase board")
	fmt.Println("  /timeline [count]          Show recent transcript-backed collaboration events")
	fmt.Println("  /live [mode]               Set live updates: off|compact|verbose")
	fmt.Println("  /resume [turn]             Resume a prior cycle from this run directory")
	fmt.Println("  /clarify [ticket] <answer> Provide clarification response for cycle")
	fmt.Println("  /approve [ticket-id]       Approve pending approval")
	fmt.Println("  /deny [ticket-id]          Deny pending approval")
	fmt.Println("  /stop                      Cancel running cycle")
	fmt.Println()
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

func truncateDesc(s string, max int) string {
	return truncate(s, max)
}

// printCycleStatus renders a rich /status display from the StatusProvider and brokers.
func printCycleStatus(sp *cycle.StatusProvider, broker *cycle.ApprovalBroker, clarifier *cycle.ClarificationBroker) {
	var snap *cycle.CycleStatus
	if sp != nil {
		snap = sp.Snapshot()
	}

	separator := strings.Repeat("-", 50)
	fmt.Println(separator)

	if snap == nil {
		fmt.Println("  Cycle is running (no status available yet).")
		if broker != nil {
			printPendingApprovals(broker)
		}
		if clarifier != nil {
			printPendingClarifications(clarifier)
		}
		fmt.Println(separator)
		return
	}

	fmt.Printf("  Cycle: %s\n", snap.CycleID)
	fmt.Printf("  State: %s (phase: %s)\n", snap.State, snap.Phase)
	fmt.Printf("  Elapsed: %.1fs\n", snap.Elapsed.Seconds())

	if snap.TaskType != "" {
		fmt.Printf("  Task type: %s\n", snap.TaskType)
	}

	if snap.TotalSubtasks > 0 {
		fmt.Printf("  Subtasks: %d/%d completed\n", snap.CompletedSubtasks, snap.TotalSubtasks)
		if snap.CurrentSubtask != "" {
			fmt.Printf("  Active: %s\n", snap.CurrentSubtask)
		}
		for _, st := range snap.Subtasks {
			status := "pending"
			if st.Completed {
				status = "done"
			} else if st.ID == snap.CurrentSubtask {
				status = "active"
			} else if st.Error != "" {
				status = "error"
			}
			fmt.Printf("    %s [%s] %s (agent: %s)\n", st.ID, status, truncateDesc(st.Description, 50), st.Agent)
		}
	}

	fmt.Printf("  Revisions: %d/%d\n", snap.RevisionCount, snap.MaxRevisions)
	fmt.Printf("  Transcript entries: %d\n", snap.TranscriptLen)
	if snap.LastTranscript.LastSummary != "" {
		actor := snap.LastTranscript.LastAgent
		if actor == "" {
			actor = "coordinator"
		}
		fmt.Printf("  Last activity: [%s][%s] %s\n", snap.LastTranscript.LastKind, actor, snap.LastTranscript.LastSummary)
	}
	if rs := snap.CurrentReview; rs != nil {
		fmt.Printf("  Review pass: %s #%d (%d findings: %d blocker, %d warn, %d info)\n",
			rs.Phase, rs.Pass, rs.Total, rs.Blockers, rs.Warns, rs.Infos)
	}
	if board := snap.CurrentBoard; board != nil && len(board.Items) > 0 {
		fmt.Printf("  Phase board (%s #%d):\n", board.Phase, board.Pass)
		for _, item := range board.Items {
			fmt.Printf("    [%s][%s][%s] %s\n",
				item.Role,
				item.Agent,
				item.Kind,
				truncateDesc(item.Summary, 90),
			)
		}
	}

	if snap.LastError != "" {
		fmt.Printf("  Last error: %s\n", snap.LastError)
	}

	if broker != nil {
		printPendingApprovals(broker)
	}
	if clarifier != nil {
		printPendingClarifications(clarifier)
	}

	fmt.Println(separator)
}

func printCycleBoard(sp *cycle.StatusProvider) {
	if sp == nil {
		fmt.Println("No cycle status provider.")
		return
	}
	snap := sp.Snapshot()
	if snap == nil {
		fmt.Println("Cycle is running (no status available yet).")
		return
	}
	board := snap.CurrentBoard
	if board == nil || len(board.Items) == 0 {
		fmt.Printf("No phase board entries yet (phase: %s#%d).\n", snap.Phase, snap.Pass)
		return
	}

	fmt.Printf("Phase board (%s #%d):\n", board.Phase, board.Pass)
	for _, item := range board.Items {
		fmt.Printf("  [%s][%s][%s] %s\n",
			item.Role,
			item.Agent,
			item.Kind,
			truncateDesc(item.Summary, 120),
		)
	}
}

func printCycleTimeline(sp *cycle.StatusProvider, limit int) {
	if sp == nil {
		fmt.Println("No cycle status provider.")
		return
	}
	if limit <= 0 {
		limit = 10
	}
	snap := sp.Snapshot()
	if snap == nil {
		fmt.Println("Cycle is running (no status available yet).")
		return
	}
	if len(snap.RecentTimeline) == 0 {
		fmt.Println("No recent transcript events yet.")
		return
	}

	events := snap.RecentTimeline
	capN := snap.RecentTimelineCap
	if capN <= 0 {
		capN = len(events)
	}
	if len(events) > limit {
		events = events[len(events)-limit:]
	}

	fmt.Printf("Recent timeline (%d shown):\n", len(events))
	if limit > capN && len(snap.RecentTimeline) >= capN && capN > 0 {
		fmt.Printf("  (status snapshot currently retains up to %d recent events; requested %d)\n", capN, limit)
	}
	for _, ev := range events {
		phase := ev.Phase
		if phase == "" {
			phase = string(snap.State)
		}
		fmt.Printf("  #%d %s#%d [%s][%s][%s] %s\n",
			ev.ID,
			phase,
			ev.Pass,
			ev.Role,
			ev.Agent,
			ev.Kind,
			truncateDesc(ev.Summary, 120),
		)
	}
}

// printPendingApprovals renders any pending approval tickets.
func printPendingApprovals(broker *cycle.ApprovalBroker) {
	pending := broker.Pending()
	if len(pending) == 0 {
		return
	}
	fmt.Println("  Pending approvals:")
	for _, pa := range pending {
		fmt.Printf("    [%s] %s (scope: %s)\n", pa.TicketID, pa.Reason, pa.Scope)
		fmt.Printf("    Use /approve %s or /deny %s\n", pa.TicketID, pa.TicketID)
	}
}

func printPendingClarifications(clarifier *cycle.ClarificationBroker) {
	pending := clarifier.Pending()
	if len(pending) == 0 {
		return
	}
	fmt.Println("  Pending clarifications:")
	for _, pc := range pending {
		fmt.Printf("    [%s] %s\n", pc.TicketID, pc.Question)
		fmt.Printf("    Use /clarify %s <answer>\n", pc.TicketID)
	}
}
