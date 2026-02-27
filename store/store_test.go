package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dre4success/tripartite/agent"
)

func TestStoreDelegateWrites(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "tripartite-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	s, err := New(tempDir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// 1. Save Delegate Event
	ev := agent.Event{
		Type:      agent.EventText,
		Agent:     "test",
		Timestamp: time.Now(),
		Data:      "hello",
		Raw:       []byte(`{"raw":true}`),
	}
	if err := s.SaveDelegateEvent(ev); err != nil {
		t.Errorf("SaveDelegateEvent() error = %v", err)
	}

	// 2. Save Raw Line
	rawLine := []byte(`{"raw":true}`)
	if err := s.SaveDelegateRawLine(rawLine); err != nil {
		t.Errorf("SaveDelegateRawLine() error = %v", err)
	}

	// 3. Save Stderr Line
	stderrLine := []byte("warning message")
	if err := s.SaveDelegateStderrLine(stderrLine); err != nil {
		t.Errorf("SaveDelegateStderrLine() error = %v", err)
	}

	// 4. Save Delegate Workspace
	ws := DelegateWorkspace{
		Enabled:      true,
		TaskID:       "task-123",
		WorktreePath: "/tmp/foo",
		Branch:       "branch-foo",
	}
	if err := s.SaveDelegateWorkspace(ws); err != nil {
		t.Errorf("SaveDelegateWorkspace() error = %v", err)
	}

	// 5. Save Summary
	summary := DelegateSummary{
		Agent:      "test",
		Model:      "test-model",
		Prompt:     "do something",
		Sandbox:    "safe",
		Duration:   time.Second,
		EventCount: 1,
		Worktree:   ws,
	}
	if err := s.SaveDelegateSummary(summary); err != nil {
		t.Errorf("SaveDelegateSummary() error = %v", err)
	}

	// Verify files exist and have content
	filesToCheck := map[string]string{
		"events.normalized.jsonl": "hello",
		"events.raw.jsonl":        `{"raw":true}`,
		"stderr.log":              "warning message",
		"workspace.json":          "branch-foo",
		"summary.md":              "test-model",
	}

	for filename, expectedContent := range filesToCheck {
		path := filepath.Join(s.RunDir, filename)
		content, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("expected file %q not found or readable: %v", filename, err)
			continue
		}
		if !strings.Contains(string(content), expectedContent) {
			t.Errorf("file %q missing expected content %q, got: %s", filename, expectedContent, string(content))
		}
	}
}

func TestStorePartialDelegateWrites(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "tripartite-test-partial-*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	s, err := New(tempDir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// Write only a few raw lines to simulate an interrupted run
	rawLine1 := []byte(`{"raw":true,"step":1}`)
	if err := s.SaveDelegateRawLine(rawLine1); err != nil {
		t.Errorf("SaveDelegateRawLine() error = %v", err)
	}

	rawLine2 := []byte(`{"raw":true,"step":2}`)
	if err := s.SaveDelegateRawLine(rawLine2); err != nil {
		t.Errorf("SaveDelegateRawLine() error = %v", err)
	}

	// Verify the file was written and flushed correctly despite no summary
	path := filepath.Join(s.RunDir, "events.raw.jsonl")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected file events.raw.jsonl not found or readable: %v", err)
	}

	contentStr := string(content)
	if !strings.Contains(contentStr, "step\":1") || !strings.Contains(contentStr, "step\":2") {
		t.Errorf("file events.raw.jsonl missing expected content, got: %s", contentStr)
	}
}

func TestSaveMetaSessionSummaryIncludesCycleDecisionAction(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "tripartite-test-meta-*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	s, err := New(tempDir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	meta := RunMeta{
		Prompt:    "test prompt",
		Models:    []string{"claude", "codex", "gemini"},
		Timeout:   "1m0s",
		Timestamp: time.Now().Format(time.RFC3339),
		Mode:      "meta",
	}
	turns := []MetaSessionTurn{
		{
			Prompt:                "apply changes",
			Engine:                "cycle",
			CycleID:               "cycle-123",
			CycleState:            "DONE",
			DecisionAction:        "apply_worktree_branch_ff",
			DecisionActionSummary: "decision action: applied worktree branch \"feature/test\" via fast-forward merge",
			FinalText:             "recommendation text",
		},
	}

	if err := s.SaveMetaSessionSummary(meta, turns); err != nil {
		t.Fatalf("SaveMetaSessionSummary() error = %v", err)
	}

	path := filepath.Join(s.RunDir, "summary.md")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read summary.md: %v", err)
	}
	got := string(content)
	if !strings.Contains(got, "**Decision Action:** apply_worktree_branch_ff") {
		t.Fatalf("summary missing decision action line:\n%s", got)
	}
	if !strings.Contains(got, "**Decision Action Result:** decision action: applied worktree branch") {
		t.Fatalf("summary missing decision action result line:\n%s", got)
	}
}

func TestSaveMetaSessionSummaryIncludesDelegateDecisionAction(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "tripartite-test-meta-delegate-*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	s, err := New(tempDir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	meta := RunMeta{
		Models:    []string{"claude"},
		Timeout:   "1m0s",
		Timestamp: time.Now().Format(time.RFC3339),
		Mode:      "meta",
	}
	turns := []MetaSessionTurn{
		{
			Prompt:                "implement feature",
			Engine:                "delegate",
			Agent:                 "claude",
			DecisionAction:        "apply_worktree_branch_ff",
			DecisionActionSummary: "applied delegate worktree branch \"tripartite/t1/claude\" via fast-forward merge",
		},
	}
	if err := s.SaveMetaSessionSummary(meta, turns); err != nil {
		t.Fatalf("SaveMetaSessionSummary() error = %v", err)
	}
	content, err := os.ReadFile(filepath.Join(s.RunDir, "summary.md"))
	if err != nil {
		t.Fatalf("read summary.md: %v", err)
	}
	got := string(content)
	if !strings.Contains(got, "**Decision Action:** apply_worktree_branch_ff") {
		t.Fatalf("missing delegate decision action line:\n%s", got)
	}
	if !strings.Contains(got, "**Decision Action Result:** applied delegate worktree branch") {
		t.Fatalf("missing delegate decision action result line:\n%s", got)
	}
}

func TestSaveAndLoadMetaSessionState(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "tripartite-test-meta-state-*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	s, err := New(tempDir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	state := MetaSessionState{
		Turns: []MetaSessionTurn{
			{Prompt: "p1", Engine: "delegate", Agent: "codex", FinalText: "ok"},
		},
		AgentSessions: map[string]string{"codex": "thread-1"},
	}
	if err := s.SaveMetaSessionState(state); err != nil {
		t.Fatalf("SaveMetaSessionState() error = %v", err)
	}

	got, err := s.LoadMetaSessionState()
	if err != nil {
		t.Fatalf("LoadMetaSessionState() error = %v", err)
	}
	if len(got.Turns) != 1 || got.Turns[0].Prompt != "p1" {
		t.Fatalf("loaded turns = %+v, want one turn with prompt p1", got.Turns)
	}
	if got.AgentSessions["codex"] != "thread-1" {
		t.Fatalf("loaded session codex = %q, want %q", got.AgentSessions["codex"], "thread-1")
	}
}
