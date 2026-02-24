package router

import "testing"

func TestClassify(t *testing.T) {
	cfg := Config{DefaultAgent: "claude"}

	tests := []struct {
		name       string
		prompt     string
		wantIntent Intent
		wantAgent  string
	}{
		// Action verbs → delegate
		{name: "fix", prompt: "fix the auth bug", wantIntent: IntentDelegate, wantAgent: "claude"},
		{name: "write", prompt: "write a unit test", wantIntent: IntentDelegate, wantAgent: "claude"},
		{name: "refactor", prompt: "refactor the handler", wantIntent: IntentDelegate, wantAgent: "claude"},
		{name: "implement", prompt: "implement user login", wantIntent: IntentDelegate, wantAgent: "claude"},
		{name: "build", prompt: "build the docker image script", wantIntent: IntentDelegate, wantAgent: "claude"},
		{name: "add", prompt: "add error handling", wantIntent: IntentDelegate, wantAgent: "claude"},
		{name: "create", prompt: "create a new endpoint", wantIntent: IntentDelegate, wantAgent: "claude"},
		{name: "delete", prompt: "delete the unused code", wantIntent: IntentDelegate, wantAgent: "claude"},
		{name: "remove", prompt: "remove deprecated API", wantIntent: IntentDelegate, wantAgent: "claude"},
		{name: "update", prompt: "update the dependencies", wantIntent: IntentDelegate, wantAgent: "claude"},
		{name: "deploy", prompt: "deploy to staging", wantIntent: IntentDelegate, wantAgent: "claude"},
		{name: "configure", prompt: "configure the linter", wantIntent: IntentDelegate, wantAgent: "claude"},
		{name: "setup", prompt: "setup the CI pipeline", wantIntent: IntentDelegate, wantAgent: "claude"},
		{name: "run", prompt: "run the test suite", wantIntent: IntentDelegate, wantAgent: "claude"},
		{name: "execute", prompt: "execute the migration", wantIntent: IntentDelegate, wantAgent: "claude"},

		// Question marks → brainstorm
		{name: "question_mark", prompt: "should we use Redis?", wantIntent: IntentBrainstorm},
		{name: "question_mark_mid", prompt: "what is the best approach?", wantIntent: IntentBrainstorm},
		{name: "question_mark_action_verb", prompt: "fix this?", wantIntent: IntentDelegate, wantAgent: "claude"},

		// Analysis/question words → brainstorm
		{name: "compare", prompt: "compare REST vs gRPC", wantIntent: IntentBrainstorm},
		{name: "review", prompt: "review the architecture", wantIntent: IntentBrainstorm},
		{name: "explain", prompt: "explain goroutines", wantIntent: IntentBrainstorm},
		{name: "analyze", prompt: "analyze the performance", wantIntent: IntentBrainstorm},
		{name: "design", prompt: "design the API schema", wantIntent: IntentBrainstorm},
		{name: "propose", prompt: "propose a migration plan", wantIntent: IntentBrainstorm},
		{name: "evaluate", prompt: "evaluate the tradeoffs", wantIntent: IntentBrainstorm},
		{name: "what", prompt: "what does this function do", wantIntent: IntentBrainstorm},
		{name: "why", prompt: "why is this slow", wantIntent: IntentBrainstorm},
		{name: "how", prompt: "how does the cache work", wantIntent: IntentBrainstorm},
		{name: "should", prompt: "should we refactor", wantIntent: IntentBrainstorm},
		{name: "which", prompt: "which database to pick", wantIntent: IntentBrainstorm},

		// Case insensitivity
		{name: "uppercase_fix", prompt: "FIX the auth bug", wantIntent: IntentDelegate, wantAgent: "claude"},
		{name: "mixed_compare", prompt: "Compare REST vs gRPC", wantIntent: IntentBrainstorm},

		// Empty input
		{name: "empty", prompt: "", wantIntent: IntentBrainstorm},
		{name: "whitespace", prompt: "   ", wantIntent: IntentBrainstorm},

		// Fallback to brainstorm
		{name: "fallback_noun", prompt: "goroutines and channels", wantIntent: IntentBrainstorm},
		{name: "fallback_phrase", prompt: "the bug in main.go", wantIntent: IntentBrainstorm},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Classify(tt.prompt, cfg)
			if got.Intent != tt.wantIntent {
				t.Errorf("Classify(%q).Intent = %q, want %q (reason: %s)", tt.prompt, got.Intent, tt.wantIntent, got.Reason)
			}
			if got.Agent != tt.wantAgent {
				t.Errorf("Classify(%q).Agent = %q, want %q", tt.prompt, got.Agent, tt.wantAgent)
			}
			if got.Reason == "" {
				t.Errorf("Classify(%q).Reason is empty", tt.prompt)
			}
		})
	}
}

func TestClassifyCustomDefaultAgent(t *testing.T) {
	cfg := Config{DefaultAgent: "codex"}
	got := Classify("fix the bug", cfg)
	if got.Agent != "codex" {
		t.Errorf("expected agent %q, got %q", "codex", got.Agent)
	}
}
