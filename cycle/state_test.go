package cycle

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/dre4success/tripartite/adapter"
	"github.com/dre4success/tripartite/store"
)

func TestTransitionTable(t *testing.T) {
	tests := []struct {
		name     string
		state    State
		setup    func(*cycleContext) // modify cc before calling transition
		expected State
	}{
		// Happy path.
		{name: "INIT→INTAKE", state: StateInit, expected: StateIntake},
		{name: "INTAKE→PLAN", state: StateIntake, expected: StatePlan},
		{name: "PLAN→PLAN_REVIEW", state: StatePlan, expected: StatePlanReview},
		{name: "PLAN→EXECUTE_skip_review", state: StatePlan, setup: func(cc *cycleContext) {
			cc.cfg.Guards.SkipPlanReview = true
		}, expected: StateExecute},

		// PLAN_REVIEW transitions.
		{name: "PLAN_REVIEW→EXECUTE_no_approval", state: StatePlanReview, setup: func(cc *cycleContext) {
			cc.intent = &IntentPayload{TaskType: TaskCodeChange}
			cc.plan = &PlanPayload{
				Permissions: "read",
				Subtasks:    []Subtask{{ID: "st-1", Description: "implement change"}},
			}
		}, expected: StateExecute},
		{name: "PLAN_REVIEW→AWAIT_CLARIFICATION_ambiguous_plan", state: StatePlanReview, setup: func(cc *cycleContext) {
			cc.cfg.Clarifier = NewClarificationBroker()
			cc.intent = &IntentPayload{TaskType: TaskCodeChange}
			cc.plan = &PlanPayload{Permissions: "read"}
		}, expected: StateAwaitClarification},
		{name: "PLAN_REVIEW_transition_falls_through_without_clarifier", state: StatePlanReview, setup: func(cc *cycleContext) {
			cc.intent = &IntentPayload{TaskType: TaskCodeChange}
			cc.plan = &PlanPayload{Permissions: "read"}
		}, expected: StateExecute},
		{name: "PLAN_REVIEW→AWAIT_APPROVAL_edit", state: StatePlanReview, setup: func(cc *cycleContext) {
			cc.intent = &IntentPayload{TaskType: TaskCodeChange}
			cc.plan = &PlanPayload{
				Permissions: "edit",
				Subtasks:    []Subtask{{ID: "st-1", Description: "implement change"}},
			}
		}, expected: StateAwaitApproval},
		{name: "PLAN_REVIEW→AWAIT_APPROVAL_full", state: StatePlanReview, setup: func(cc *cycleContext) {
			cc.intent = &IntentPayload{TaskType: TaskCodeChange}
			cc.plan = &PlanPayload{
				Permissions: "full",
				Subtasks:    []Subtask{{ID: "st-1", Description: "implement change"}},
			}
		}, expected: StateAwaitApproval},

		// EXECUTE transitions.
		{name: "EXECUTE→OUTPUT_REVIEW", state: StateExecute, expected: StateOutputReview},
		{name: "EXECUTE→DECISION_GATE_skip_review", state: StateExecute, setup: func(cc *cycleContext) {
			cc.cfg.Guards.SkipOutputReview = true
		}, expected: StateDecisionGate},
		{name: "EXECUTE→RECOVERING_on_error", state: StateExecute, setup: func(cc *cycleContext) {
			cc.lastError = fmt.Errorf("test error")
		}, expected: StateRecovering},

		// OUTPUT_REVIEW transitions.
		{name: "OUTPUT_REVIEW→DECISION_GATE_no_blockers", state: StateOutputReview, expected: StateDecisionGate},
		{name: "OUTPUT_REVIEW→REVISE_with_blockers", state: StateOutputReview, setup: func(cc *cycleContext) {
			cc.transcript.Append(KindReviewFinding, "reviewer", StateOutputReview, "", 0, ReviewFindingPayload{
				Severity: SeverityBlocker,
				Summary:  "test blocker",
			})
		}, expected: StateRevise},
		{name: "OUTPUT_REVIEW→DECISION_GATE_blockers_no_budget", state: StateOutputReview, setup: func(cc *cycleContext) {
			cc.transcript.Append(KindReviewFinding, "reviewer", StateOutputReview, "", 0, ReviewFindingPayload{
				Severity: SeverityBlocker,
				Summary:  "test blocker",
			})
			cc.revisionCount = 3
			cc.cfg.Guards.MaxRevisionLoops = 3
		}, expected: StateDecisionGate},

		// REVISE always → OUTPUT_REVIEW.
		{name: "REVISE→OUTPUT_REVIEW", state: StateRevise, expected: StateOutputReview},

		// DECISION_GATE transitions.
		{name: "DECISION_GATE→DONE_discuss", state: StateDecisionGate, setup: func(cc *cycleContext) {
			cc.intent = &IntentPayload{TaskType: TaskDiscuss}
		}, expected: StateDone},
		{name: "DECISION_GATE→AWAIT_APPROVAL_code_change", state: StateDecisionGate, setup: func(cc *cycleContext) {
			cc.intent = &IntentPayload{TaskType: TaskCodeChange}
		}, expected: StateAwaitApproval},
		{name: "DECISION_GATE→AWAIT_APPROVAL_hybrid", state: StateDecisionGate, setup: func(cc *cycleContext) {
			cc.intent = &IntentPayload{TaskType: TaskHybrid}
		}, expected: StateAwaitApproval},

		// AWAIT_APPROVAL transitions.
		{name: "AWAIT_APPROVAL→resume_approved", state: StateAwaitApproval, setup: func(cc *cycleContext) {
			cc.resumeState = StateExecute
			cc.lastApproval = &PendingApproval{Approved: true}
		}, expected: StateExecute},
		{name: "AWAIT_APPROVAL→ABORTED_denied", state: StateAwaitApproval, setup: func(cc *cycleContext) {
			cc.resumeState = StateExecute
			cc.lastApproval = &PendingApproval{Approved: false}
		}, expected: StateAborted},
		{name: "AWAIT_CLARIFICATION→resume", state: StateAwaitClarification, setup: func(cc *cycleContext) {
			cc.resumeState = StatePlan
		}, expected: StatePlan},

		// RECOVERING transitions.
		{name: "RECOVERING→EXECUTE_can_retry", state: StateRecovering, setup: func(cc *cycleContext) {
			cc.retryCount = map[string]int{"st-1": 0}
		}, expected: StateExecute},
		{name: "RECOVERING→ABORTED_no_budget", state: StateRecovering, setup: func(cc *cycleContext) {
			cc.retryCount = map[string]int{"st-1": 5}
			cc.cfg.Guards.MaxRetriesPerTask = 2
		}, expected: StateAborted},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cc := newCycleContext(Config{
				Prompt: "test",
				Guards: DefaultGuards(),
			})
			if tt.setup != nil {
				tt.setup(cc)
			}

			got := transition(tt.state, cc)
			if got != tt.expected {
				t.Errorf("transition(%s) = %s, want %s", tt.state, got, tt.expected)
			}
		})
	}
}

