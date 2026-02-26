package cycle

import "testing"

func TestTranscriptLatestPassForPhase(t *testing.T) {
	tr := NewTranscript()
	tr.Append(KindReviewFinding, "r1", StateOutputReview, "output_review", 1, ReviewFindingPayload{Severity: SeverityInfo})
	tr.Append(KindReviewFinding, "r1", StateOutputReview, "output_review", 3, ReviewFindingPayload{Severity: SeverityWarn})
	tr.Append(KindReviewFinding, "r1", StatePlanReview, "plan_review", 2, ReviewFindingPayload{Severity: SeverityBlocker})
	tr.Append(KindArtifact, "a1", StateExecute, "execute", 0, ArtifactPayload{SubtaskID: "st-1"})

	if got := tr.LatestPassForPhase(KindReviewFinding, "output_review"); got != 3 {
		t.Fatalf("LatestPassForPhase(output_review) = %d, want 3", got)
	}
	if got := tr.LatestPassForPhase(KindReviewFinding, "plan_review"); got != 2 {
		t.Fatalf("LatestPassForPhase(plan_review) = %d, want 2", got)
	}
	if got := tr.LatestPassForPhase(KindArtifact, "execute"); got != 0 {
		t.Fatalf("LatestPassForPhase(artifact, execute) = %d, want 0", got)
	}
}

func TestTranscriptStatusSummary(t *testing.T) {
	tr := NewTranscript()

	// State changes should not dominate the "last activity" summary.
	tr.Append(KindStateChange, "coordinator", StatePlanReview, "plan_review", 1, StateChangePayload{
		From: StatePlan,
		To:   StatePlanReview,
	})
	tr.Append(KindReviewFinding, "claude", StatePlanReview, "plan_review", 1, ReviewFindingPayload{
		Reviewer: "claude",
		Target:   "st-1",
		Severity: SeverityWarn,
		Summary:  "needs clearer rollback steps",
	})
	tr.Append(KindReviewFinding, "gemini", StatePlanReview, "plan_review", 1, ReviewFindingPayload{
		Reviewer: "gemini",
		Target:   "st-2",
		Severity: SeverityBlocker,
		Summary:  "missing migration ordering",
	})
	tr.Append(KindStateChange, "coordinator", StateExecute, "plan_review", 1, StateChangePayload{
		From: StatePlanReview,
		To:   StateExecute,
	})

	s := tr.StatusSummary("plan_review", 1)
	if s.LastKind != KindReviewFinding {
		t.Fatalf("LastKind = %s, want %s", s.LastKind, KindReviewFinding)
	}
	if s.LastAgent != "gemini" {
		t.Fatalf("LastAgent = %q, want %q", s.LastAgent, "gemini")
	}
	if s.LastSummary == "" {
		t.Fatal("LastSummary should not be empty")
	}
	if s.Review == nil {
		t.Fatal("Review summary should be present for review phase")
	}
	if s.Review.Total != 2 || s.Review.Blockers != 1 || s.Review.Warns != 1 || s.Review.Infos != 0 {
		t.Fatalf("Review stats = %+v, want total=2 blockers=1 warns=1 infos=0", *s.Review)
	}
}

func TestTranscriptStatusSummaryNoReviewForNonReviewPhase(t *testing.T) {
	tr := NewTranscript()
	tr.Append(KindArtifact, "codex", StateExecute, "execute", 0, ArtifactPayload{
		SubtaskID: "st-1",
		Revision:  0,
	})

	s := tr.StatusSummary("execute", 0)
	if s.Review != nil {
		t.Fatalf("Review summary = %+v, want nil", *s.Review)
	}
	if s.LastKind != KindArtifact {
		t.Fatalf("LastKind = %s, want %s", s.LastKind, KindArtifact)
	}
}

