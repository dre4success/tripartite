package meta

import (
	"strings"
	"testing"
	"time"

	"github.com/dre4success/tripartite/cycle"
)

func TestLiveCycleUpdatePrinterDedupesElapsedOnlyChanges(t *testing.T) {
	p := &liveCycleUpdatePrinter{mode: LiveCycleVerbose}

	snap := &cycle.CycleStatus{
		State:             cycle.StatePlan,
		Phase:             "plan",
		Pass:              0,
		Elapsed:           2 * time.Second,
		CompletedSubtasks: 0,
		TotalSubtasks:     0,
		RevisionCount:     0,
		MaxRevisions:      3,
		PendingApprovals:  0,
		LastTranscript: cycle.TranscriptStatusSummary{
			LastKind:    cycle.KindIntent,
			LastAgent:   "coordinator",
			LastSummary: "plan a refactor",
		},
	}

	lines1 := p.Next(snap)
	if len(lines1) == 0 {
		t.Fatal("expected initial lines")
	}

	snap2 := *snap
	snap2.Elapsed = 5 * time.Second // should not produce new lines by itself
	lines2 := p.Next(&snap2)
	if len(lines2) != 0 {
		t.Fatalf("elapsed-only change emitted lines: %v", lines2)
	}
}

func TestLiveCycleUpdatePrinterEmitsBoardAndReviewChanges(t *testing.T) {
	p := &liveCycleUpdatePrinter{mode: LiveCycleVerbose}

	snap := &cycle.CycleStatus{
		State:             cycle.StateOutputReview,
		Phase:             "output_review",
		Pass:              1,
		CompletedSubtasks: 1,
		TotalSubtasks:     2,
		RevisionCount:     0,
		MaxRevisions:      3,
		CurrentReview: &cycle.ReviewPassStats{
			Phase:    "output_review",
			Pass:     1,
			Total:    1,
			Blockers: 1,
		},
		CurrentBoard: &cycle.PhaseBoardSummary{
			Phase: "output_review",
			Pass:  1,
			Items: []cycle.PhaseBoardItem{
				{Role: "reviewer", Agent: "gemini", Kind: cycle.KindReviewFinding, Summary: "[blocker] st-1: missing test"},
			},
		},
		LastTranscript: cycle.TranscriptStatusSummary{
			LastKind:    cycle.KindReviewFinding,
			LastAgent:   "gemini",
			LastSummary: "[blocker] st-1: missing test",
		},
	}

	lines1 := p.Next(snap)
	if len(lines1) < 3 {
		t.Fatalf("expected multiple lines (state/activity/review/board), got %v", lines1)
	}

	// Same snapshot should produce no lines.
	lines2 := p.Next(snap)
	if len(lines2) != 0 {
		t.Fatalf("duplicate snapshot emitted lines: %v", lines2)
	}

	// Change board summary and review stats.
	snapNext := *snap
	review := *snap.CurrentReview
	review.Total = 2
	review.Warns = 1
	snapNext.CurrentReview = &review
	board := *snap.CurrentBoard
	board.Items = append([]cycle.PhaseBoardItem(nil), snap.CurrentBoard.Items...)
	board.Items[0].Summary = "[warn] st-1: flaky test"
	snapNext.CurrentBoard = &board
	snapNext.LastTranscript = cycle.TranscriptStatusSummary{
		LastKind:    cycle.KindReviewFinding,
		LastAgent:   "claude",
		LastSummary: "[warn] st-1: flaky test",
	}

	lines3 := p.Next(&snapNext)
	if len(lines3) == 0 {
		t.Fatal("expected updates after board/review/activity change")
	}
}

