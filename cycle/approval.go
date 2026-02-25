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
	notify  chan string // ticketID when resolved
}

// NewApprovalBroker creates a new broker.
func NewApprovalBroker() *ApprovalBroker {
	return &ApprovalBroker{
		pending: make(map[string]*PendingApproval),
		notify:  make(chan string, 16),
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
	pa.Resolved = true
	pa.Approved = approved
	pa.Comment = comment
	b.mu.Unlock()

	// Non-blocking send; buffer should be large enough.
	select {
	case b.notify <- ticketID:
	default:
	}
	return nil
}

// Wait blocks until the given ticket is resolved or the context is cancelled.
func (b *ApprovalBroker) Wait(ctx context.Context, ticketID string) (*PendingApproval, error) {
	for {
		b.mu.Lock()
		pa, ok := b.pending[ticketID]
		if ok && pa.Resolved {
			b.mu.Unlock()
			return pa, nil
		}
		b.mu.Unlock()

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case resolved := <-b.notify:
			if resolved == ticketID {
				b.mu.Lock()
				pa := b.pending[ticketID]
				b.mu.Unlock()
				return pa, nil
			}
			// Not our ticket, loop again.
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
