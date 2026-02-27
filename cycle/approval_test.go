package cycle

import (
	"context"
	"testing"
	"time"
)

func TestApprovalBrokerWaitUnknownTicket(t *testing.T) {
	t.Parallel()

	b := NewApprovalBroker()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	if _, err := b.Wait(ctx, "tk-missing"); err == nil {
		t.Fatal("expected error for unknown ticket")
	}
}

func TestApprovalBrokerWaitResolve(t *testing.T) {
	t.Parallel()

	b := NewApprovalBroker()
	req := b.Request("need write access", "EXECUTE", StateExecute)

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
