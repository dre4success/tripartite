package adapter

import (
	"strings"
	"testing"
)

func TestCodexParseResponseAgentMessage(t *testing.T) {
	c := &Codex{}
	out := strings.Join([]string{
		`{"type":"thread.started","thread_id":"t1"}`,
		`{"type":"item.completed","item":{"type":"agent_message","content":"hello from codex"}}`,
		`{"type":"turn.completed"}`,
	}, "\n")

	got, err := c.ParseResponse([]byte(out))
	if err != nil {
		t.Fatalf("ParseResponse() error = %v", err)
	}
	if got != "hello from codex" {
		t.Fatalf("unexpected content: %q", got)
	}
}

func TestCodexParseResponseMultipleMessages(t *testing.T) {
	c := &Codex{}
	out := strings.Join([]string{
		`{"type":"item.completed","item":{"type":"agent_message","content":"line1"}}`,
		`{"type":"item.completed","item":{"type":"agent_message","content":"line2"}}`,
	}, "\n")

	got, err := c.ParseResponse([]byte(out))
	if err != nil {
		t.Fatalf("ParseResponse() error = %v", err)
	}
	if got != "line1\n\nline2" {
		t.Fatalf("unexpected content: %q", got)
	}
}

func TestCodexParseResponseMetaOnly(t *testing.T) {
	c := &Codex{}
	out := strings.Join([]string{
		`{"type":"thread.started","thread_id":"t1"}`,
		`{"type":"turn.started"}`,
		`{"type":"turn.completed"}`,
	}, "\n")

	got, err := c.ParseResponse([]byte(out))
	if err != nil {
		t.Fatalf("ParseResponse() error = %v", err)
	}
	if got != "" {
		t.Fatalf("expected empty content for meta-only response, got %q", got)
	}
}

func TestCodexParseResponseErrorEvent(t *testing.T) {
	c := &Codex{}
	out := `{"type":"error","message":"boom"}`

	_, err := c.ParseResponse([]byte(out))
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected error containing boom, got %v", err)
	}
}

func TestCodexParseResponseErrorDetailEvent(t *testing.T) {
	c := &Codex{}
	out := `{"type":"error","detail":"rate limit exceeded"}`

	_, err := c.ParseResponse([]byte(out))
	if err == nil || !strings.Contains(err.Error(), "rate limit exceeded") {
		t.Fatalf("expected error containing detail text, got %v", err)
	}
}

func TestCodexParseResponseArrayContent(t *testing.T) {
	c := &Codex{}
	out := `{"type":"item.completed","item":{"type":"agent_message","content":[{"text":"a"},{"text":"b"}]}}`

	got, err := c.ParseResponse([]byte(out))
	if err != nil {
		t.Fatalf("ParseResponse() error = %v", err)
	}
	if got != "a\nb" {
		t.Fatalf("unexpected content: %q", got)
	}
}

func TestCodexParseResponseFallbackRaw(t *testing.T) {
	c := &Codex{}
	out := "plain text response"

	got, err := c.ParseResponse([]byte(out))
	if err != nil {
		t.Fatalf("ParseResponse() error = %v", err)
	}
	if got != out {
		t.Fatalf("unexpected content: %q", got)
	}
}

func TestCodexParseResponseEmpty(t *testing.T) {
	c := &Codex{}
	_, err := c.ParseResponse(nil)
	if err == nil {
		t.Fatal("expected error for empty response")
	}
}
