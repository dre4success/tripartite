package cycle

import (
	"sync"
	"testing"
)

func TestTranscriptAppendAndEntries(t *testing.T) {
	tr := NewTranscript()

	e1 := tr.Append(KindIntent, "coordinator", StateIntake, "test-payload-1")
	e2 := tr.Append(KindPlan, "agent-a", StatePlan, "test-payload-2")

	if e1.ID != 1 {
		t.Errorf("first entry ID = %d, want 1", e1.ID)
	}
	if e2.ID != 2 {
		t.Errorf("second entry ID = %d, want 2", e2.ID)
	}

	entries := tr.Entries()
	if len(entries) != 2 {
		t.Fatalf("Entries() returned %d entries, want 2", len(entries))
	}

	// Verify Entries() returns a copy.
	entries[0].Agent = "mutated"
	original := tr.Entries()
	if original[0].Agent == "mutated" {
		t.Error("Entries() should return a copy, but mutation leaked")
	}
}

func TestTranscriptLast(t *testing.T) {
	tr := NewTranscript()

	tr.Append(KindIntent, "coordinator", StateIntake, "first")
	tr.Append(KindArtifact, "agent-a", StateExecute, "artifact-1")
	tr.Append(KindArtifact, "agent-b", StateExecute, "artifact-2")

	last := tr.Last(KindArtifact)
	if last == nil {
		t.Fatal("Last(KindArtifact) returned nil")
	}
	if last.Agent != "agent-b" {
		t.Errorf("Last(KindArtifact).Agent = %q, want %q", last.Agent, "agent-b")
	}

	// Non-existent kind.
	if tr.Last(KindDecision) != nil {
		t.Error("Last(KindDecision) should return nil")
	}
}

func TestTranscriptByKind(t *testing.T) {
	tr := NewTranscript()

	tr.Append(KindIntent, "coordinator", StateIntake, "intent")
	tr.Append(KindArtifact, "agent-a", StateExecute, "a1")
	tr.Append(KindError, "coordinator", StateExecute, "err1")
	tr.Append(KindArtifact, "agent-b", StateExecute, "a2")

	artifacts := tr.ByKind(KindArtifact)
	if len(artifacts) != 2 {
		t.Errorf("ByKind(KindArtifact) returned %d, want 2", len(artifacts))
	}

	errors := tr.ByKind(KindError)
	if len(errors) != 1 {
		t.Errorf("ByKind(KindError) returned %d, want 1", len(errors))
	}

	decisions := tr.ByKind(KindDecision)
	if len(decisions) != 0 {
		t.Errorf("ByKind(KindDecision) returned %d, want 0", len(decisions))
	}
}

func TestTranscriptLen(t *testing.T) {
	tr := NewTranscript()
	if tr.Len() != 0 {
		t.Errorf("empty transcript Len() = %d, want 0", tr.Len())
	}

	tr.Append(KindIntent, "", StateInit, nil)
	tr.Append(KindPlan, "", StatePlan, nil)

	if tr.Len() != 2 {
		t.Errorf("Len() = %d, want 2", tr.Len())
	}
}

func TestTranscriptConcurrency(t *testing.T) {
	tr := NewTranscript()
	var wg sync.WaitGroup

	// Concurrent appends.
	for i := range 50 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			tr.Append(KindArtifact, "agent", StateExecute, n)
		}(i)
	}

	// Concurrent reads.
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = tr.Entries()
			_ = tr.Last(KindArtifact)
			_ = tr.ByKind(KindArtifact)
			_ = tr.Len()
		}()
	}

	wg.Wait()

	if tr.Len() != 50 {
		t.Errorf("after concurrent appends, Len() = %d, want 50", tr.Len())
	}
}