func TestTranscriptPhaseBoardSummary(t *testing.T) {
	tr := NewTranscript()
	roles := &RoleMap{
		Planner:     "claude",
		Implementer: "codex",
		Reviewer:    "gemini",
	}

	// Current phase is output_review pass 2. Include multiple entries from the same agent;
	// only the latest per agent should remain.
	tr.Append(KindStateChange, "coordinator", StateOutputReview, "output_review", 2, StateChangePayload{
		From: StateExecute, To: StateOutputReview,
	})
	tr.Append(KindReviewFinding, "claude", StateOutputReview, "output_review", 2, ReviewFindingPayload{
		Reviewer: "claude", Severity: SeverityWarn, Target: "st-1", Summary: "old warning",
	})
	tr.Append(KindReviewFinding, "claude", StateOutputReview, "output_review", 2, ReviewFindingPayload{
		Reviewer: "claude", Severity: SeverityInfo, Target: "st-1", Summary: "newer note",
	})
	tr.Append(KindReviewFinding, "gemini", StateOutputReview, "output_review", 2, ReviewFindingPayload{
		Reviewer: "gemini", Severity: SeverityBlocker, Target: "st-2", Summary: "blocking issue",
	})
	tr.Append(KindError, "coordinator", StateOutputReview, "output_review", 2, "review parser hiccup")
	// Different pass should be ignored.
	tr.Append(KindReviewFinding, "codex", StateOutputReview, "output_review", 1, ReviewFindingPayload{
		Reviewer: "codex", Severity: SeverityWarn, Target: "st-3", Summary: "wrong pass",
	})

	board := tr.PhaseBoardSummary("output_review", 2, roles)
	if board == nil {
		t.Fatal("expected non-nil board")
	}
	if board.Phase != "output_review" || board.Pass != 2 {
		t.Fatalf("board = %+v, want phase=output_review pass=2", *board)
	}
	if len(board.Items) != 3 {
		t.Fatalf("len(board.Items) = %d, want 3", len(board.Items))
	}

	// Order should be coordinator, planner(claude), reviewer(gemini).
	if got := board.Items[0]; got.Agent != "coordinator" || got.Role != "coordinator" || got.Kind != KindError {
		t.Fatalf("item[0] = %+v, want coordinator error", got)
	}
	if got := board.Items[1]; got.Agent != "claude" || got.Role != "planner" || got.Kind != KindReviewFinding {
		t.Fatalf("item[1] = %+v, want planner review_finding", got)
	}
	if got := board.Items[2]; got.Agent != "gemini" || got.Role != "reviewer" || got.Kind != KindReviewFinding {
		t.Fatalf("item[2] = %+v, want reviewer review_finding", got)
	}
	if got := board.Items[1].Summary; got == "" || got == "old warning" {
		t.Fatalf("planner summary should come from latest entry, got %q", got)
	}
}

func TestTranscriptPhaseBoardSummaryNilWhenEmpty(t *testing.T) {
	tr := NewTranscript()
	if got := tr.PhaseBoardSummary("execute", 0, nil); got != nil {
		t.Fatalf("PhaseBoardSummary() = %+v, want nil", *got)
	}
}

func TestTranscriptRecentTimeline(t *testing.T) {
	tr := NewTranscript()
	roles := &RoleMap{
		Planner:     "claude",
		Implementer: "codex",
		Reviewer:    "gemini",
	}

	tr.Append(KindStateChange, "coordinator", StatePlan, "plan", 0, StateChangePayload{From: StateIntake, To: StatePlan})
	tr.Append(KindIntent, "coordinator", StateIntake, "intake", 0, IntentPayload{NormalizedGoal: "refactor auth"})
	tr.Append(KindPlan, "claude", StatePlan, "plan", 0, PlanPayload{Subtasks: []Subtask{{ID: "st-1"}}, Permissions: "edit"})
	tr.Append(KindReviewFinding, "gemini", StatePlanReview, "plan_review", 1, ReviewFindingPayload{
		Reviewer: "gemini", Severity: SeverityWarn, Target: "st-1", Summary: "clarify migration ordering",
	})
	tr.Append(KindArtifact, "codex", StateExecute, "execute", 0, ArtifactPayload{SubtaskID: "st-1", Revision: 0})
	tr.Append(KindApprovalRequest, "coordinator", StateAwaitApproval, "await_approval", 0, ApprovalRequestPayload{
		TicketID: "tk-1", Reason: "need approval", Scope: "execute",
	})

	got := tr.RecentTimeline(4, roles)
	if len(got) != 4 {
		t.Fatalf("RecentTimeline len = %d, want 4", len(got))
	}

	// Oldest -> newest among the selected events.
	if got[0].Kind != KindPlan || got[0].Agent != "claude" || got[0].Role != "planner" {
		t.Fatalf("event[0] = %+v, want planner plan", got[0])
	}
	if got[1].Kind != KindReviewFinding || got[1].Agent != "gemini" || got[1].Role != "reviewer" {
		t.Fatalf("event[1] = %+v, want reviewer finding", got[1])
	}
	if got[2].Kind != KindArtifact || got[2].Agent != "codex" || got[2].Role != "implementer" {
		t.Fatalf("event[2] = %+v, want implementer artifact", got[2])
	}
	if got[3].Kind != KindApprovalRequest || got[3].Agent != "coordinator" || got[3].Role != "coordinator" {
		t.Fatalf("event[3] = %+v, want coordinator approval_request", got[3])
	}
}
