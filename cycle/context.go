package cycle

import (
	"context"
	"strings"
	"time"

	"github.com/dre4success/tripartite/store"
)

// cycleContext holds the mutable state for a single cycle run.
type cycleContext struct {
	cycleID               string
	state                 State
	cfg                   Config
	transcript            *Transcript
	intent                *IntentPayload
	plan                  *PlanPayload
	decision              *DecisionPayload
	decisionApproveAction string
	decisionDenyAction    string
	revisionCount         int
	retryCount            map[string]int // subtask ID → retry count
	resumeState           State          // state to resume after AWAIT_APPROVAL
	lastError             error
	lastApproval          *PendingApproval
	worktreeInfo          store.DelegateWorkspace
	startedAt             time.Time
	// Phase/pass tracking for transcript entries.
	currentPhase          string
	currentSubtask        string
	outputReviewPassCount int
	planReviewPassCount   int
	// Brainstorm runs are phase-scoped so persisted artifacts do not overwrite.
	planBrainstormRuns         int
	planReviewBrainstormRuns   int
	outputReviewBrainstormRuns int
}

func newCycleContext(cfg Config) *cycleContext {
	return &cycleContext{
		cycleID:    generateCycleID(),
		state:      StateInit,
		cfg:        cfg,
		transcript: NewTranscript(),
		retryCount: make(map[string]int),
	}
}

func phaseName(state State) string {
	return strings.ToLower(string(state))
}

func (cc *cycleContext) passForState(state State) int {
	switch state {
	case StateOutputReview:
		return cc.outputReviewPassCount
	case StatePlanReview:
		return cc.planReviewPassCount
	default:
		return 0
	}
}

// currentPass returns the pass count for the current state.
func (cc *cycleContext) currentPass() int {
	return cc.passForState(cc.state)
}

func (cc *cycleContext) pushStatus() {
	if cc.startedAt.IsZero() {
		return
	}
	publishStatus(cc.cfg, cc, cc.startedAt)
}

// handle dispatches to the appropriate handler for the current state.
func (cc *cycleContext) handle(ctx context.Context) error {
	// Track phase and increment pass counters for review states.
	cc.currentPhase = phaseName(cc.state)
	switch cc.state {
	case StateOutputReview:
		cc.outputReviewPassCount++
	case StatePlanReview:
		cc.planReviewPassCount++
	}
	cc.pushStatus()

	switch cc.state {
	case StateInit:
		return cc.handleInit(ctx)
	case StateIntake:
		return cc.handleIntake(ctx)
	case StatePlan:
		return cc.handlePlan(ctx)
	case StatePlanReview:
		return cc.handlePlanReview(ctx)
	case StateExecute:
		return cc.handleExecute(ctx)
	case StateOutputReview:
		return cc.handleOutputReview(ctx)
	case StateRevise:
		return cc.handleRevise(ctx)
	case StateDecisionGate:
		return cc.handleDecisionGate(ctx)
	case StateAwaitApproval:
		return cc.handleAwaitApproval(ctx)
	case StateAwaitClarification:
		return cc.handleAwaitClarification(ctx)
	case StateRecovering:
		return cc.handleRecovering(ctx)
	default:
		return nil
	}
}

// --- Condition checkers used by transition() ---

// needsApproval returns true if the plan requires write permissions.
func (cc *cycleContext) needsApproval() bool {
	if cc.plan == nil {
		return false
	}
	return cc.plan.Permissions == "edit" || cc.plan.Permissions == "full"
}

// executionFailed returns true if the last operation produced an error.
func (cc *cycleContext) executionFailed() bool {
	return cc.lastError != nil
}

// hasBlockers returns true if any review findings have blocker severity.
func (cc *cycleContext) hasBlockers() bool {
	findings := cc.latestReviewFindings(StateOutputReview)
	for _, f := range findings {
		if f.Severity == SeverityBlocker {
			return true
		}
	}
	return false
}

// revisionBudget returns remaining revision loops.
func (cc *cycleContext) revisionBudget() int {
	max := cc.cfg.Guards.MaxRevisionLoops
	if max == 0 {
		max = DefaultGuards().MaxRevisionLoops
	}
	return max - cc.revisionCount
}

// canRetry returns true if any failed subtask still has retry budget.
func (cc *cycleContext) canRetry() bool {
	maxRetries := cc.cfg.Guards.MaxRetriesPerTask
	if maxRetries == 0 {
		maxRetries = DefaultGuards().MaxRetriesPerTask
	}
	for _, count := range cc.retryCount {
		if count < maxRetries {
			return true
		}
	}
	return false
}

// requiresOperatorDecision returns true for code_change or hybrid tasks.
func (cc *cycleContext) requiresOperatorDecision() bool {
	if cc.intent == nil {
		return false
	}
	return cc.intent.TaskType == TaskCodeChange || cc.intent.TaskType == TaskHybrid
}

// approvalDenied returns true if the last approval was denied.
func (cc *cycleContext) approvalDenied() bool {
	if cc.lastApproval == nil {
		return false
	}
	if cc.lastApproval.Approved {
		return false
	}
	// Denying the final decision gate means "keep proposal" rather than aborting
	// the cycle. Permission approvals still abort on deny.
	if cc.lastApproval.Scope == decisionGateApprovalScope {
		return false
	}
	return true
}

func (cc *cycleContext) isDecisionApproval() bool {
	return cc.resumeState == StateDone && cc.decision != nil
}

// latestReviewFindings returns review findings for the most recent visit to the given state.
// It uses the latest state transition into that state as a boundary, so stale findings from
// earlier review passes do not affect current transitions.
func (cc *cycleContext) latestReviewFindings(target State) []ReviewFindingPayload {
	entries := cc.transcript.Entries()
	start := 0
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if e.Kind != KindStateChange {
			continue
		}
		sc, ok := e.Payload.(StateChangePayload)
		if !ok {
			continue
		}
		if sc.To == target {
			start = i + 1
			break
		}
	}

	var out []ReviewFindingPayload
	for _, e := range entries[start:] {
		if e.Kind != KindReviewFinding || e.State != target {
			continue
		}
		if f, ok := e.Payload.(ReviewFindingPayload); ok {
			out = append(out, f)
		}
	}
	return out
}

// latestArtifact returns the most recent artifact for a subtask/revision, if any.
func (cc *cycleContext) latestArtifact(subtaskID string, revision int) *ArtifactPayload {
	entries := cc.transcript.Entries()
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if e.Kind != KindArtifact {
			continue
		}
		a, ok := e.Payload.(ArtifactPayload)
		if !ok {
			continue
		}
		if a.SubtaskID == subtaskID && a.Revision == revision {
			aa := a
			return &aa
		}
	}
	return nil
}
