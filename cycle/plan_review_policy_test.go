package cycle

import (
	"context"
	"strings"
	"testing"
)

func TestPlanReviewNonInteractiveClarificationPolicyStrictFail(t *testing.T) {
	cc := newCycleContext(Config{
		Guards: DefaultGuards(),
	})
	cc.state = StatePlanReview
	cc.currentPhase = phaseName(StatePlanReview)
	cc.intent = &IntentPayload{TaskType: TaskCodeChange}
	cc.plan = &PlanPayload{Permissions: "read"} // ambiguous: no subtasks

	err := cc.handlePlanReview(context.Background())
	if err == nil {
		t.Fatal("expected strict-fail error when clarification is required in non-interactive mode")
	}
	if !strings.Contains(err.Error(), "clarification required") {
		t.Fatalf("unexpected error: %v", err)
	}

	e := cc.transcript.Last(KindClarifyRequest)
	if e == nil {
		t.Fatal("expected clarify_request transcript entry for strict-fail policy")
	}
	p, ok := e.Payload.(ClarificationRequestPayload)
	if !ok {
		t.Fatalf("clarify_request payload type = %T", e.Payload)
	}
	if p.TicketID != "non-interactive" {
		t.Fatalf("ticket_id = %q, want non-interactive", p.TicketID)
	}
	if strings.TrimSpace(p.Question) == "" {
		t.Fatal("expected non-empty clarification question")
	}
	if p.ResumeState != StatePlan {
		t.Fatalf("resume_state = %s, want %s", p.ResumeState, StatePlan)
	}
}

func TestPlanReviewInteractiveClarificationPolicyDoesNotFail(t *testing.T) {
	cc := newCycleContext(Config{
		Clarifier: NewClarificationBroker(),
		Guards:    DefaultGuards(),
	})
	cc.state = StatePlanReview
	cc.currentPhase = phaseName(StatePlanReview)
	cc.intent = &IntentPayload{TaskType: TaskCodeChange}
	cc.plan = &PlanPayload{Permissions: "read"} // ambiguous: no subtasks

	if err := cc.handlePlanReview(context.Background()); err != nil {
		t.Fatalf("unexpected error in interactive mode: %v", err)
	}

	if got := cc.transcript.Last(KindClarifyRequest); got != nil {
		t.Fatalf("did not expect clarify_request entry during plan review in interactive mode, got %+v", got)
	}
}
