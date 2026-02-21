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