func TestConditionCheckers(t *testing.T) {
	t.Run("needsApproval", func(t *testing.T) {
		cc := newCycleContext(Config{})
		if cc.needsApproval() {
			t.Error("nil plan should not need approval")
		}

		cc.plan = &PlanPayload{Permissions: "read"}
		if cc.needsApproval() {
			t.Error("read permissions should not need approval")
		}

		cc.plan.Permissions = "edit"
		if !cc.needsApproval() {
			t.Error("edit permissions should need approval")
		}
	})

	t.Run("hasBlockers", func(t *testing.T) {
		cc := newCycleContext(Config{})
		if cc.hasBlockers() {
			t.Error("empty transcript should have no blockers")
		}

		cc.transcript.Append(KindReviewFinding, "r", StateOutputReview, "", 0, ReviewFindingPayload{
			Severity: SeverityInfo,
		})
		if cc.hasBlockers() {
			t.Error("info findings should not count as blockers")
		}

		cc.transcript.Append(KindReviewFinding, "r", StateOutputReview, "", 0, ReviewFindingPayload{
			Severity: SeverityBlocker,
		})
		if !cc.hasBlockers() {
			t.Error("blocker finding should count")
		}
	})

	t.Run("revisionBudget", func(t *testing.T) {
		cc := newCycleContext(Config{Guards: DefaultGuards()})
		if cc.revisionBudget() != 3 {
			t.Errorf("initial budget = %d, want 3", cc.revisionBudget())
		}

		cc.revisionCount = 2
		if cc.revisionBudget() != 1 {
			t.Errorf("after 2 revisions budget = %d, want 1", cc.revisionBudget())
		}
	})

	t.Run("requiresOperatorDecision", func(t *testing.T) {
		cc := newCycleContext(Config{})
		if cc.requiresOperatorDecision() {
			t.Error("nil intent should not require operator")
		}

		cc.intent = &IntentPayload{TaskType: TaskDiscuss}
		if cc.requiresOperatorDecision() {
			t.Error("discuss should not require operator")
		}

		cc.intent.TaskType = TaskCodeChange
		if !cc.requiresOperatorDecision() {
			t.Error("code_change should require operator")
		}
	})

	t.Run("needsClarification", func(t *testing.T) {
		cc := newCycleContext(Config{})
		cc.intent = &IntentPayload{TaskType: TaskCodeChange}
		cc.plan = &PlanPayload{Permissions: "read"}
		if !cc.needsClarification() {
			t.Fatal("expected clarification when plan has no subtasks for code_change")
		}
		if cc.pendingClarification != "" {
			t.Fatal("needsClarification should not mutate pendingClarification")
		}

		cc.clarificationCount = DefaultGuards().MaxClarifications
		if cc.needsClarification() {
			t.Fatal("did not expect clarification when clarification budget is exhausted")
		}

		cc = newCycleContext(Config{})
		cc.intent = &IntentPayload{TaskType: TaskCodeChange}
		cc.plan = &PlanPayload{
			Permissions: "read",
			Subtasks:    []Subtask{{ID: "st-1", Description: "implement change"}},
		}
		if cc.needsClarification() {
			t.Fatal("did not expect clarification when plan has concrete subtasks")
		}

		cc.transcript.Append(KindReviewFinding, "reviewer", StatePlanReview, phaseName(StatePlanReview), 1, ReviewFindingPayload{
			Reviewer:              "reviewer",
			Severity:              SeverityWarn,
			Target:                "clarification",
			Summary:               "needs additional operator decision",
			NeedsClarification:    true,
			ClarificationQuestion: "Should we include backward compatibility for v1 clients?",
		})
		q, ok := cc.clarificationCandidate()
		if !ok {
			t.Fatal("expected clarificationCandidate from structured review finding")
		}
		if q != "Should we include backward compatibility for v1 clients?" {
			t.Fatalf("question = %q, want %q", q, "Should we include backward compatibility for v1 clients?")
		}
	})
}

