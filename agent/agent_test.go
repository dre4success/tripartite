package agent

import (
	"testing"
)

func TestClaudeParseEvent(t *testing.T) {
	c := &ClaudeAgent{}

	tests := []struct {
		name     string
		line     string
		wantType EventType
		wantData string
		wantErr  bool
	}{
		{
			name:     "text chunk",
			line:     `{"type":"assistant","message":{"content":[{"type":"text","text":"hello world"}]}}`,
			wantType: EventText,
			wantData: "hello world",
		},
		{
			name:     "tool use",
			line:     `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"make_file"}]}}`,
			wantType: EventToolUse,
			wantData: "make_file",
		},
		{
			name:     "thinking",
			line:     `{"type":"assistant","message":{"content":[{"type":"thinking","text":"hmm"}]}}`,
			wantType: EventThinking,
			wantData: "hmm",
		},
		{
			name:     "result",
			line:     `{"type":"result","result":"Success"}`,
			wantType: EventDone,
			wantData: "Success",
		},
		{
			name:    "invalid json",
			line:    `{invalid`,
			wantErr: true,
		},
		{
			name:    "unknown type",
			line:    `{"type":"unknown"}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev, err := c.ParseEvent([]byte(tt.line))
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseEvent() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr {
				if ev.Type != tt.wantType {
					t.Errorf("got type %v, want %v", ev.Type, tt.wantType)
				}
				if ev.Agent != "claude" {
					t.Errorf("got agent %q, want claude", ev.Agent)
				}
				// Verify Raw is strictly preserved
				if string(ev.Raw) != tt.line {
					t.Errorf("got raw %q, want %q", string(ev.Raw), tt.line)
				}
				if dataStr, ok := ev.Data.(string); ok {
					if dataStr != tt.wantData {
						t.Errorf("got data %q, want %q", dataStr, tt.wantData)
					}
				}
			}
		})
	}
}

func TestCodexParseEvent(t *testing.T) {
	c := &CodexAgent{}

	tests := []struct {
		name     string
		line     string
		wantType EventType
		wantData string
		wantErr  bool
	}{
		{
			name:     "session started",
			line:     `{"type":"thread.started","thread_id":"th-123"}`,
			wantType: EventSession,
			wantData: "th-123",
		},
		{
			name:     "text chunk",
			line:     `{"type":"item.completed","item":{"type":"agent_message","content":"hello"}}`,
			wantType: EventText,
			wantData: "hello",
		},
		{
			name:     "command",
			line:     `{"type":"item.completed","item":{"type":"command","content":"ls -la"}}`,
			wantType: EventCommand,
			wantData: "ls -la",
		},
		{
			name:     "file change",
			line:     `{"type":"item.completed","item":{"type":"file_change","content":"main.go"}}`,
			wantType: EventFileChange,
			wantData: "main.go",
		},
		{
			name:     "turn completed",
			line:     `{"type":"turn.completed"}`,
			wantType: EventDone,
		},
		{
			name:     "error",
			line:     `{"type":"error","message":"failed"}`,
			wantType: EventError,
			wantData: "failed",
		},
		{
			name:    "unknown event",
			line:    `{"type":"unknown"}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev, err := c.ParseEvent([]byte(tt.line))
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseEvent() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr {
				if ev.Type != tt.wantType {
					t.Errorf("got type %v, want %v", ev.Type, tt.wantType)
				}
				if ev.Agent != "codex" {
					t.Errorf("got agent %q, want codex", ev.Agent)
				}
				if string(ev.Raw) != tt.line {
					t.Errorf("got raw %q, want %q", string(ev.Raw), tt.line)
				}
				if dataStr, ok := ev.Data.(string); ok {
					if dataStr != tt.wantData {
						t.Errorf("got data %q, want %q", dataStr, tt.wantData)
					}
				}
			}
		})
	}
}

func TestGeminiParseEvent(t *testing.T) {
	g := &GeminiAgent{}

	tests := []struct {
		name     string
		line     string
		wantType EventType
		wantData string
		wantErr  bool
	}{
		{
			name:     "session started",
			line:     `{"type":"session.started","session_id":"gs-123"}`,
			wantType: EventSession,
			wantData: "gs-123",
		},
		{
			name:     "message",
			line:     `{"type":"message","content":"hello Gemini"}`,
			wantType: EventText,
			wantData: "hello Gemini",
		},
		{
			name:     "tool use",
			line:     `{"type":"tool_use","content":"query_db"}`,
			wantType: EventToolUse,
			wantData: "query_db",
		},
		{
			name:     "result",
			line:     `{"type":"result","content":"done"}`,
			wantType: EventDone,
			wantData: "done",
		},
		{
			name:     "error",
			line:     `{"type":"error","message":"failed"}`,
			wantType: EventError,
			wantData: "failed",
		},
		{
			name:    "invalid json",
			line:    `{invalid`,
			wantErr: true,
		},
		{
			name:    "unknown event",
			line:    `{"type":"unknown"}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev, err := g.ParseEvent([]byte(tt.line))
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseEvent() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr {
				if ev.Type != tt.wantType {
					t.Errorf("got type %v, want %v", ev.Type, tt.wantType)
				}
				if ev.Agent != "gemini" {
					t.Errorf("got agent %q, want gemini", ev.Agent)
				}
				if string(ev.Raw) != tt.line {
					t.Errorf("got raw %q, want %q", string(ev.Raw), tt.line)
				}
				if dataStr, ok := ev.Data.(string); ok {
					if dataStr != tt.wantData {
						t.Errorf("got data %q, want %q", dataStr, tt.wantData)
					}
				}
			}
		})
	}
}

func TestResolveModel(t *testing.T) {
	if got := ResolveModel("claude", "sonnet"); got != "claude-sonnet-4-6" {
		t.Errorf("expected claude-sonnet-4-6, got %q", got)
	}
	if got := ResolveModel("claude", "unknown"); got != "unknown" {
		t.Errorf("expected unknown, got %q", got)
	}
}
