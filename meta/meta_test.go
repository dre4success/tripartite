package meta

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dre4success/tripartite/adapter"
	"github.com/dre4success/tripartite/agent"
	"github.com/dre4success/tripartite/cycle"
	"github.com/dre4success/tripartite/orchestrator"
	"github.com/dre4success/tripartite/router"
	"github.com/dre4success/tripartite/store"
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
		{name: "board", input: "/board", wantCmd: "board", wantArg: ""},
		{name: "resume", input: "/resume", wantCmd: "resume", wantArg: ""},
		{name: "resume_with_arg", input: "/resume 3", wantCmd: "resume", wantArg: "3"},
		{name: "clarify", input: "/clarify answer", wantCmd: "clarify", wantArg: "answer"},
		{name: "clarify_with_ticket", input: "/clarify cq-123 answer", wantCmd: "clarify", wantArg: "cq-123 answer"},
		{name: "timeline", input: "/timeline", wantCmd: "timeline", wantArg: ""},
		{name: "timeline_with_arg", input: "/timeline 5", wantCmd: "timeline", wantArg: "5"},
		{name: "live_with_arg", input: "/live verbose", wantCmd: "live", wantArg: "verbose"},
		{name: "live_no_arg", input: "/live", wantCmd: "live", wantArg: ""},
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

