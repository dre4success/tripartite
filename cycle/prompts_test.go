package cycle

import (
	"testing"
	"github.com/dre4success/tripartite/adapter"
)

func TestParseReviewLineTags(t *testing.T) {
	tests := []struct {
		name             string
		line             string
		wantSeverity     Severity
		wantNeedsClarify bool
		wantRest         string
		wantOK           bool
	}{
		{
			name:             "severity_only",
			line:             "[warn] st-1: tighten rollback steps",
			wantSeverity:     SeverityWarn,
			wantNeedsClarify: false,
			wantRest:         "st-1: tighten rollback steps",
			wantOK:           true,
		},
		{
			name:             "severity_and_clarify",
			line:             "[blocker][clarify] clarification: which auth table is canonical?",
			wantSeverity:     SeverityBlocker,
			wantNeedsClarify: true,
			wantRest:         "clarification: which auth table is canonical?",
			wantOK:           true,
		},
		{
			name:             "unknown_tag_ignored",
			line:             "[info][foo] st-2: looks fine",
			wantSeverity:     SeverityInfo,
			wantNeedsClarify: false,
			wantRest:         "st-2: looks fine",
			wantOK:           true,
		},
		{
			name:   "missing_severity_rejected",
			line:   "[clarify] st-2: please clarify",
			wantOK: false,
		},
		{
			name:   "untagged_rejected",
			line:   "st-2: no severity tag",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sev, clarify, rest, ok := parseReviewLineTags(tt.line)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if sev != tt.wantSeverity {
				t.Fatalf("severity = %s, want %s", sev, tt.wantSeverity)
			}
			if clarify != tt.wantNeedsClarify {
				t.Fatalf("needsClarify = %v, want %v", clarify, tt.wantNeedsClarify)
			}
			if rest != tt.wantRest {
				t.Fatalf("rest = %q, want %q", rest, tt.wantRest)
			}
		})
	}
}

func TestParseReviewFindingsFromTextStructuredClarification(t *testing.T) {
	text := `
- [warn] st-1: add rollback tests
- [blocker][clarify] clarification: should we migrate legacy sessions before deploy?
- [info] general: looks good
`
	got := parseReviewFindingsFromText("claude", text)
	if len(got) != 3 {
		t.Fatalf("len(findings) = %d, want 3", len(got))
	}
	if got[1].Severity != SeverityBlocker {
		t.Fatalf("finding[1].Severity = %s, want %s", got[1].Severity, SeverityBlocker)
	}
	if !got[1].NeedsClarification {
		t.Fatal("finding[1].NeedsClarification = false, want true")
	}
	if got[1].ClarificationQuestion == "" {
		t.Fatal("finding[1].ClarificationQuestion should not be empty")
	}
}

func TestParsePlanFromResponses_Fallback(t *testing.T) {
	// Round 1 has a good plan.
	r1 := []adapter.Response{
		{
			ExitCode: 0,
			Content: "## Subtasks\n1. [agent] task one\n2. [agent] task two",
		},
	}
	// Round 2 is empty/failed.
	r2 := []adapter.Response{
		{
			ExitCode: 1, // failed
			Content:  "",
		},
	}

	rounds := [][]adapter.Response{r1, r2}
	plan := parsePlanFromResponses(rounds)

	if len(plan.Subtasks) != 2 {
		t.Errorf("expected 2 subtasks from fallback, got %d", len(plan.Subtasks))
	}
}

