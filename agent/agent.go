package agent

import (
	"encoding/json"
	"errors"
	"os/exec"
	"time"

	"github.com/dre4success/tripartite/models"
)

// EventType classifies normalized streaming events from any agent CLI.
type EventType string

const (
	EventThinking   EventType = "thinking"
	EventText       EventType = "text"
	EventToolUse    EventType = "tool_use"
	EventToolResult EventType = "tool_result"
	EventFileChange EventType = "file_change"
	EventCommand    EventType = "command"
	EventSession    EventType = "session"
	EventError      EventType = "error"
	EventDone       EventType = "done"
)

// ErrSkipEvent indicates the raw line is valid but not relevant for normalized
// delegate output (for example, lifecycle/noise events).
var ErrSkipEvent = errors.New("skip event")

// Event is a normalized streaming event emitted by an agent's ParseEvent.
// Raw is always populated by the agent — callers can use it for full-fidelity logging.
type Event struct {
	Type      EventType       `json:"type"`
	Agent     string          `json:"agent"`
	Timestamp time.Time       `json:"timestamp"`
	Data      any             `json:"data,omitempty"`
	Raw       json.RawMessage `json:"raw"`
}

// PromptMode indicates how an agent CLI receives its prompt.
type PromptMode string

const (
	PromptArg      PromptMode = "arg"      // pass prompt as command-line argument
	PromptStdin    PromptMode = "stdin"    // pipe prompt via stdin
	PromptTempFile PromptMode = "tempfile" // write prompt to a temp file, pass path
)

// StreamOpts configures a streaming agent invocation.
type StreamOpts struct {
	Model     string // model alias or full ID; empty means use agent default
	Sandbox   string // sandbox level: "safe", "write", "full" (agent-specific mapping)
	Cwd       string // working directory for the subprocess
	SessionID string // session ID for continuation (empty = new session)
}

// Agent defines the streaming-first interface for v2 delegate mode.
// Each CLI wrapper implements this to produce a stream of normalized Events.
type Agent interface {
	// Name returns the agent's short identifier (e.g. "claude").
	Name() string
	// BinaryName returns the CLI binary to exec (e.g. "claude").
	BinaryName() string
	// CheckInstalled verifies the CLI binary is on PATH.
	CheckInstalled() error
	// SupportedModels returns the list of model aliases this agent accepts.
	SupportedModels() []string
	// DefaultModel returns the model ID used when StreamOpts.Model is empty.
	DefaultModel() string
	// PromptMode returns how this agent's CLI receives the prompt.
	PromptMode() PromptMode
	// ContinuationArgs returns extra CLI args to resume a previous session.
	// Returns nil if the agent does not support continuation.
	ContinuationArgs(sessionID string) []string
	// StreamCommand builds an *exec.Cmd that produces JSONL/streaming output on stdout.
	StreamCommand(prompt string, opts StreamOpts) *exec.Cmd
	// ParseEvent normalizes a single line of CLI output into an Event.
	// Returns an error for unparseable lines — the caller should preserve the raw line.
	ParseEvent(line []byte) (Event, error)
	// BlockedEnvVars returns env var names that must not be set (for preflight checks).
	BlockedEnvVars() []string
}

// Registry maps agent names to their constructor functions.
var Registry = map[string]func() Agent{
	"claude": func() Agent { return &ClaudeAgent{} },
	"codex":  func() Agent { return &CodexAgent{} },
	"gemini": func() Agent { return &GeminiAgent{} },
}

// ModelAliases re-exports models.ModelAliases for backward compatibility.
var ModelAliases = models.ModelAliases

// ResolveModel re-exports models.ResolveModel for backward compatibility.
var ResolveModel = models.ResolveModel