func TestParseResumeTurnArg(t *testing.T) {
	tests := []struct {
		name    string
		arg     string
		want    int
		wantErr bool
	}{
		{name: "empty_uses_latest", arg: "", want: 0},
		{name: "whitespace_uses_latest", arg: "   ", want: 0},
		{name: "positive_turn", arg: "4", want: 4},
		{name: "zero_invalid", arg: "0", wantErr: true},
		{name: "negative_invalid", arg: "-1", wantErr: true},
		{name: "non_numeric_invalid", arg: "abc", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseResumeTurnArg(tt.arg)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (value=%d)", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("value = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestParseClarifyArg(t *testing.T) {
	tests := []struct {
		name       string
		arg        string
		wantTicket string
		wantAnswer string
		wantErr    bool
	}{
		{name: "answer_only", arg: "please scope to auth module", wantTicket: "", wantAnswer: "please scope to auth module"},
		{name: "ticket_and_answer", arg: "cq-1234 include migration notes", wantTicket: "cq-1234", wantAnswer: "include migration notes"},
		{name: "ticket_without_answer_invalid", arg: "cq-1234", wantErr: true},
		{name: "empty_invalid", arg: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotTicket, gotAnswer, err := parseClarifyArg(tt.arg)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (ticket=%q answer=%q)", gotTicket, gotAnswer)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotTicket != tt.wantTicket {
				t.Fatalf("ticket = %q, want %q", gotTicket, tt.wantTicket)
			}
			if gotAnswer != tt.wantAnswer {
				t.Fatalf("answer = %q, want %q", gotAnswer, tt.wantAnswer)
			}
		})
	}
}

func TestResolveApprovalTicket(t *testing.T) {
	pending := []*cycle.PendingApproval{
		{TicketID: "tk-1"},
		{TicketID: "tk-2"},
	}

	t.Run("explicit ticket wins", func(t *testing.T) {
		got, err := resolveApprovalTicket("tk-99", pending)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "tk-99" {
			t.Fatalf("ticket = %q, want %q", got, "tk-99")
		}
	})

	t.Run("single pending auto-selects", func(t *testing.T) {
		got, err := resolveApprovalTicket("", pending[:1])
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "tk-1" {
			t.Fatalf("ticket = %q, want %q", got, "tk-1")
		}
	})

	t.Run("none pending errors", func(t *testing.T) {
		_, err := resolveApprovalTicket("", nil)
		if err == nil || !strings.Contains(err.Error(), "no pending approvals") {
			t.Fatalf("err = %v, want no pending approvals", err)
		}
	})

	t.Run("multiple pending requires ticket", func(t *testing.T) {
		_, err := resolveApprovalTicket("", pending)
		if err == nil || !strings.Contains(err.Error(), "multiple pending approvals") {
			t.Fatalf("err = %v, want multiple pending approvals", err)
		}
	})
}

func TestResolveClarificationTicket(t *testing.T) {
	pending := []*cycle.PendingClarification{
		{TicketID: "cq-1"},
		{TicketID: "cq-2"},
	}

	t.Run("explicit ticket wins", func(t *testing.T) {
		got, err := resolveClarificationTicket("cq-9", pending)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "cq-9" {
			t.Fatalf("ticket = %q, want %q", got, "cq-9")
		}
	})

	t.Run("single pending auto-selects", func(t *testing.T) {
		got, err := resolveClarificationTicket("", pending[:1])
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "cq-1" {
			t.Fatalf("ticket = %q, want %q", got, "cq-1")
		}
	})

	t.Run("none pending errors", func(t *testing.T) {
		_, err := resolveClarificationTicket("", nil)
		if err == nil || !strings.Contains(err.Error(), "no pending clarifications") {
			t.Fatalf("err = %v, want no pending clarifications", err)
		}
	})

	t.Run("multiple pending requires ticket", func(t *testing.T) {
		_, err := resolveClarificationTicket("", pending)
		if err == nil || !strings.Contains(err.Error(), "multiple pending clarifications") {
			t.Fatalf("err = %v, want multiple pending clarifications", err)
		}
	})
}

func TestAdjustRouteForAvailability(t *testing.T) {
	tests := []struct {
		name         string
		in           router.Result
		defaultAgent string
		adapters     []string
		agents       []string
		wantIntent   router.Intent
		wantAgent    string
		wantReason   string
	}{
		{
			name:         "delegate_falls_back_to_brainstorm_when_no_agents",
			in:           router.Result{Intent: router.IntentDelegate, Agent: "claude", Reason: "action verb: fix"},
			defaultAgent: "claude",
			adapters:     []string{"claude", "gemini"},
			agents:       nil,
			wantIntent:   router.IntentBrainstorm,
			wantAgent:    "",
			wantReason:   "fallback to brainstorm",
		},
		{
			name:         "brainstorm_falls_back_to_delegate_when_no_adapters",
			in:           router.Result{Intent: router.IntentBrainstorm, Reason: "contains question mark"},
			defaultAgent: "claude",
			adapters:     nil,
			agents:       []string{"codex"},
			wantIntent:   router.IntentDelegate,
			wantAgent:    "codex",
			wantReason:   "fallback to delegate",
		},
		{
			name:         "delegate_reselects_available_default_agent",
			in:           router.Result{Intent: router.IntentDelegate, Agent: "claude", Reason: "action verb: fix"},
			defaultAgent: "gemini",
			adapters:     []string{"claude"},
			agents:       []string{"gemini", "codex"},
			wantIntent:   router.IntentDelegate,
			wantAgent:    "gemini",
			wantReason:   "selected available agent",
		},
		{
			name:         "delegate_keeps_existing_available_agent",
			in:           router.Result{Intent: router.IntentDelegate, Agent: "codex", Reason: "action verb: fix"},
			defaultAgent: "claude",
			adapters:     []string{"claude"},
			agents:       []string{"codex"},
			wantIntent:   router.IntentDelegate,
			wantAgent:    "codex",
		},
		{
			name:         "brainstorm_stays_brainstorm_when_adapters_ready",
			in:           router.Result{Intent: router.IntentBrainstorm, Reason: "analysis/question word: how"},
			defaultAgent: "claude",
			adapters:     []string{"claude"},
			agents:       nil,
			wantIntent:   router.IntentBrainstorm,
			wantAgent:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := adjustRouteForAvailability(tt.in, tt.defaultAgent, tt.adapters, tt.agents)
			if got.Intent != tt.wantIntent {
				t.Fatalf("intent = %q, want %q (reason: %s)", got.Intent, tt.wantIntent, got.Reason)
			}
			if got.Agent != tt.wantAgent {
				t.Fatalf("agent = %q, want %q", got.Agent, tt.wantAgent)
			}
			if tt.wantReason != "" && !strings.Contains(got.Reason, tt.wantReason) {
				t.Fatalf("reason = %q, want substring %q", got.Reason, tt.wantReason)
			}
		})
	}
}

