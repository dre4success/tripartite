package cycle

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// PendingClarification represents a clarification request awaiting operator input.
type PendingClarification struct {
	TicketID    string
	Question    string
	ResumeState State
	Resolved    bool
	Answer      string
}

// ClarificationBroker coordinates clarification requests between the cycle and REPL.
type ClarificationBroker struct {
	mu      sync.Mutex
	pending map[string]*PendingClarification
	waiters map[string]chan struct{}
}

// NewClarificationBroker creates a broker for clarification interrupts.
func NewClarificationBroker() *ClarificationBroker {
	return &ClarificationBroker{
		pending: make(map[string]*PendingClarification),
		waiters: make(map[string]chan struct{}),
	}
}

// Request creates a clarification request and returns it.
func (b *ClarificationBroker) Request(question string, resumeState State) *PendingClarification {
	b.mu.Lock()
	defer b.mu.Unlock()

	id := generateClarificationID()
	pc := &PendingClarification{
		TicketID:    id,
		Question:    question,
		ResumeState: resumeState,
	}
	b.pending[id] = pc
	return pc
}

// Resolve marks a clarification request as resolved.
func (b *ClarificationBroker) Resolve(ticketID, answer string) error {
	b.mu.Lock()
	pc, ok := b.pending[ticketID]
	if !ok {
		b.mu.Unlock()
		return fmt.Errorf("unknown clarification ticket %q", ticketID)
	}
	alreadyResolved := pc.Resolved
	pc.Resolved = true
	pc.Answer = answer
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

// Wait blocks until the given clarification ticket is resolved or ctx is cancelled.
func (b *ClarificationBroker) Wait(ctx context.Context, ticketID string) (*PendingClarification, error) {
	for {
		b.mu.Lock()
		pc, ok := b.pending[ticketID]
		if !ok {
			b.mu.Unlock()
			return nil, fmt.Errorf("unknown clarification ticket %q", ticketID)
		}
		if pc.Resolved {
			b.mu.Unlock()
			return pc, nil
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

// Pending returns all unresolved clarification requests.
func (b *ClarificationBroker) Pending() []*PendingClarification {
	b.mu.Lock()
	defer b.mu.Unlock()

	var out []*PendingClarification
	for _, pc := range b.pending {
		if !pc.Resolved {
			out = append(out, pc)
		}
	}
	return out
}

func generateClarificationID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("cq-%d", time.Now().UnixNano())
	}
	return "cq-" + hex.EncodeToString(b[:])
}

func (b *ClarificationBroker) waiterForLocked(ticketID string) chan struct{} {
	ch, ok := b.waiters[ticketID]
	if !ok {
		ch = make(chan struct{})
		b.waiters[ticketID] = ch
	}
	return ch
}
