package stream

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/dre4success/tripartite/agent"
)

type mockAgent struct {
	name       string
	promptMode agent.PromptMode
	echo       string
	delayDelay time.Duration
}

func (m *mockAgent) Name() string                               { return m.name }
func (m *mockAgent) BinaryName() string                         { return "echo" }
func (m *mockAgent) CheckInstalled() error                      { return nil }
func (m *mockAgent) SupportedModels() []string                  { return nil }
func (m *mockAgent) DefaultModel() string                       { return "" }
func (m *mockAgent) PromptMode() agent.PromptMode               { return m.promptMode }
func (m *mockAgent) ContinuationArgs(sessionID string) []string { return nil }
func (m *mockAgent) BlockedEnvVars() []string                   { return nil }

func (m *mockAgent) StreamCommand(prompt string, opts agent.StreamOpts) *exec.Cmd {
	if m.delayDelay > 0 {
		// Used for cancellation testing. We sleep then echo.
		return exec.Command("sh", "-c", fmt.Sprintf("sleep %d && echo '%s'", int(m.delayDelay.Seconds()), m.echo))
	}

	switch m.promptMode {
	case agent.PromptArg:
		return exec.Command("echo", m.echo)
	case agent.PromptStdin:
		// Cat stdin and then echo our response
		return exec.Command("sh", "-c", fmt.Sprintf("cat > /dev/null && echo '%s'", m.echo))
	case agent.PromptTempFile:
		return exec.Command("sh", "-c", fmt.Sprintf("cat %s > /dev/null && echo '%s'", prompt, m.echo))
	default:
		return exec.Command("echo", m.echo)
	}
}

func (m *mockAgent) ParseEvent(line []byte) (agent.Event, error) {
	if strings.Contains(string(line), "unknown") {
		return agent.Event{}, fmt.Errorf("mock error")
	}
	return agent.Event{
		Type:  agent.EventText,
		Agent: m.name,
		Raw:   line,
		Data:  string(line),
	}, nil
}

func TestRun_PromptModes(t *testing.T) {
	tests := []struct {
		name string
		mode agent.PromptMode
	}{
		{"PromptArg", agent.PromptArg},
		{"PromptStdin", agent.PromptStdin},
		{"PromptTempFile", agent.PromptTempFile},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &mockAgent{
				name:       "mock",
				promptMode: tt.mode,
				echo:       `{"type":"text"}`,
			}

			var events []agent.Event
			var raws [][]byte

			err := Run(context.Background(), a, "hello world", agent.StreamOpts{}, Callbacks{
				OnEvent: func(e agent.Event) {
					events = append(events, e)
				},
				OnRawLine: func(line []byte) {
					raws = append(raws, line)
				},
			})

			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}

			if len(events) != 1 {
				t.Errorf("got %d events, want 1", len(events))
			}
			if len(raws) != 1 {
				t.Errorf("got %d raw lines, want 1", len(raws))
			}
		})
	}
}

func TestRun_Cancellation(t *testing.T) {
	a := &mockAgent{
		name:       "mock_sleep",
		promptMode: agent.PromptArg,
		echo:       "done",
		delayDelay: 5 * time.Second, // process will run for 5 seconds
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Start Run in a goroutine
	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(ctx, a, "test", agent.StreamOpts{}, Callbacks{})
	}()

	// Wait a moment for process to start
	time.Sleep(100 * time.Millisecond)

	// Cancel the context
	cancel()

	// Wait for Run to return
	select {
	case err := <-errCh:
		if err != context.Canceled && !strings.Contains(err.Error(), "canceled") && !strings.Contains(err.Error(), "interrupt") && !strings.Contains(err.Error(), "killed") {
			// Some systems might return exit status 130 or 137 based on graceful kill.
			// The main thing is it didn't wait the full 5 seconds.
			t.Logf("Process terminated with error: %v", err)
		}
	case <-time.After(4500 * time.Millisecond):
		t.Fatalf("Run() did not return promptly after context cancellation")
	}
}

func TestRun_UnknownEvents(t *testing.T) {
	a := &mockAgent{
		name:       "mock",
		promptMode: agent.PromptArg,
		echo:       `{"type":"unknown"}`,
	}

	var events []agent.Event
	var parseErrs []error
	var raws [][]byte

	err := Run(context.Background(), a, "test", agent.StreamOpts{}, Callbacks{
		OnEvent: func(e agent.Event) {
			events = append(events, e)
		},
		OnParseError: func(line []byte, err error) {
			parseErrs = append(parseErrs, err)
		},
		OnRawLine: func(line []byte) {
			raws = append(raws, line)
		},
	})

	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if len(events) != 0 {
		t.Errorf("expected 0 events, got %d", len(events))
	}
	if len(parseErrs) != 1 {
		t.Errorf("expected 1 parse error, got %d", len(parseErrs))
	}
	if len(raws) != 1 {
		t.Errorf("expected 1 raw line, got %d", len(raws))
	}
}
