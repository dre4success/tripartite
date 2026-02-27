package cycle

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dre4success/tripartite/store"
)

func TestRunResumeAwaitApprovalDecisionApproveToDone(t *testing.T) {
	s := newTempStore(t)
	turn := 1
	now := time.Now().UTC()

	entries := []Entry{
		{
			ID:        1,
			Kind:      KindDecision,
			Timestamp: now,
			Agent:     "coordinator",
			State:     StateDecisionGate,
			Phase:     phaseName(StateDecisionGate),
			Payload: DecisionPayload{
				Recommendation: "Apply changes",
				Actions:        []string{decisionActionAcceptResult, decisionActionKeepProposal},
			},
		},
		{
			ID:        2,
			Kind:      KindApprovalRequest,
			Timestamp: now.Add(1 * time.Second),
			Agent:     "coordinator",
			State:     StateAwaitApproval,
			Phase:     phaseName(StateAwaitApproval),
			Payload: ApprovalRequestPayload{
				TicketID:    "tk-old",
				Reason:      "Decision required",
				Scope:       decisionGateApprovalScope,
				ResumeState: StateDone,
			},
		},
	}
	saveResumeFixtures(t, s, turn, entries, []store.CycleCheckpoint{
		{
			CycleID:    "cycle-approval-done",
			State:      string(StateAwaitApproval),
			Timestamp:  now.Add(2 * time.Second),
			EntryCount: len(entries),
			Elapsed:    2 * time.Second,
		},
		{
			CycleID:    "cycle-approval-done",
			State:      string(StateAborted),
			Timestamp:  now.Add(3 * time.Second),
			EntryCount: len(entries) + 1,
			Elapsed:    3 * time.Second,
			Error:      "context canceled",
		},
	})

	broker := NewApprovalBroker()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	type out struct {
		r   *Result
		err error
	}
	done := make(chan out, 1)
	go func() {
		r, err := RunResume(ctx, Config{
			Store:   s,
			TurnNum: turn,
			Broker:  broker,
			Guards:  DefaultGuards(),
		})
		done <- out{r: r, err: err}
	}()

	ticket := waitResumeApprovalTicket(t, broker, 2*time.Second)
	if err := broker.Resolve(ticket, true, ""); err != nil {
		t.Fatalf("Resolve approval: %v", err)
	}

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("RunResume returned error: %v", got.err)
		}
		if got.r == nil {
			t.Fatal("RunResume result is nil")
		}
		if got.r.FinalState != StateDone {
			t.Fatalf("FinalState = %s, want %s", got.r.FinalState, StateDone)
		}
		if got.r.DecisionAction == nil {
			t.Fatal("expected DecisionAction payload after decision approval")
		}
		if got.r.DecisionAction.Action != decisionActionAcceptResult || !got.r.DecisionAction.Succeeded {
			t.Fatalf("DecisionAction = %#v", got.r.DecisionAction)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for resume completion")
	}
}

func TestRunResumeAwaitApprovalDenyToAborted(t *testing.T) {
	s := newTempStore(t)
	turn := 1
	now := time.Now().UTC()

	entries := []Entry{
		{
			ID:        1,
			Kind:      KindApprovalRequest,
			Timestamp: now,
			Agent:     "coordinator",
			State:     StateAwaitApproval,
			Phase:     phaseName(StateAwaitApproval),
			Payload: ApprovalRequestPayload{
				TicketID:    "tk-old",
				Reason:      "Need approval to execute write plan",
				Scope:       string(StatePlanReview),
				ResumeState: StateExecute,
			},
		},
	}
	saveResumeFixtures(t, s, turn, entries, []store.CycleCheckpoint{
		{
			CycleID:    "cycle-approval-abort",
			State:      string(StateAwaitApproval),
			Timestamp:  now.Add(1 * time.Second),
			EntryCount: len(entries),
			Elapsed:    1 * time.Second,
		},
	})

	broker := NewApprovalBroker()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	type out struct {
		r   *Result
		err error
	}
	done := make(chan out, 1)
	go func() {
		r, err := RunResume(ctx, Config{
			Store:   s,
			TurnNum: turn,
			Broker:  broker,
			Guards:  DefaultGuards(),
		})
		done <- out{r: r, err: err}
	}()

	ticket := waitResumeApprovalTicket(t, broker, 2*time.Second)
	if err := broker.Resolve(ticket, false, "no"); err != nil {
		t.Fatalf("Resolve approval: %v", err)
	}

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("RunResume returned error: %v", got.err)
		}
		if got.r == nil {
			t.Fatal("RunResume result is nil")
		}
		if got.r.FinalState != StateAborted {
			t.Fatalf("FinalState = %s, want %s", got.r.FinalState, StateAborted)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for resume completion")
	}
}

