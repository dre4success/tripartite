package cycle

import (
	"time"

	"github.com/dre4success/tripartite/adapter"
	"github.com/dre4success/tripartite/agent"
	"github.com/dre4success/tripartite/logger"
	"github.com/dre4success/tripartite/store"
)

// Config holds the configuration for a single cycle run.
type Config struct {
	Prompt       string
	Adapters     []adapter.Adapter
	Agents       []agent.Agent
	Approval     adapter.ApprovalLevel
	Sandbox      string
	Worktree     bool
	Timeout      time.Duration
	Store        *store.Store
	Logger       *logger.Logger
	DefaultAgent string
	TurnNum      int
	Guards       Guards
	Broker       *ApprovalBroker
}

// Guards holds safety limits for cycle execution.
type Guards struct {
	MaxTotalRuntime   time.Duration
	MaxRevisionLoops  int
	MaxRetriesPerTask int
	QuorumMin         int
	SkipPlanReview    bool
	SkipOutputReview  bool
}

// DefaultGuards returns sensible defaults for safety guards.
func DefaultGuards() Guards {
	return Guards{
		MaxTotalRuntime:   30 * time.Minute,
		MaxRevisionLoops:  3,
		MaxRetriesPerTask: 2,
		QuorumMin:         2,
	}
}
