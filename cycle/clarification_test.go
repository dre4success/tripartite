package cycle

import (
	"context"
	"testing"
	"time"
)

func TestClarificationBrokerWaitUnknownTicket(t *testing.T) {
	t.Parallel()

	b := NewClarificationBroker()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	if _, err := b.Wait(ctx, "cq-missing"); err == nil {
		t.Fatal("expected error for unknown ticket")
	}
}

func TestClarificationInterruptFlow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	clarifier := NewClarificationBroker()
	cc := newCycleContext(Config{
		Clarifier: clarifier,
		Guards:    DefaultGuards(),
	})

	// Start at PLAN_REVIEW with an ambiguous plan (no subtasks for code-change).
	cc.state = StatePlanReview
	cc.currentPhase = phaseName(StatePlanReview)
	cc.intent = &IntentPayload{TaskType: TaskCodeChange}
	cc.plan = &PlanPayload{Permissions: "read"}

	next := transition(StatePlanReview, cc)
	if next != StateAwaitClarification {
		t.Fatalf("transition(PLAN_REVIEW) = %s, want %s", next, StateAwaitClarification)
	}
	if cc.resumeState != StatePlan {
		t.Fatalf("resumeState = %s, want %s", cc.resumeState, StatePlan)
	}

	// Enter clarification state and wait in background.
	cc.state = StateAwaitClarification
	cc.currentPhase = phaseName(StateAwaitClarification)
	waitDone := make(chan error, 1)
	go func() {
		waitDone <- cc.handleAwaitClarification(ctx)
	}()

	// Wait for the broker ticket, then resolve it as the operator.
	var ticketID string
	deadline := time.Now().Add(800 * time.Millisecond)
	for time.Now().Before(deadline) {
		pending := clarifier.Pending()
		if len(pending) > 0 {
			ticketID = pending[0].TicketID
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if ticketID == "" {
		t.Fatal("expected pending clarification ticket")
	}

	answer := "Scope to auth module first, then add migration notes."
	if err := clarifier.Resolve(ticketID, answer); err != nil {
		t.Fatalf("Resolve clarification: %v", err)
	}

	select {
	case err := <-waitDone:
		if err != nil {
			t.Fatalf("handleAwaitClarification returned error: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for clarification handler")
	}

	if cc.clarificationCount != 1 {
		t.Fatalf("clarificationCount = %d, want 1", cc.clarificationCount)
	}
	if len(cc.clarifications) != 1 || cc.clarifications[0] != answer {
		t.Fatalf("clarifications = %#v, want [%q]", cc.clarifications, answer)
	}
	if cc.pendingClarification != "" {
		t.Fatalf("pendingClarification = %q, want empty", cc.pendingClarification)
	}

	req := cc.transcript.Last(KindClarifyRequest)
	if req == nil {
		t.Fatal("missing clarify_request transcript entry")
	}
	res := cc.transcript.Last(KindClarifyResult)
	if res == nil {
		t.Fatal("missing clarify_result transcript entry")
	}

	resultPayload, ok := res.Payload.(ClarificationResultPayload)
	if !ok {
		t.Fatalf("clarify_result payload type = %T", res.Payload)
	}
	if resultPayload.Answer != answer {
		t.Fatalf("clarify_result answer = %q, want %q", resultPayload.Answer, answer)
	}

	next = transition(StateAwaitClarification, cc)
	if next != StatePlan {
		t.Fatalf("transition(AWAIT_CLARIFICATION) = %s, want %s", next, StatePlan)
	}
}