func TestRunResumeAwaitClarificationToDone(t *testing.T) {
	s := newTempStore(t)
	turn := 1
	now := time.Now().UTC()

	entries := []Entry{
		{
			ID:        1,
			Kind:      KindClarifyRequest,
			Timestamp: now,
			Agent:     "coordinator",
			State:     StateAwaitClarification,
			Phase:     phaseName(StateAwaitClarification),
			Payload: ClarificationRequestPayload{
				TicketID:    "cq-old",
				Question:    "Which module should be migrated first?",
				ResumeState: StateDone,
			},
		},
	}
	saveResumeFixtures(t, s, turn, entries, []store.CycleCheckpoint{
		{
			CycleID:    "cycle-clarify-done",
			State:      string(StateAwaitClarification),
			Timestamp:  now.Add(1 * time.Second),
			EntryCount: len(entries),
			Elapsed:    1 * time.Second,
		},
		{
			CycleID:    "cycle-clarify-done",
			State:      string(StateAborted),
			Timestamp:  now.Add(2 * time.Second),
			EntryCount: len(entries) + 1,
			Elapsed:    2 * time.Second,
			Error:      "context canceled",
		},
	})

	clarifier := NewClarificationBroker()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	type out struct {
		r   *Result
		err error
	}
	done := make(chan out, 1)
	go func() {
		r, err := RunResume(ctx, Config{
			Store:     s,
			TurnNum:   turn,
			Clarifier: clarifier,
			Guards:    DefaultGuards(),
		})
		done <- out{r: r, err: err}
	}()

	ticket := waitResumeClarificationTicket(t, clarifier, 2*time.Second)
	if err := clarifier.Resolve(ticket, "Start with auth package."); err != nil {
		t.Fatalf("Resolve clarification: %v", err)
	}

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("RunResume returned error: %v", got.err)
		}
		if got.r == nil {
			t.Fatal("RunResume result is nil")
		}
		if got.r.FinalState != StateDone {
			t.Fatalf("FinalState = %s, want %s", got.r.FinalState, StateDone)
		}
		if last := got.r.Transcript.Last(KindClarifyResult); last == nil {
			t.Fatal("expected clarify_result entry after resume clarification")
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for resume completion")
	}
}

