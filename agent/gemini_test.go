package agent

import (
	"testing"
)

func TestGeminiAgent_ParseInitEvent(t *testing.T) {
	g := &GeminiAgent{}
	
	// Test happy path with session_id
	line := []byte(`{"type": "init", "session_id": "test-session-123", "content": ""}`)
	ev, err := g.ParseEvent(line)
	if err != nil {
		t.Fatalf("unexpected error parsing init event: %v", err)
	}
	if ev.Type != EventSession {
		t.Errorf("expected event type EventSession, got %v", ev.Type)
	}
	if ev.Data != "test-session-123" {
		t.Errorf("expected session id 'test-session-123', got %v", ev.Data)
	}

	// Test missing session_id returns error
	lineMissing := []byte(`{"type": "init", "content": ""}`)
	_, errMissing := g.ParseEvent(lineMissing)
	if errMissing == nil {
		t.Error("expected error for init event missing session_id, got nil")
	}
}