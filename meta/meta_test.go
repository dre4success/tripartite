package meta

import (
	"testing"

	"github.com/dre4success/tripartite/adapter"
	"github.com/dre4success/tripartite/orchestrator"
	"github.com/dre4success/tripartite/router"
)

func TestParseSlashCommand(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantCmd string
		wantArg string
	}{
		{name: "quit", input: "/quit", wantCmd: "quit", wantArg: ""},
		{name: "exit", input: "/exit", wantCmd: "exit", wantArg: ""},
		{name: "history", input: "/history", wantCmd: "history", wantArg: ""},
		{name: "help", input: "/help", wantCmd: "help", wantArg: ""},
		{name: "brainstorm_with_prompt", input: "/brainstorm explain goroutines", wantCmd: "brainstorm", wantArg: "explain goroutines"},
		{name: "delegate_with_agent", input: "/delegate claude fix the bug", wantCmd: "delegate", wantArg: "claude fix the bug"},
		{name: "delegate_no_agent", input: "/delegate fix the bug", wantCmd: "delegate", wantArg: "fix the bug"},
		{name: "normal_text", input: "fix the auth bug", wantCmd: "", wantArg: "fix the auth bug"},
		{name: "normal_question", input: "compare REST vs gRPC", wantCmd: "", wantArg: "compare REST vs gRPC"},
		{name: "case_insensitive", input: "/QUIT", wantCmd: "quit", wantArg: ""},
		{name: "brainstorm_no_arg", input: "/brainstorm", wantCmd: "brainstorm", wantArg: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, arg := parseSlashCommand(tt.input)
			if cmd != tt.wantCmd {
				t.Errorf("parseSlashCommand(%q) cmd = %q, want %q", tt.input, cmd, tt.wantCmd)
			}
			if arg != tt.wantArg {
				t.Errorf("parseSlashCommand(%q) arg = %q, want %q", tt.input, arg, tt.wantArg)
			}
		})
	}
}

func TestToOrchestratorHistory(t *testing.T) {
	t.Run("brainstorm turn", func(t *testing.T) {
		turns := []Turn{{
			Prompt: "explain goroutines",
			Route:  router.Result{Intent: router.IntentBrainstorm},
			Brainstorm: &BrainstormResult{
				Rounds: [][]adapter.Response{
					{{Model: "claude", Content: "goroutines are..."}},
					{{Model: "claude", Content: "review..."}},
				},
			},
		}}

		got := toOrchestratorHistory(turns)
		if len(got) != 1 {
			t.Fatalf("expected 1 turn, got %d", len(got))
		}
		if got[0].Prompt != "explain goroutines" {
			t.Errorf("prompt = %q, want %q", got[0].Prompt, "explain goroutines")
		}
		if len(got[0].Responses) != 2 {
			t.Errorf("responses rounds = %d, want 2", len(got[0].Responses))
		}
	})

	t.Run("delegate turn", func(t *testing.T) {
		turns := []Turn{{
			Prompt: "fix the bug",
			Route:  router.Result{Intent: router.IntentDelegate, Agent: "claude"},
			Delegate: &DelegateResult{
				Agent:     "claude",
				FinalText: "I fixed the bug by...",
			},
		}}

		got := toOrchestratorHistory(turns)
		if len(got) != 1 {
			t.Fatalf("expected 1 turn, got %d", len(got))
		}
		if len(got[0].Responses) != 1 {
			t.Fatalf("expected 1 round (synthetic), got %d", len(got[0].Responses))
		}
		if len(got[0].Responses[0]) != 1 {
			t.Fatalf("expected 1 response in synthetic round, got %d", len(got[0].Responses[0]))
		}
		resp := got[0].Responses[0][0]
		if resp.Model != "claude" {
			t.Errorf("model = %q, want %q", resp.Model, "claude")
		}
		if resp.Content != "I fixed the bug by..." {
			t.Errorf("content = %q, want %q", resp.Content, "I fixed the bug by...")
		}
	})

	t.Run("mixed history", func(t *testing.T) {
		turns := []Turn{
			{
				Prompt:     "explain X",
				Route:      router.Result{Intent: router.IntentBrainstorm},
				Brainstorm: &BrainstormResult{Rounds: [][]adapter.Response{{{Model: "claude", Content: "X is..."}}}},
			},
			{
				Prompt:   "fix Y",
				Route:    router.Result{Intent: router.IntentDelegate, Agent: "codex"},
				Delegate: &DelegateResult{Agent: "codex", FinalText: "fixed Y"},
			},
			{
				Prompt:     "review Z",
				Route:      router.Result{Intent: router.IntentBrainstorm},
				Brainstorm: &BrainstormResult{Rounds: [][]adapter.Response{{{Model: "gemini", Content: "Z looks..."}}}},
			},
		}

		got := toOrchestratorHistory(turns)
		if len(got) != 3 {
			t.Fatalf("expected 3 turns, got %d", len(got))
		}

		// Turn 1: brainstorm
		if got[0].Responses[0][0].Model != "claude" {
			t.Errorf("turn 1 model = %q, want claude", got[0].Responses[0][0].Model)
		}
		// Turn 2: delegate (synthetic)
		if got[1].Responses[0][0].Model != "codex" {
			t.Errorf("turn 2 model = %q, want codex", got[1].Responses[0][0].Model)
		}
		// Turn 3: brainstorm
		if got[2].Responses[0][0].Model != "gemini" {
			t.Errorf("turn 3 model = %q, want gemini", got[2].Responses[0][0].Model)
		}
	})

	t.Run("empty history", func(t *testing.T) {
		got := toOrchestratorHistory(nil)
		if len(got) != 0 {
			t.Errorf("expected 0 turns, got %d", len(got))
		}
	})
}

// Verify that our orchestrator.Turn type matches what we construct.
func TestOrchestratorTurnCompatibility(t *testing.T) {
	_ = orchestrator.Turn{
		Prompt:    "test",
		Responses: [][]adapter.Response{{{Model: "claude", Content: "hello"}}},
	}
}