func TestParseDelegateArg(t *testing.T) {
	cfg := Config{DefaultAgent: "claude"}

	tests := []struct {
		name              string
		arg               string
		wantAgent         string
		wantPrompt        string
		wantExplicitAgent bool
	}{
		{
			name:              "explicit_known_agent_with_prompt",
			arg:               "gemini fix the bug",
			wantAgent:         "gemini",
			wantPrompt:        "fix the bug",
			wantExplicitAgent: true,
		},
		{
			name:              "explicit_known_agent_no_prompt",
			arg:               "codex",
			wantAgent:         "codex",
			wantPrompt:        "",
			wantExplicitAgent: true,
		},
		{
			name:              "unknown_word_is_prompt_uses_default_agent",
			arg:               "fix the bug",
			wantAgent:         "claude",
			wantPrompt:        "fix the bug",
			wantExplicitAgent: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agentName, prompt, explicit := parseDelegateArg(tt.arg, cfg)
			if agentName != tt.wantAgent {
				t.Fatalf("agent = %q, want %q", agentName, tt.wantAgent)
			}
			if prompt != tt.wantPrompt {
				t.Fatalf("prompt = %q, want %q", prompt, tt.wantPrompt)
			}
			if explicit != tt.wantExplicitAgent {
				t.Fatalf("explicit = %v, want %v", explicit, tt.wantExplicitAgent)
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

func TestStoreMetaSessionTurnRoundTrip(t *testing.T) {
	in := []Turn{
		{
			Prompt: "brainstorm prompt",
			Brainstorm: &BrainstormResult{
				Rounds: [][]adapter.Response{
					{{Model: "claude", Content: "a"}},
				},
			},
		},
		{
			Prompt: "delegate prompt",
			Delegate: &DelegateResult{
				Agent:                 "claude",
				FinalText:             "done",
				DecisionAction:        delegateDecisionActionKeep,
				DecisionActionSummary: "kept",
				DecisionActionError:   "",
			},
		},
	}
	storeTurns := toStoreMetaSessionTurns(in)
	out := fromStoreMetaSessionTurns(storeTurns)
	if len(out) != len(in) {
		t.Fatalf("turns len = %d, want %d", len(out), len(in))
	}
	if out[0].Brainstorm == nil || len(out[0].Brainstorm.Rounds) != 1 {
		t.Fatal("brainstorm turn did not round-trip")
	}
	if out[1].Delegate == nil || out[1].Delegate.DecisionAction != delegateDecisionActionKeep {
		t.Fatal("delegate decision did not round-trip")
	}
}

func TestCheckpointMetaSessionState(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "tripartite-meta-state-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	s, err := store.New(tempDir)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}

	cfg := Config{
		Store:   s,
		Timeout: time.Minute,
	}
	turns := []Turn{{
		Prompt: "delegate",
		Delegate: &DelegateResult{
			Agent:     "codex",
			FinalText: "ok",
		},
	}}
	sessions := map[string]string{"codex": "thread-1"}
	checkpointMetaSessionState(cfg, turns, sessions)

	path := filepath.Join(s.RunDir, "meta_session_state.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected %s: %v", path, err)
	}
	state, err := s.LoadMetaSessionState()
	if err != nil {
		t.Fatalf("LoadMetaSessionState: %v", err)
	}
	if len(state.Turns) != 1 {
		t.Fatalf("state turns = %d, want 1", len(state.Turns))
	}
	if got := state.AgentSessions["codex"]; got != "thread-1" {
		t.Fatalf("agent session codex = %q, want %q", got, "thread-1")
	}
}

func TestUpdateAgentSession(t *testing.T) {
	sessions := map[string]string{}
	updateAgentSession(sessions, &Turn{
		Delegate: &DelegateResult{
			Agent:     "codex",
			SessionID: "thread-123",
		},
	})
	if got := sessions["codex"]; got != "thread-123" {
		t.Fatalf("session codex = %q, want %q", got, "thread-123")
	}
}

func TestQueueDelegateDecisionTicket(t *testing.T) {
	broker := cycle.NewApprovalBroker()
	turn := Turn{
		Delegate: &DelegateResult{
			Agent:    "claude",
			RepoRoot: "/tmp/repo",
			Worktree: store.DelegateWorkspace{
				Enabled: true,
				Branch:  "tripartite/test/claude",
				Commits: []store.DelegateCommit{
					{SHA: "abc"},
				},
			},
		},
	}
	ticketID, pd, ok := queueDelegateDecisionTicket(broker, turn, 2)
	if !ok {
		t.Fatal("expected decision ticket")
	}
	if ticketID == "" {
		t.Fatal("ticket id should not be empty")
	}
	if pd.TurnIndex != 2 {
		t.Fatalf("turn index = %d, want 2", pd.TurnIndex)
	}
	if pd.Branch != "tripartite/test/claude" {
		t.Fatalf("branch = %q, want %q", pd.Branch, "tripartite/test/claude")
	}
	pending := broker.Pending()
	if len(pending) != 1 {
		t.Fatalf("pending = %d, want 1", len(pending))
	}
	if pending[0].Kind != cycle.ApprovalKindDecision {
		t.Fatalf("ticket kind = %q, want %q", pending[0].Kind, cycle.ApprovalKindDecision)
	}
}

func TestQueueDelegateDecisionTicketNoCommits(t *testing.T) {
	broker := cycle.NewApprovalBroker()
	turn := Turn{
		Delegate: &DelegateResult{
			RepoRoot: "/tmp/repo",
			Worktree: store.DelegateWorkspace{
				Enabled: true,
				Branch:  "tripartite/test/claude",
			},
		},
	}
	ticketID, _, ok := queueDelegateDecisionTicket(broker, turn, 1)
	if ok || ticketID != "" {
		t.Fatalf("expected no ticket, got ok=%v ticket=%q", ok, ticketID)
	}
}

func TestApplyDelegateDecision(t *testing.T) {
	t.Run("deny keeps proposal", func(t *testing.T) {
		action, summary, errMsg := applyDelegateDecision(context.TODO(), false, pendingDelegateDecision{})
		if action != delegateDecisionActionKeep {
			t.Fatalf("action = %q, want %q", action, delegateDecisionActionKeep)
		}
		if summary == "" {
			t.Fatal("expected summary for deny action")
		}
		if errMsg != "" {
			t.Fatalf("errMsg = %q, want empty", errMsg)
		}
	})

	t.Run("approve merge failure reports error", func(t *testing.T) {
		action, _, errMsg := applyDelegateDecision(context.TODO(), true, pendingDelegateDecision{
			RepoRoot: "/tmp/not-a-repo",
			Branch:   "tripartite/test/claude",
		})
		if action != delegateDecisionActionApplyFF {
			t.Fatalf("action = %q, want %q", action, delegateDecisionActionApplyFF)
		}
		if errMsg == "" {
			t.Fatal("expected merge error")
		}
	})
}

func TestRequiresDelegateLaunchApproval(t *testing.T) {
	tests := []struct {
		sandbox string
		want    bool
	}{
		{sandbox: "safe", want: false},
		{sandbox: "write", want: true},
		{sandbox: "full", want: true},
		{sandbox: "WRITE", want: true},
	}
	for _, tt := range tests {
		if got := requiresDelegateLaunchApproval(tt.sandbox); got != tt.want {
			t.Fatalf("requiresDelegateLaunchApproval(%q) = %v, want %v", tt.sandbox, got, tt.want)
		}
	}
}

func TestEnforceOneShotDelegateLaunchPolicy(t *testing.T) {
	delegateRoute := router.Result{Intent: router.IntentDelegate}
	brainstormRoute := router.Result{Intent: router.IntentBrainstorm}

	if err := enforceOneShotDelegateLaunchPolicy(brainstormRoute, "full"); err != nil {
		t.Fatalf("brainstorm route should not require launch gate: %v", err)
	}
	if err := enforceOneShotDelegateLaunchPolicy(delegateRoute, "safe"); err != nil {
		t.Fatalf("safe sandbox should not require launch gate: %v", err)
	}
	if err := enforceOneShotDelegateLaunchPolicy(delegateRoute, "write"); err == nil {
		t.Fatal("expected launch gate error for write sandbox in one-shot delegate")
	}
	if err := enforceOneShotDelegateLaunchPolicy(delegateRoute, "FULL"); err == nil {
		t.Fatal("expected launch gate error for full sandbox in one-shot delegate")
	}
}

func TestRequestDelegateLaunchApproval(t *testing.T) {
	broker := cycle.NewApprovalBroker()
	route := router.Result{Intent: router.IntentDelegate, Agent: "claude", Reason: "forced"}
	ticketID, launch := requestDelegateLaunchApproval(broker, "fix auth bug", route, "write")
	if ticketID == "" {
		t.Fatal("expected ticket id")
	}
	if launch.Route.Agent != "claude" {
		t.Fatalf("launch route agent = %q, want %q", launch.Route.Agent, "claude")
	}
	pending := broker.Pending()
	if len(pending) != 1 {
		t.Fatalf("pending approvals = %d, want 1", len(pending))
	}
	if pending[0].Kind != cycle.ApprovalKindPermission {
		t.Fatalf("approval kind = %q, want %q", pending[0].Kind, cycle.ApprovalKindPermission)
	}
}

func TestBuildSessionMemorySnippet(t *testing.T) {
	history := []Turn{
		{Prompt: "p1", Brainstorm: &BrainstormResult{Rounds: [][]adapter.Response{{{Model: "claude", Content: "brainstorm summary"}}}}},
		{Prompt: "p2", Delegate: &DelegateResult{Agent: "claude", FinalText: "implemented change"}},
		{Prompt: "p3", Cycle: &CycleResult{Recommendation: "ship this", FinalState: "DONE"}},
	}
	got := buildSessionMemorySnippet(history, 3, 500)
	if !strings.Contains(got, "brainstorm:") || !strings.Contains(got, "delegate(claude):") || !strings.Contains(got, "cycle:") {
		t.Fatalf("memory snippet missing expected summaries:\n%s", got)
	}
}

func TestBuildDelegatePrompt(t *testing.T) {
	history := []Turn{
		{
			Prompt:   "do X",
			Delegate: &DelegateResult{Agent: "claude", FinalText: "implemented X"},
		},
	}
	a := &agentStub{name: "stub", continuation: nil}
	got := buildDelegatePrompt("now do Y", history, "", a)
	if !strings.Contains(got, "Session context") || !strings.Contains(got, "Current task:") || !strings.Contains(got, "now do Y") {
		t.Fatalf("unexpected delegate prompt:\n%s", got)
	}

	aNative := &agentStub{name: "native", continuation: []string{"--resume", "sid"}}
	gotNative := buildDelegatePrompt("now do Y", history, "sid-1", aNative)
	if gotNative != "now do Y" {
		t.Fatalf("expected native continuation prompt to remain unchanged, got:\n%s", gotNative)
	}
}

type agentStub struct {
	name         string
	continuation []string
}

func (a *agentStub) Name() string                                     { return a.name }
func (a *agentStub) BinaryName() string                               { return a.name }
func (a *agentStub) CheckInstalled() error                            { return nil }
func (a *agentStub) SupportedModels() []string                        { return nil }
func (a *agentStub) DefaultModel() string                             { return "" }
func (a *agentStub) PromptMode() agent.PromptMode                     { return agent.PromptArg }
func (a *agentStub) ContinuationArgs(_ string) []string               { return a.continuation }
func (a *agentStub) StreamCommand(string, agent.StreamOpts) *exec.Cmd { return nil }
func (a *agentStub) ParseEvent([]byte) (agent.Event, error)           { return agent.Event{}, nil }
func (a *agentStub) BlockedEnvVars() []string                         { return nil }

// Verify that our orchestrator.Turn type matches what we construct.
func TestOrchestratorTurnCompatibility(t *testing.T) {
	_ = orchestrator.Turn{
		Prompt:    "test",
		Responses: [][]adapter.Response{{{Model: "claude", Content: "hello"}}},
	}
}
