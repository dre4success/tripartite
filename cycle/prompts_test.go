package cycle

import "testing"

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
