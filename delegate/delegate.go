package delegate

import (
	"context"
	"fmt"
	"time"

	"github.com/dre4success/tripartite/agent"
	"github.com/dre4success/tripartite/display"
	"github.com/dre4success/tripartite/store"
	"github.com/dre4success/tripartite/stream"
	"github.com/dre4success/tripartite/workspace"
)

// Config controls one delegate-mode execution.
type Config struct {
	Agent     agent.Agent
	Prompt    string
	Model     string
	Sandbox   string
	Timeout   time.Duration
	SessionID string
	Cwd       string
	Store     *store.Store
	Worktree  store.DelegateWorkspace
}

// Run executes one delegated task and persists stream artifacts.
func Run(ctx context.Context, cfg Config) error {
	if cfg.Agent == nil {
		return fmt.Errorf("delegate agent is required")
	}
	if cfg.Store == nil {
		return fmt.Errorf("delegate store is required")
	}

	meta := store.RunMeta{
		Prompt:    cfg.Prompt,
		Models:    []string{cfg.Agent.Name()},
		Timeout:   cfg.Timeout.String(),
		Timestamp: time.Now().Format(time.RFC3339),
		Mode:      "delegate",
	}
	if err := cfg.Store.SaveInput(meta); err != nil {
		return fmt.Errorf("save input: %w", err)
	}

	if cfg.Worktree.Enabled {
		if err := cfg.Store.SaveDelegateWorkspace(cfg.Worktree); err != nil {
			return fmt.Errorf("save workspace: %w", err)
		}
	}

	resolvedModel := cfg.Model
	if resolvedModel == "" {
		resolvedModel = cfg.Agent.DefaultModel()
	}
	resolvedModel = agent.ResolveModel(cfg.Agent.Name(), resolvedModel)

	var cancel context.CancelFunc
	if cfg.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, cfg.Timeout)
		defer cancel()
	}

	start := time.Now()
	eventCount := 0
	runErr := stream.Run(ctx, cfg.Agent, cfg.Prompt, agent.StreamOpts{
		Model:     resolvedModel,
		Sandbox:   cfg.Sandbox,
		Cwd:       cfg.Cwd,
		SessionID: cfg.SessionID,
	}, stream.Callbacks{
		OnEvent: func(ev agent.Event) {
			eventCount++
			display.PrintEvent(ev)
			if err := cfg.Store.SaveDelegateEvent(ev); err != nil {
				fmt.Printf("[warn] failed to save normalized event: %v\n", err)
			}
		},
		OnRawLine: func(line []byte) {
			if err := cfg.Store.SaveDelegateRawLine(line); err != nil {
				fmt.Printf("[warn] failed to save raw line: %v\n", err)
			}
		},
		OnStderrLine: func(line []byte) {
			fmt.Printf("[%s][stderr] %s\n", cfg.Agent.Name(), string(line))
			if err := cfg.Store.SaveDelegateStderrLine(line); err != nil {
				fmt.Printf("[warn] failed to save stderr: %v\n", err)
			}
		},
		OnParseError: func(line []byte, err error) {
			fmt.Printf("[%s][parse-error] %v\n", cfg.Agent.Name(), err)
		},
	})
	duration := time.Since(start)

	if cfg.Worktree.Enabled {
		inspectCtx, inspectCancel := context.WithTimeout(context.Background(), 5*time.Second)
		head, commits, err := workspace.Inspect(inspectCtx, cfg.Worktree.WorktreePath, cfg.Worktree.BaseCommit)
		inspectCancel()
		if err != nil {
			fmt.Printf("[warn] failed to inspect worktree commits: %v\n", err)
		} else {
			cfg.Worktree.HeadCommit = head
			cfg.Worktree.Commits = make([]store.DelegateCommit, 0, len(commits))
			for _, c := range commits {
				cfg.Worktree.Commits = append(cfg.Worktree.Commits, store.DelegateCommit{
					SHA:     c.SHA,
					Subject: c.Subject,
				})
			}
			if err := cfg.Store.SaveDelegateWorkspace(cfg.Worktree); err != nil {
				fmt.Printf("[warn] failed to save workspace metadata: %v\n", err)
			}
		}
	}

	summary := store.DelegateSummary{
		Agent:      cfg.Agent.Name(),
		Model:      resolvedModel,
		Prompt:     cfg.Prompt,
		Sandbox:    cfg.Sandbox,
		Duration:   duration,
		EventCount: eventCount,
		Worktree:   cfg.Worktree,
	}
	if runErr != nil {
		summary.Error = runErr.Error()
	}
	if err := cfg.Store.SaveDelegateSummary(summary); err != nil {
		fmt.Printf("[warn] failed to save delegate summary: %v\n", err)
	}

	return runErr
}