func TestRunResumeMissingTranscript(t *testing.T) {
	s := newTempStore(t)
	turn := 1
	now := time.Now().UTC()

	if err := s.SaveCycleCheckpoint(turn, store.CycleCheckpoint{
		CycleID:    "cycle-missing-transcript",
		State:      string(StateAwaitApproval),
		Timestamp:  now,
		EntryCount: 0,
	}); err != nil {
		t.Fatalf("SaveCycleCheckpoint: %v", err)
	}

	_, err := RunResume(context.Background(), Config{
		Store:   s,
		TurnNum: turn,
		Broker:  NewApprovalBroker(),
		Guards:  DefaultGuards(),
	})
	if err == nil {
		t.Fatal("expected RunResume error for missing transcript")
	}
	if !strings.Contains(err.Error(), "transcript") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunResumeRequiresBrokerAndClarifierForInterruptStates(t *testing.T) {
	t.Run("await_approval_requires_broker", func(t *testing.T) {
		s := newTempStore(t)
		turn := 1
		now := time.Now().UTC()
		entries := []Entry{
			{
				ID:        1,
				Kind:      KindApprovalRequest,
				Timestamp: now,
				Agent:     "coordinator",
				State:     StateAwaitApproval,
				Payload: ApprovalRequestPayload{
					TicketID:    "tk-1",
					Scope:       string(StatePlanReview),
					ResumeState: StateExecute,
				},
			},
		}
		saveResumeFixtures(t, s, turn, entries, []store.CycleCheckpoint{
			{
				CycleID:    "cycle-await-approval",
				State:      string(StateAwaitApproval),
				Timestamp:  now.Add(1 * time.Second),
				EntryCount: len(entries),
			},
		})

		_, err := RunResume(context.Background(), Config{
			Store:   s,
			TurnNum: turn,
			Guards:  DefaultGuards(),
		})
		if err == nil {
			t.Fatal("expected error when broker is missing")
		}
		if !strings.Contains(err.Error(), "approval broker") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("await_clarification_requires_clarifier", func(t *testing.T) {
		s := newTempStore(t)
		turn := 1
		now := time.Now().UTC()
		entries := []Entry{
			{
				ID:        1,
				Kind:      KindClarifyRequest,
				Timestamp: now,
				Agent:     "coordinator",
				State:     StateAwaitClarification,
				Payload: ClarificationRequestPayload{
					TicketID:    "cq-1",
					Question:    "Need clarification",
					ResumeState: StatePlan,
				},
			},
		}
		saveResumeFixtures(t, s, turn, entries, []store.CycleCheckpoint{
			{
				CycleID:    "cycle-await-clarification",
				State:      string(StateAwaitClarification),
				Timestamp:  now.Add(1 * time.Second),
				EntryCount: len(entries),
			},
		})

		_, err := RunResume(context.Background(), Config{
			Store:   s,
			TurnNum: turn,
			Guards:  DefaultGuards(),
		})
		if err == nil {
			t.Fatal("expected error when clarifier is missing")
		}
		if !strings.Contains(err.Error(), "clarification broker") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestRunResumeRejectsUnsafeLatestState(t *testing.T) {
	s := newTempStore(t)
	turn := 1
	now := time.Now().UTC()
	if err := s.SaveCycleCheckpoint(turn, store.CycleCheckpoint{
		CycleID:    "cycle-unsafe",
		State:      string(StateExecute),
		Timestamp:  now,
		EntryCount: 0,
	}); err != nil {
		t.Fatalf("SaveCycleCheckpoint: %v", err)
	}
	if err := s.SaveCycleTranscript(turn, []Entry{}); err != nil {
		t.Fatalf("SaveCycleTranscript: %v", err)
	}

	_, err := RunResume(context.Background(), Config{
		Store:   s,
		TurnNum: turn,
		Guards:  DefaultGuards(),
	})
	if err == nil {
		t.Fatal("expected error for unsafe latest state")
	}
	if !strings.Contains(err.Error(), "safe resume state") && !strings.Contains(err.Error(), "not a safe resume state") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func newTempStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	s, err := store.New(dir)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	return s
}

func saveResumeFixtures(t *testing.T, s *store.Store, turn int, entries []Entry, checkpoints []store.CycleCheckpoint) {
	t.Helper()
	for _, cp := range checkpoints {
		if err := s.SaveCycleCheckpoint(turn, cp); err != nil {
			t.Fatalf("SaveCycleCheckpoint(%s): %v", cp.State, err)
		}
	}
	if err := s.SaveCycleTranscript(turn, entries); err != nil {
		t.Fatalf("SaveCycleTranscript: %v", err)
	}
}

func waitResumeApprovalTicket(t *testing.T, broker *ApprovalBroker, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		pending := broker.Pending()
		if len(pending) > 0 {
			return pending[0].TicketID
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("expected pending approval ticket")
	return ""
}

func waitResumeClarificationTicket(t *testing.T, clarifier *ClarificationBroker, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		pending := clarifier.Pending()
		if len(pending) > 0 {
			return pending[0].TicketID
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("expected pending clarification ticket")
	return ""
}
