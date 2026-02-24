package meta

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dre4success/tripartite/adapter"
	"github.com/dre4success/tripartite/agent"
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

	fmt.Println("Tripartite meta session")
	fmt.Printf("Adapters: %s\n", strings.Join(adapterNames, ", "))
	fmt.Printf("Agents: %s\n", strings.Join(agentNames, ", "))
	fmt.Println("Commands: /brainstorm, /delegate, /history, /help, /quit")
	fmt.Println()

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			break
		}

		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		cmd, arg := parseSlashCommand(input)

		switch cmd {
		case "quit", "exit":
			fmt.Println("Ending session.")
			return saveMetaSession(cfg, adapterNames, agentNames, turns)

		case "history":
			fmt.Printf("Session has %d turn(s).\n", len(turns))
			for i, t := range turns {
				engine := "brainstorm"
				if t.Delegate != nil {
					engine = "delegate → " + t.Delegate.Agent
				}
				fmt.Printf("  Turn %d [%s]: %s\n", i+1, engine, truncate(t.Prompt, 70))
			}
			fmt.Println()
			continue

		case "help":
			printHelp()
			continue

		case "brainstorm":
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
			// Normal prompt — route automatically.
			turn, err := RunOnce(ctx, cfg, input, turns, len(turns)+1)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				fmt.Println("(You can try another prompt or /quit)")
				continue
			}
			turns = append(turns, *turn)
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("reading stdin: %w", err)
	}

	fmt.Println("\nEnd of input. Ending session.")
	return saveMetaSession(cfg, adapterNames, agentNames, turns)
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

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}
