package cycle

import (
	"sync"
	"testing"
)

func TestTranscriptAppendAndEntries(t *testing.T) {
	tr := NewTranscript()

	e1 := tr.Append(KindIntent, "coordinator", StateIntake, "", 0, "test-payload-1")
	e2 := tr.Append(KindPlan, "agent-a", StatePlan, "", 0, "test-payload-2")

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

	tr.Append(KindIntent, "coordinator", StateIntake, "", 0, "first")
	tr.Append(KindArtifact, "agent-a", StateExecute, "", 0, "artifact-1")
	tr.Append(KindArtifact, "agent-b", StateExecute, "", 0, "artifact-2")

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

	tr.Append(KindIntent, "coordinator", StateIntake, "", 0, "intent")
	tr.Append(KindArtifact, "agent-a", StateExecute, "", 0, "a1")
	tr.Append(KindError, "coordinator", StateExecute, "", 0, "err1")
	tr.Append(KindArtifact, "agent-b", StateExecute, "", 0, "a2")

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

func TestTranscriptByKindAndPass(t *testing.T) {
	tr := NewTranscript()

	// Simulate two OUTPUT_REVIEW passes with findings.
	tr.Append(KindReviewFinding, "reviewer", StateOutputReview, "output_review", 1, ReviewFindingPayload{
		Severity: SeverityBlocker,
		Summary:  "pass-1 blocker",
	})
	tr.Append(KindReviewFinding, "reviewer", StateOutputReview, "output_review", 1, ReviewFindingPayload{
		Severity: SeverityInfo,
		Summary:  "pass-1 info",
	})
	tr.Append(KindReviewFinding, "reviewer", StateOutputReview, "output_review", 2, ReviewFindingPayload{
		Severity: SeverityWarn,
		Summary:  "pass-2 warn",
	})

	// Query pass 1.
	pass1 := tr.ByKindAndPass(KindReviewFinding, "output_review", 1)
	if len(pass1) != 2 {
		t.Fatalf("ByKindAndPass(pass=1) returned %d, want 2", len(pass1))
	}

	// Query pass 2.
	pass2 := tr.ByKindAndPass(KindReviewFinding, "output_review", 2)
	if len(pass2) != 1 {
		t.Fatalf("ByKindAndPass(pass=2) returned %d, want 1", len(pass2))
	}

	// Query non-existent pass.
	pass3 := tr.ByKindAndPass(KindReviewFinding, "output_review", 3)
	if len(pass3) != 0 {
		t.Errorf("ByKindAndPass(pass=3) returned %d, want 0", len(pass3))
	}

	// Query wrong phase.
	wrong := tr.ByKindAndPass(KindReviewFinding, "plan_review", 1)
	if len(wrong) != 0 {
		t.Errorf("ByKindAndPass(plan_review, 1) returned %d, want 0", len(wrong))
	}
}

func TestTranscriptPhasePassFields(t *testing.T) {
	tr := NewTranscript()

	e := tr.Append(KindArtifact, "agent", StateExecute, "execute", 0, "payload")
	if e.Phase != "execute" {
		t.Errorf("Phase = %q, want %q", e.Phase, "execute")
	}
	if e.Pass != 0 {
		t.Errorf("Pass = %d, want 0", e.Pass)
	}

	e2 := tr.Append(KindReviewFinding, "reviewer", StateOutputReview, "output_review", 2, "finding")
	if e2.Phase != "output_review" {
		t.Errorf("Phase = %q, want %q", e2.Phase, "output_review")
	}
	if e2.Pass != 2 {
		t.Errorf("Pass = %d, want 2", e2.Pass)
	}
}

func TestTranscriptLen(t *testing.T) {
	tr := NewTranscript()
	if tr.Len() != 0 {
		t.Errorf("empty transcript Len() = %d, want 0", tr.Len())
	}

	tr.Append(KindIntent, "", StateInit, "", 0, nil)
	tr.Append(KindPlan, "", StatePlan, "", 0, nil)

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
			tr.Append(KindArtifact, "agent", StateExecute, "", 0, n)
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
			_ = tr.ByKindAndPass(KindArtifact, "", 0)
			_ = tr.Len()
		}()
	}

	wg.Wait()

	if tr.Len() != 50 {
		t.Errorf("after concurrent appends, Len() = %d, want 50", tr.Len())
	}
}
