package adapter

import "testing"

func TestParseApprovalLevel(t *testing.T) {
	cases := []struct {
		input string
		want  ApprovalLevel
		ok    bool
	}{
		{input: "", want: ApprovalEdit, ok: true},
		{input: "edit", want: ApprovalEdit, ok: true},
		{input: "read", want: ApprovalRead, ok: true},
		{input: "full", want: ApprovalFull, ok: true},
		{input: "READ", want: ApprovalRead, ok: true},
		{input: "invalid", ok: false},
	}

	for _, tc := range cases {
		got, err := ParseApprovalLevel(tc.input)
		if tc.ok && err != nil {
			t.Fatalf("ParseApprovalLevel(%q) unexpected error: %v", tc.input, err)
		}
		if !tc.ok && err == nil {
			t.Fatalf("ParseApprovalLevel(%q) expected error", tc.input)
		}
		if tc.ok && got != tc.want {
			t.Fatalf("ParseApprovalLevel(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestClaudeBuildCommandApproval(t *testing.T) {
	a := &Claude{}

	cmd := a.BuildCommand("x", ApprovalRead)
	requireArgsContainSequence(t, cmd.Args, "--permission-mode", "plan")

	cmd = a.BuildCommand("x", ApprovalEdit)
	requireArgsContainSequence(t, cmd.Args, "--permission-mode", "acceptEdits")

	cmd = a.BuildCommand("x", ApprovalFull)
	requireArgsContainSequence(t, cmd.Args, "--dangerously-skip-permissions")
}

func TestCodexBuildCommandApproval(t *testing.T) {
	a := &Codex{}

	cmd := a.BuildCommand("x", ApprovalRead)
	requireArgsContainSequence(t, cmd.Args, "--sandbox", "read-only")

	cmd = a.BuildCommand("x", ApprovalEdit)
	requireArgsContainSequence(t, cmd.Args, "--full-auto")

	cmd = a.BuildCommand("x", ApprovalFull)
	requireArgsContainSequence(t, cmd.Args, "--dangerously-bypass-approvals-and-sandbox")
}

func TestGeminiBuildCommandApproval(t *testing.T) {
	a := &Gemini{}

	cmd := a.BuildCommand("x", ApprovalRead)
	requireArgsContainSequence(t, cmd.Args, "--approval-mode", "plan")

	cmd = a.BuildCommand("x", ApprovalEdit)
	requireArgsContainSequence(t, cmd.Args, "--approval-mode", "auto_edit")

	cmd = a.BuildCommand("x", ApprovalFull)
	requireArgsContainSequence(t, cmd.Args, "--yolo")
}

func requireArgsContainSequence(t *testing.T, args []string, seq ...string) {
	t.Helper()
	if len(seq) == 0 {
		return
	}
	for i := 0; i <= len(args)-len(seq); i++ {
		match := true
		for j := range seq {
			if args[i+j] != seq[j] {
				match = false
				break
			}
		}
		if match {
			return
		}
	}
	t.Fatalf("args %v do not contain sequence %v", args, seq)
}