func TestHasBlockersScopesToLatestOutputReview(t *testing.T) {
	cc := newCycleContext(Config{Guards: DefaultGuards()})

	// First OUTPUT_REVIEW pass has a blocker.
	cc.transcript.Append(KindStateChange, "coordinator", StateOutputReview, "", 0, StateChangePayload{
		From: StateExecute,
		To:   StateOutputReview,
	})
	cc.transcript.Append(KindReviewFinding, "reviewer", StateOutputReview, "", 0, ReviewFindingPayload{
		Severity: SeverityBlocker,
		Summary:  "old blocker",
	})
	cc.transcript.Append(KindStateChange, "coordinator", StateRevise, "", 0, StateChangePayload{
		From: StateOutputReview,
		To:   StateRevise,
	})

	// Second OUTPUT_REVIEW pass has no findings yet.
	cc.transcript.Append(KindStateChange, "coordinator", StateOutputReview, "", 0, StateChangePayload{
		From: StateRevise,
		To:   StateOutputReview,
	})

	if cc.hasBlockers() {
		t.Fatal("hasBlockers should ignore stale blockers from previous OUTPUT_REVIEW pass")
	}

	// Add a current-pass blocker and confirm detection.
	cc.transcript.Append(KindReviewFinding, "reviewer", StateOutputReview, "", 0, ReviewFindingPayload{
		Severity: SeverityBlocker,
		Summary:  "current blocker",
	})
	if !cc.hasBlockers() {
		t.Fatal("hasBlockers should detect blockers in current OUTPUT_REVIEW pass")
	}
}

func TestFillPlanDefaultsPermissions(t *testing.T) {
	tests := []struct {
		name      string
		taskType  TaskType
		approval  adapter.ApprovalLevel
		wantPerms string
	}{
		{name: "discuss_defaults_read_even_when_cli_edit", taskType: TaskDiscuss, approval: adapter.ApprovalEdit, wantPerms: "read"},
		{name: "code_change_defaults_edit", taskType: TaskCodeChange, approval: adapter.ApprovalEdit, wantPerms: "edit"},
		{name: "hybrid_respects_read_constraint", taskType: TaskHybrid, approval: adapter.ApprovalRead, wantPerms: "read"},
		{name: "hybrid_respects_full_constraint", taskType: TaskHybrid, approval: adapter.ApprovalFull, wantPerms: "full"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cc := newCycleContext(Config{Approval: tt.approval})
			cc.intent = &IntentPayload{TaskType: tt.taskType}
			cc.plan = &PlanPayload{}

			cc.fillPlanDefaults()

			if cc.plan.Permissions != tt.wantPerms {
				t.Fatalf("permissions = %q, want %q", cc.plan.Permissions, tt.wantPerms)
			}
		})
	}
}

