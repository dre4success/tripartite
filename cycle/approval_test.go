package cycle

import (
	"context"
	"testing"
	"time"
)

func TestNormalizeApprovalKind(t *testing.T) {
	tests := []struct {
		name  string
		kind  ApprovalKind
		scope string
		want  ApprovalKind
	}{
		{name: "explicit permission wins", kind: ApprovalKindPermission, scope: ApprovalScopeDecisionGate, want: ApprovalKindPermission},
		{name: "explicit decision wins", kind: ApprovalKindDecision, scope: "execute", want: ApprovalKindDecision},
		{name: "scope decision fallback", kind: "", scope: ApprovalScopeDecisionGate, want: ApprovalKindDecision},
		{name: "default permission fallback", kind: "", scope: "execute", want: ApprovalKindPermission},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeApprovalKind(tt.kind, tt.scope)
			if got != tt.want {
				t.Fatalf("NormalizeApprovalKind(%q, %q) = %q, want %q", tt.kind, tt.scope, got, tt.want)
			}
		})
	}
}

func TestApprovalBrokerWaitUnknownTicket(t *testing.T) {
	b := NewApprovalBroker()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	if _, err := b.Wait(ctx, "tk-missing"); err == nil {
		t.Fatal("expected error for unknown ticket")
	}
}

func TestApprovalBrokerWaitResolve(t *testing.T) {
	b := NewApprovalBroker()
	req := b.Request("need write access", "EXECUTE", StateExecute)
	if req.Kind != ApprovalKindPermission {
		t.Fatalf("request kind = %q, want %q", req.Kind, ApprovalKindPermission)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	done := make(chan *PendingApproval, 1)
	errs := make(chan error, 1)
	go func() {
		resolved, err := b.Wait(ctx, req.TicketID)
		if err != nil {
			errs <- err
			return
		}
		done <- resolved
	}()

	if err := b.Resolve(req.TicketID, true, "ok"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	select {
	case err := <-errs:
		t.Fatalf("Wait returned error: %v", err)
	case resolved := <-done:
		if !resolved.Approved {
			t.Fatal("approved = false, want true")
		}
		if resolved.Comment != "ok" {
			t.Fatalf("comment = %q, want %q", resolved.Comment, "ok")
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for approval resolution")
	}
}

func TestApprovalBrokerRequestDecisionKindFromScope(t *testing.T) {
	b := NewApprovalBroker()
	req := b.Request("decision required", ApprovalScopeDecisionGate, StateDone)
	if req.Kind != ApprovalKindDecision {
		t.Fatalf("request kind = %q, want %q", req.Kind, ApprovalKindDecision)
	}
}