func TestLiveCycleUpdatePrinterBoardTimelineIsIncrementalWithinSamePass(t *testing.T) {
	p := &liveCycleUpdatePrinter{mode: LiveCycleVerbose}

	base := &cycle.CycleStatus{
		State:             cycle.StateOutputReview,
		Phase:             "output_review",
		Pass:              2,
		CompletedSubtasks: 1,
		TotalSubtasks:     2,
		RevisionCount:     1,
		MaxRevisions:      3,
		CurrentBoard: &cycle.PhaseBoardSummary{
			Phase: "output_review",
			Pass:  2,
			Items: []cycle.PhaseBoardItem{
				{Role: "reviewer", Agent: "gemini", Kind: cycle.KindReviewFinding, Summary: "[blocker] st-1: missing test"},
				{Role: "planner", Agent: "claude", Kind: cycle.KindReviewFinding, Summary: "[warn] st-2: weak rollback"},
			},
		},
		LastTranscript: cycle.TranscriptStatusSummary{
			LastKind:    cycle.KindReviewFinding,
			LastAgent:   "gemini",
			LastSummary: "[blocker] st-1: missing test",
		},
	}

	first := p.Next(base)
	if len(first) == 0 {
		t.Fatal("expected initial output")
	}

	// Update only one board item in the same phase/pass and keep other signals identical.
	next := *base
	board := *base.CurrentBoard
	board.Items = append([]cycle.PhaseBoardItem(nil), base.CurrentBoard.Items...)
	board.Items[1].Summary = "[warn] st-2: rollback still unclear"
	next.CurrentBoard = &board
	second := p.Next(&next)
	if len(second) == 0 {
		t.Fatal("expected incremental board update")
	}

	var boardHeaderCount, boardItemCount int
	for _, line := range second {
		if strings.Contains(line, "[cycle][live] board ") {
			boardHeaderCount++
		}
		if strings.Contains(line, "[cycle][live]   [") {
			boardItemCount++
		}
	}
	if boardHeaderCount != 0 {
		t.Fatalf("same-pass board update should not reprint board header, got %d lines: %v", boardHeaderCount, second)
	}
	if boardItemCount != 1 {
		t.Fatalf("same-pass board update should emit exactly 1 changed item, got %d lines: %v", boardItemCount, second)
	}
}

func TestLiveCycleUpdatePrinterIncludesPendingApprovalAndClarificationHint(t *testing.T) {
	p := &liveCycleUpdatePrinter{mode: LiveCycleCompact}

	snap := &cycle.CycleStatus{
		State:                      cycle.StateAwaitApproval,
		Phase:                      "await_approval",
		Pass:                       0,
		PendingApprovals:           3,
		PendingPermissionApprovals: 1,
		PendingDecisionApprovals:   2,
		PendingClarifications:      2,
		MaxRevisions:               3,
	}

	lines := p.Next(snap)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "approvals=3") || !strings.Contains(joined, "permissions=1") || !strings.Contains(joined, "decisions=2") || !strings.Contains(joined, "clarifications=2") {
		t.Fatalf("expected pending counts in live output, got: %v", lines)
	}
	if !strings.Contains(joined, "/approve|/deny") || !strings.Contains(joined, "/clarify") {
		t.Fatalf("expected operator action hints in live output, got: %v", lines)
	}
}

func TestParseLiveCycleVerbosity(t *testing.T) {
	tests := []struct {
		in      string
		want    LiveCycleVerbosity
		wantErr bool
	}{
		{in: "", want: LiveCycleCompact},
		{in: "compact", want: LiveCycleCompact},
		{in: "verbose", want: LiveCycleVerbose},
		{in: "off", want: LiveCycleOff},
		{in: "VERBOSE", want: LiveCycleVerbose},
		{in: "nope", wantErr: true},
	}

	for _, tt := range tests {
		got, err := ParseLiveCycleVerbosity(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Fatalf("ParseLiveCycleVerbosity(%q): expected error", tt.in)
			}
			continue
		}
		if err != nil {
			t.Fatalf("ParseLiveCycleVerbosity(%q): unexpected error: %v", tt.in, err)
		}
		if got != tt.want {
			t.Fatalf("ParseLiveCycleVerbosity(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