func TestPlanDecisionActions(t *testing.T) {
	t.Run("worktree_with_commits_prefers_apply_on_approve", func(t *testing.T) {
		cc := newCycleContext(Config{})
		cc.intent = &IntentPayload{TaskType: TaskCodeChange}
		cc.worktreeInfo.Enabled = true
		cc.worktreeInfo.Branch = "tripartite/test/claude"
		cc.worktreeInfo.Commits = []store.DelegateCommit{{SHA: "abc123", Subject: "feat: test"}}

		got := cc.planDecisionActions()

		if got.Approve != decisionActionApplyWorktreeFF {
			t.Fatalf("Approve = %q, want %q", got.Approve, decisionActionApplyWorktreeFF)
		}
		if got.Deny != decisionActionKeepProposal {
			t.Fatalf("Deny = %q, want %q", got.Deny, decisionActionKeepProposal)
		}
		wantActions := []string{decisionActionApplyWorktreeFF, decisionActionAcceptResult, decisionActionKeepProposal}
		if !reflect.DeepEqual(got.Actions, wantActions) {
			t.Fatalf("Actions = %#v, want %#v", got.Actions, wantActions)
		}
	})

	t.Run("no_worktree_keeps_accept_or_proposal", func(t *testing.T) {
		cc := newCycleContext(Config{})
		cc.intent = &IntentPayload{TaskType: TaskHybrid}

		got := cc.planDecisionActions()

		if got.Approve != decisionActionAcceptResult {
			t.Fatalf("Approve = %q, want %q", got.Approve, decisionActionAcceptResult)
		}
		if got.Deny != decisionActionKeepProposal {
			t.Fatalf("Deny = %q, want %q", got.Deny, decisionActionKeepProposal)
		}
	})
}

func TestDecisionApprovalDenyDoesNotAbort(t *testing.T) {
	cc := newCycleContext(Config{})
	cc.decision = &DecisionPayload{}
	cc.resumeState = StateDone
	cc.lastApproval = &PendingApproval{Approved: false, Scope: ApprovalScopeDecisionGate}

	if cc.approvalDenied() {
		t.Fatal("approvalDenied should be false for denied decision-gate approval")
	}
	if got := transition(StateAwaitApproval, cc); got != StateDone {
		t.Fatalf("transition(AWAIT_APPROVAL) = %s, want %s", got, StateDone)
	}
}

func TestStateChangeMetadataUsesSourcePhaseAndPass(t *testing.T) {
	t.Run("output_review_pass_is_preserved", func(t *testing.T) {
		cc := newCycleContext(Config{Guards: DefaultGuards()})
		cc.state = StateOutputReview
		cc.currentPhase = phaseName(StateOutputReview)
		cc.outputReviewPassCount = 2

		to := transition(cc.state, cc) // no blockers -> DECISION_GATE
		cc.appendStateChange(cc.state, to)

		e := cc.transcript.Last(KindStateChange)
		if e == nil {
			t.Fatal("expected state change entry")
		}
		if e.State != StateOutputReview {
			t.Fatalf("entry.State = %s, want %s", e.State, StateOutputReview)
		}
		if e.Phase != "output_review" {
			t.Fatalf("entry.Phase = %q, want %q", e.Phase, "output_review")
		}
		if e.Pass != 2 {
			t.Fatalf("entry.Pass = %d, want 2", e.Pass)
		}
		payload, ok := e.Payload.(StateChangePayload)
		if !ok {
			t.Fatalf("payload type = %T, want StateChangePayload", e.Payload)
		}
		if payload.From != StateOutputReview || payload.To != StateDecisionGate {
			t.Fatalf("payload = %+v, want From=%s To=%s", payload, StateOutputReview, StateDecisionGate)
		}
	})

	t.Run("plan_review_pass_is_preserved", func(t *testing.T) {
		cc := newCycleContext(Config{Guards: DefaultGuards()})
		cc.state = StatePlanReview
		cc.currentPhase = phaseName(StatePlanReview)
		cc.planReviewPassCount = 1
		cc.intent = &IntentPayload{TaskType: TaskCodeChange}
		cc.plan = &PlanPayload{
			Permissions: "read",
			Subtasks:    []Subtask{{ID: "st-1", Description: "implement change"}},
		}

		to := transition(cc.state, cc) // read -> EXECUTE
		cc.appendStateChange(cc.state, to)

		e := cc.transcript.Last(KindStateChange)
		if e == nil {
			t.Fatal("expected state change entry")
		}
		if e.State != StatePlanReview {
			t.Fatalf("entry.State = %s, want %s", e.State, StatePlanReview)
		}
		if e.Phase != "plan_review" {
			t.Fatalf("entry.Phase = %q, want %q", e.Phase, "plan_review")
		}
		if e.Pass != 1 {
			t.Fatalf("entry.Pass = %d, want 1", e.Pass)
		}
		payload, ok := e.Payload.(StateChangePayload)
		if !ok {
			t.Fatalf("payload type = %T, want StateChangePayload", e.Payload)
		}
		if payload.From != StatePlanReview || payload.To != StateExecute {
			t.Fatalf("payload = %+v, want From=%s To=%s", payload, StatePlanReview, StateExecute)
		}
	})
}
