package cycle

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

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
	start := time.Now()

	// Apply runtime timeout guard.
	maxRuntime := cfg.Guards.MaxTotalRuntime
	if maxRuntime == 0 {
		maxRuntime = DefaultGuards().MaxTotalRuntime
	}
	ctx, cancel := context.WithTimeout(ctx, maxRuntime)
	defer cancel()

	for {
		// Check context (covers both explicit cancel and MaxTotalRuntime).
		if err := ctx.Err(); err != nil {
			cc.transcript.Append(KindError, "coordinator", cc.state, "cycle timed out or cancelled")
			cc.state = StateAborted
			break
		}

		// Terminal states.
		if cc.state == StateDone || cc.state == StateAborted {
			break
		}

		// Checkpoint.
		if cfg.Store != nil {
			checkpoint(cfg.Store, cfg.TurnNum, cc, time.Since(start))
		}

		// Handle current state.
		if err := cc.handle(ctx); err != nil {
			cc.lastError = err
			cc.transcript.Append(KindError, "coordinator", cc.state, err.Error())
			// For handler errors in non-EXECUTE states, abort.
			if cc.state != StateExecute {
				cc.state = StateAborted
				break
			}
		}

		// Deterministic transition.
		prev := cc.state
		cc.state = transition(cc.state, cc)
		cc.transcript.Append(KindStateChange, "coordinator", cc.state, StateChangePayload{
			From: prev,
			To:   cc.state,
		})
	}

	// Final checkpoint.
	elapsed := time.Since(start)
	cc.finalizeWorktree()
	if cfg.Store != nil {
		checkpoint(cfg.Store, cfg.TurnNum, cc, elapsed)
		saveFinalTranscript(cfg.Store, cfg.TurnNum, cc)
	}

	return &Result{
		CycleID:    cc.cycleID,
		FinalState: cc.state,
		Transcript: cc.transcript,
		Plan:       cc.plan,
		Decision:   cc.decision,
		Elapsed:    elapsed,
	}, nil
}
