package cycle

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// PendingApproval represents an approval request waiting for operator input.
type PendingApproval struct {
	TicketID    string
	Reason      string
	Scope       string
	ResumeState State
	Resolved    bool
	Approved    bool
	Comment     string
}

// ApprovalBroker coordinates approval requests between the cycle goroutine
// and the REPL. The cycle calls Request() + Wait(); the REPL calls Resolve().
type ApprovalBroker struct {
	mu      sync.Mutex
	pending map[string]*PendingApproval
	waiters map[string]chan struct{} // ticketID -> closed on resolve
}

// NewApprovalBroker creates a new broker.
func NewApprovalBroker() *ApprovalBroker {
	return &ApprovalBroker{
		pending: make(map[string]*PendingApproval),
		waiters: make(map[string]chan struct{}),
	}
}

// Request creates a new pending approval and returns it.
func (b *ApprovalBroker) Request(reason, scope string, resumeState State) *PendingApproval {
	b.mu.Lock()
	defer b.mu.Unlock()

	id := generateTicketID()
	pa := &PendingApproval{
		TicketID:    id,
		Reason:      reason,
		Scope:       scope,
		ResumeState: resumeState,
	}
	b.pending[id] = pa
	return pa
}

// Resolve marks an approval as resolved. Called by the REPL.
func (b *ApprovalBroker) Resolve(ticketID string, approved bool, comment string) error {
	b.mu.Lock()
	pa, ok := b.pending[ticketID]
	if !ok {
		b.mu.Unlock()
		return fmt.Errorf("unknown approval ticket %q", ticketID)
	}
	alreadyResolved := pa.Resolved
	pa.Resolved = true
	pa.Approved = approved
	pa.Comment = comment
	waitCh, hasWaiter := b.waiters[ticketID]
	if hasWaiter {
		delete(b.waiters, ticketID)
	}
	b.mu.Unlock()

	if !alreadyResolved && hasWaiter {
		close(waitCh)
	}
	return nil
}

// Wait blocks until the given ticket is resolved or the context is cancelled.
func (b *ApprovalBroker) Wait(ctx context.Context, ticketID string) (*PendingApproval, error) {
	for {
		b.mu.Lock()
		pa, ok := b.pending[ticketID]
		if !ok {
			b.mu.Unlock()
			return nil, fmt.Errorf("unknown approval ticket %q", ticketID)
		}
		if pa.Resolved {
			b.mu.Unlock()
			return pa, nil
		}
		waitCh := b.waiterForLocked(ticketID)
		b.mu.Unlock()

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-waitCh:
		}
	}
}

// Pending returns all unresolved approvals.
func (b *ApprovalBroker) Pending() []*PendingApproval {
	b.mu.Lock()
	defer b.mu.Unlock()

	var out []*PendingApproval
	for _, pa := range b.pending {
		if !pa.Resolved {
			out = append(out, pa)
		}
	}
	return out
}

func generateTicketID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("tk-%d", time.Now().UnixNano())
	}
	return "tk-" + hex.EncodeToString(b[:])
}

func (b *ApprovalBroker) waiterForLocked(ticketID string) chan struct{} {
	ch, ok := b.waiters[ticketID]
	if !ok {
		ch = make(chan struct{})
		b.waiters[ticketID] = ch
	}
	return ch
}
