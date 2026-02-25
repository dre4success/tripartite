package cycle

import (
	"sync"
	"testing"
	"time"
)

func TestStatusProviderConcurrency(t *testing.T) {
	sp := NewStatusProvider()
	var wg sync.WaitGroup

	// 1 writer pushing updates.
	wg.Go(func() {
		for i := range 100 {
			sp.Update(CycleStatus{
				CycleID:       "cycle-test",
				State:         StateExecute,
				Phase:         "execute",
				Pass:          i,
				TranscriptLen: i,
				RetryCount:    map[string]int{"st-1": i},
				Subtasks: []SubtaskStatus{
					{ID: "st-1", Completed: i > 50},
				},
			})
		}
	})

	// 10 concurrent readers.
	for range 10 {
		wg.Go(func() {
			for range 50 {
				snap := sp.Snapshot()
				if snap != nil {
					_ = snap.CycleID
					_ = snap.Phase
					_ = snap.RetryCount
					_ = snap.Subtasks
				}
			}
		})
	}

	wg.Wait()

	// Final snapshot should exist.
	snap := sp.Snapshot()
	if snap == nil {
		t.Fatal("expected non-nil snapshot after updates")
	}
	if snap.CycleID != "cycle-test" {
		t.Errorf("CycleID = %q, want %q", snap.CycleID, "cycle-test")
	}
}

func TestStatusProviderSnapshotCopyIsolation(t *testing.T) {
	sp := NewStatusProvider()
	sp.Update(CycleStatus{
		CycleID:    "cycle-iso",
		State:      StatePlan,
		RetryCount: map[string]int{"st-1": 1},
		Subtasks: []SubtaskStatus{
			{ID: "st-1", Completed: false},
		},
	})

	snap := sp.Snapshot()

	// Mutate the snapshot.
	snap.CycleID = "mutated"
	snap.RetryCount["st-1"] = 99
	snap.Subtasks[0].Completed = true

	// Original should be unaffected.
	original := sp.Snapshot()
	if original.CycleID != "cycle-iso" {
		t.Errorf("mutation leaked: CycleID = %q", original.CycleID)
	}
	if original.RetryCount["st-1"] != 1 {
		t.Errorf("mutation leaked: RetryCount[st-1] = %d", original.RetryCount["st-1"])
	}
	if original.Subtasks[0].Completed {
		t.Error("mutation leaked: Subtasks[0].Completed = true")
	}
}

func TestStatusProviderClear(t *testing.T) {
	sp := NewStatusProvider()

	// Initially nil.
	if sp.Snapshot() != nil {
		t.Fatal("expected nil snapshot before any update")
	}

	sp.Update(CycleStatus{
		CycleID:   "cycle-clear",
		State:     StateDone,
		StartedAt: time.Now(),
	})
	if sp.Snapshot() == nil {
		t.Fatal("expected non-nil snapshot after update")
	}

	sp.Clear()
	if sp.Snapshot() != nil {
		t.Fatal("expected nil snapshot after Clear()")
	}
}

func TestStatusProviderUpdateCopiesInput(t *testing.T) {
	sp := NewStatusProvider()

	retries := map[string]int{"st-1": 1}
	subtasks := []SubtaskStatus{{ID: "st-1", Completed: false}}
	sp.Update(CycleStatus{
		CycleID:    "cycle-copy",
		State:      StateExecute,
		RetryCount: retries,
		Subtasks:   subtasks,
	})

	// Mutate the caller-owned inputs after Update.
	retries["st-1"] = 9
	subtasks[0].Completed = true

	snap := sp.Snapshot()
	if snap == nil {
		t.Fatal("expected non-nil snapshot")
	}
	if got := snap.RetryCount["st-1"]; got != 1 {
		t.Errorf("RetryCount alias leaked from Update: got %d, want 1", got)
	}
	if snap.Subtasks[0].Completed {
		t.Error("Subtasks alias leaked from Update: Completed = true, want false")
	}
}
