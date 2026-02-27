package cycle

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/dre4success/tripartite/store"
)

func TestSelectResumeCheckpoint(t *testing.T) {
	now := time.Now()
	cp := func(state State, offset time.Duration) store.CycleCheckpoint {
		return store.CycleCheckpoint{
			CycleID:    "cycle-test",
			State:      string(state),
			Timestamp:  now.Add(offset),
			EntryCount: 1,
		}
	}

	t.Run("latest safe checkpoint", func(t *testing.T) {
		got, state, note, err := selectResumeCheckpoint([]store.CycleCheckpoint{
			cp(StatePlanReview, 1*time.Second),
			cp(StateAwaitApproval, 2*time.Second),
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if state != StateAwaitApproval || got.State != string(StateAwaitApproval) {
			t.Fatalf("selected %s / %s, want %s", state, got.State, StateAwaitApproval)
		}
		if note != "" {
			t.Fatalf("note = %q, want empty", note)
		}
	})

	t.Run("done is not resumable", func(t *testing.T) {
		_, _, _, err := selectResumeCheckpoint([]store.CycleCheckpoint{
			cp(StateDecisionGate, 1*time.Second),
			cp(StateDone, 2*time.Second),
		})
		if err == nil {
			t.Fatal("expected error for DONE checkpoint")
		}
	})

	t.Run("aborted resumes from prior safe checkpoint", func(t *testing.T) {
		_, state, note, err := selectResumeCheckpoint([]store.CycleCheckpoint{
			cp(StateDecisionGate, 1*time.Second),
			cp(StateAwaitApproval, 2*time.Second),
			cp(StateAborted, 3*time.Second),
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if state != StateAwaitApproval {
			t.Fatalf("state = %s, want %s", state, StateAwaitApproval)
		}
		if note == "" {
			t.Fatal("expected resume note for ABORTED fallback")
		}
	})

	t.Run("aborted after unsafe state is rejected", func(t *testing.T) {
		_, _, _, err := selectResumeCheckpoint([]store.CycleCheckpoint{
			cp(StatePlanReview, 1*time.Second),
			cp(StateExecute, 2*time.Second),
			cp(StateAborted, 3*time.Second),
		})
		if err == nil {
			t.Fatal("expected error for unsafe resume fallback")
		}
	})
}

func TestDecodeTranscriptJSON(t *testing.T) {
	now := time.Now().UTC()
	entries := []Entry{
		{
			ID:        1,
			Kind:      KindIntent,
			Timestamp: now,
			Agent:     "coordinator",
			State:     StateIntake,
			Phase:     "intake",
			Payload: IntentPayload{
				RawPrompt:      "test",
				NormalizedGoal: "test goal",
				TaskType:       TaskHybrid,
				Roles:          RoleMap{Planner: "claude"},
			},
		},
		{
			ID:        2,
			Kind:      KindDecision,
			Timestamp: now.Add(time.Second),
			Agent:     "coordinator",
			State:     StateDecisionGate,
			Phase:     "decision_gate",
			Payload: DecisionPayload{
				Recommendation: "Ship it",
				Tradeoffs:      []string{"warn"},
				Note:           "operator note",
				Actions:        []string{decisionActionAcceptResult},
			},
		},
		{
			ID:        3,
			Kind:      KindApprovalRequest,
			Timestamp: now.Add(2 * time.Second),
			Agent:     "coordinator",
			State:     StateAwaitApproval,
			Phase:     "await_approval",
			Payload: ApprovalRequestPayload{
				TicketID:    "tk-1",
				Reason:      "Decision required",
				Scope:       ApprovalScopeDecisionGate,
				ResumeState: StateDone,
			},
		},
		{
			ID:        4,
			Kind:      KindDecisionAction,
			Timestamp: now.Add(3 * time.Second),
			Agent:     "coordinator",
			State:     StateAwaitApproval,
			Phase:     "await_approval",
			Payload: DecisionActionPayload{
				Action:    decisionActionAcceptResult,
				Succeeded: true,
				Summary:   "decision action: accepted cycle result without applying changes",
			},
		},
	}

	data, err := json.Marshal(entries)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	tr, err := decodeTranscriptJSON(data)
	if err != nil {
		t.Fatalf("decodeTranscriptJSON: %v", err)
	}

	if tr.Len() != len(entries) {
		t.Fatalf("Len = %d, want %d", tr.Len(), len(entries))
	}
	dec := tr.Last(KindDecision)
	if dec == nil {
		t.Fatal("missing decision entry")
	}
	dp, ok := dec.Payload.(DecisionPayload)
	if !ok {
		t.Fatalf("decision payload type = %T", dec.Payload)
	}
	if dp.Note != "operator note" {
		t.Fatalf("decision note = %q, want %q", dp.Note, "operator note")
	}
	if len(dp.Actions) != 1 || dp.Actions[0] != decisionActionAcceptResult {
		t.Fatalf("decision actions = %#v", dp.Actions)
	}
	act := tr.Last(KindDecisionAction)
	if act == nil {
		t.Fatal("missing decision_action entry")
	}
	ap, ok := act.Payload.(DecisionActionPayload)
	if !ok {
		t.Fatalf("decision_action payload type = %T", act.Payload)
	}
	if ap.Action != decisionActionAcceptResult || !ap.Succeeded {
		t.Fatalf("decision_action payload = %#v", ap)
	}
}

func TestRestoreDerivedStateFromTranscriptBackfillsPrompt(t *testing.T) {
	cc := newCycleContext(Config{})
	cc.transcript.Append(KindIntent, "coordinator", StateIntake, phaseName(StateIntake), 0, IntentPayload{
		RawPrompt:      "implement auth migration",
		NormalizedGoal: "implement auth migration",
		TaskType:       TaskCodeChange,
	})

	cc.restoreDerivedStateFromTranscript()

	if cc.cfg.Prompt != "implement auth migration" {
		t.Fatalf("cfg.Prompt = %q, want %q", cc.cfg.Prompt, "implement auth migration")
	}
	if cc.intent == nil || cc.intent.RawPrompt != "implement auth migration" {
		t.Fatalf("intent not restored correctly: %#v", cc.intent)
	}
}
