package cycle

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

const statusRecentTimelineCap = 50

// State represents a cycle state machine state.
type State string

const (
	StateInit               State = "INIT"
	StateIntake             State = "INTAKE"
	StatePlan               State = "PLAN"
	StatePlanReview         State = "PLAN_REVIEW"
	StateExecute            State = "EXECUTE"
	StateOutputReview       State = "OUTPUT_REVIEW"
	StateRevise             State = "REVISE"
	StateDecisionGate       State = "DECISION_GATE"
	StateDone               State = "DONE"
	StateAwaitApproval      State = "AWAIT_APPROVAL"
	StateAwaitClarification State = "AWAIT_CLARIFICATION"
	StateRecovering         State = "RECOVERING"
	StateAborted            State = "ABORTED"
)

// Result holds the outcome of a completed cycle.
type Result struct {
	CycleID    string
	FinalState State
	Transcript *Transcript
	Plan       *PlanPayload
	Decision   *DecisionPayload
	Elapsed    time.Duration
}

// transition returns the next state given the current state and cycle context.
// This is a pure deterministic function — no model calls.
func transition(state State, cc *cycleContext) State {
	switch state {
	case StateInit:
		return StateIntake

	case StateIntake:
		return StatePlan

	case StatePlan:
		if cc.cfg.Guards.SkipPlanReview {
			return StateExecute
		}
		return StatePlanReview

	case StatePlanReview:
		if cc.cfg.Clarifier != nil && cc.needsClarification() {
			cc.resumeState = StatePlan
			return StateAwaitClarification
		}
		if cc.needsApproval() {
			cc.resumeState = StateExecute
			return StateAwaitApproval
		}
		return StateExecute

	case StateExecute:
		if cc.executionFailed() {
			return StateRecovering
		}
		if cc.cfg.Guards.SkipOutputReview {
			return StateDecisionGate
		}
		return StateOutputReview

	case StateOutputReview:
		if cc.hasBlockers() && cc.revisionBudget() > 0 {
			return StateRevise
		}
		return StateDecisionGate

	case StateRevise:
		return StateOutputReview

	case StateDecisionGate:
		if cc.requiresOperatorDecision() {
			cc.resumeState = StateDone
			return StateAwaitApproval
		}
		return StateDone

	case StateAwaitApproval:
		if cc.approvalDenied() {
			return StateAborted
		}
		return cc.resumeState

	case StateAwaitClarification:
		if cc.resumeState == "" {
			return StatePlan
		}
		return cc.resumeState

	case StateRecovering:
		if cc.canRetry() {
			return StateExecute
		}
		return StateAborted

	default:
		return StateAborted
	}
}

func generateCycleID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("cycle-%d", time.Now().UnixNano())
	}
	return "cycle-" + hex.EncodeToString(b[:])
}

// Run executes the full cycle state machine and returns the result.
// It should be called from a goroutine; the context can be cancelled
// to abort the cycle (e.g. via /stop).
func Run(ctx context.Context, cfg Config) (*Result, error) {
	cc := newCycleContext(cfg)
	cc.startedAt = time.Now()
	return runLoop(ctx, cc)
}

// publishStatus pushes a CycleStatus snapshot to the StatusProvider (if configured).
func publishStatus(cfg Config, cc *cycleContext, start time.Time) {
	if cfg.Status == nil {
		return
	}

	maxRevisions := cfg.Guards.MaxRevisionLoops
	if maxRevisions == 0 {
		maxRevisions = DefaultGuards().MaxRevisionLoops
	}

	pendingApprovals := 0
	if cfg.Broker != nil {
		pendingApprovals = len(cfg.Broker.Pending())
	}

	lastErr := ""
	if cc.lastError != nil {
		lastErr = cc.lastError.Error()
	}

	taskType := ""
	intent := ""
	var roles *RoleMap
	if cc.intent != nil {
		taskType = string(cc.intent.TaskType)
		intent = cc.intent.NormalizedGoal
		roles = &cc.intent.Roles
	}

	// Status snapshots should reflect the current machine state, even if they are emitted
	// at loop boundaries before handle() refreshes cc.currentPhase.
	statusPhase := phaseName(cc.state)
	statusPass := cc.passForState(cc.state)
	tx := cc.transcript.StatusSummary(statusPhase, statusPass)
	board := cc.transcript.PhaseBoardSummary(statusPhase, statusPass, roles)
	timeline := cc.transcript.RecentTimeline(statusRecentTimelineCap, roles)

	subtasks := buildSubtaskStatuses(cc)
	completed := 0
	for _, s := range subtasks {
		if s.Completed {
			completed++
		}
	}

	cfg.Status.Update(CycleStatus{
		CycleID:           cc.cycleID,
		State:             cc.state,
		Phase:             statusPhase,
		Pass:              statusPass,
		StartedAt:         start,
		Elapsed:           time.Since(start),
		CurrentSubtask:    cc.currentSubtask,
		TotalSubtasks:     len(subtasks),
		CompletedSubtasks: completed,
		Subtasks:          subtasks,
		RevisionCount:     cc.revisionCount,
		MaxRevisions:      maxRevisions,
		RetryCount:        cc.retryCount,
		PendingApprovals:  pendingApprovals,
		LastError:         lastErr,
		TaskType:          taskType,
		Intent:            intent,
		TranscriptLen:     cc.transcript.Len(),
		LastTranscript:    tx,
		CurrentReview:     tx.Review,
		CurrentBoard:      board,
		RecentTimeline:    timeline,
		RecentTimelineCap: statusRecentTimelineCap,
	})
}

func (cc *cycleContext) appendStateChange(fromState, toState State) {
	cc.transcript.Append(KindStateChange, "coordinator", fromState, phaseName(fromState), cc.passForState(fromState), StateChangePayload{
		From: fromState,
		To:   toState,
	})
}

// buildSubtaskStatuses iterates plan subtasks and checks for completed artifacts.
func buildSubtaskStatuses(cc *cycleContext) []SubtaskStatus {
	if cc.plan == nil {
		return nil
	}

	statuses := make([]SubtaskStatus, 0, len(cc.plan.Subtasks))
	for _, st := range cc.plan.Subtasks {
		ss := SubtaskStatus{
			ID:          st.ID,
			Description: st.Description,
			Agent:       st.Agent,
			Revision:    cc.revisionCount,
		}

		// Check all revisions for a successful artifact.
		for rev := cc.revisionCount; rev >= 0; rev-- {
			if a := cc.latestArtifact(st.ID, rev); a != nil {
				if a.Error == "" {
					ss.Completed = true
				} else {
					ss.Error = a.Error
				}
				ss.Revision = rev
				break
			}
		}

		statuses = append(statuses, ss)
	}
	return statuses
}
