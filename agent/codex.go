package agent

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
)

// CodexAgent implements Agent for the OpenAI Codex CLI.
// Uses exec --json mode for JSONL streaming output.
type CodexAgent struct{}

func (c *CodexAgent) Name() string       { return "codex" }
func (c *CodexAgent) BinaryName() string { return "codex" }

func (c *CodexAgent) CheckInstalled() error {
	_, err := exec.LookPath("codex")
	if err != nil {
		return fmt.Errorf("codex binary not found in PATH: %w", err)
	}
	return nil
}

func (c *CodexAgent) SupportedModels() []string {
	return []string{"o3", "o4-mini", "o4mini", "codex"}
}

func (c *CodexAgent) DefaultModel() string   { return "" }
func (c *CodexAgent) PromptMode() PromptMode { return PromptArg }

func (c *CodexAgent) BlockedEnvVars() []string {
	return []string{"CODEX_API_KEY", "OPENAI_API_KEY"}
}

// ContinuationArgs returns nil — Codex thread resume is deferred to v2.1.
func (c *CodexAgent) ContinuationArgs(sessionID string) []string {
	return nil
}

// mapSandbox converts tripartite sandbox levels to Codex sandbox flags.
func mapSandbox(level string) string {
	switch level {
	case "safe":
		return "read-only"
	case "write":
		return "workspace-write"
	case "full":
		return "danger-full-access"
	default:
		return ""
	}
}

func (c *CodexAgent) StreamCommand(prompt string, opts StreamOpts) *exec.Cmd {
	args := []string{"exec", prompt, "--json"}

	model := opts.Model
	if model == "" {
		model = c.DefaultModel()
	} else {
		model = ResolveModel("codex", model)
	}
	if model != "" {
		args = append(args, "-m", model)
	}

	if sandbox := mapSandbox(opts.Sandbox); sandbox != "" {
		args = append(args, "--sandbox", sandbox)
	}

	cmd := exec.Command("codex", args...)
	if opts.Cwd != "" {
		cmd.Dir = opts.Cwd
	}
	return cmd
}

// ParseEvent normalizes a single line of Codex JSONL output into an Event.
func (c *CodexAgent) ParseEvent(line []byte) (Event, error) {
	var raw struct {
		Type      string `json:"type"`
		ThreadID  string `json:"thread_id"`
		SessionID string `json:"session_id"`
		Item      struct {
			Type    string `json:"type"`
			Content string `json:"content"`
		} `json:"item"`
		Message string `json:"message"`
	}

	if err := json.Unmarshal(line, &raw); err != nil {
		return Event{}, fmt.Errorf("codex: invalid JSON: %w", err)
	}

	now := time.Now()
	base := Event{
		Agent:     "codex",
		Timestamp: now,
		Raw:       json.RawMessage(line),
	}

	switch raw.Type {
	case "thread.started", "session.started":
		sid := raw.ThreadID
		if sid == "" {
			sid = raw.SessionID
		}
		if sid == "" {
			return Event{}, fmt.Errorf("codex: %s missing thread/session id", raw.Type)
		}
		base.Type = EventSession
		base.Data = sid
		return base, nil

	case "item.completed":
		switch raw.Item.Type {
		case "agent_message":
			base.Type = EventText
			base.Data = raw.Item.Content
		case "command":
			base.Type = EventCommand
			base.Data = raw.Item.Content
		case "file_change":
			base.Type = EventFileChange
			base.Data = raw.Item.Content
		default:
			return Event{}, fmt.Errorf("codex: unrecognized item type %q", raw.Item.Type)
		}
		return base, nil

	case "turn.completed":
		base.Type = EventDone
		return base, nil

	case "error":
		base.Type = EventError
		base.Data = raw.Message
		return base, nil

	default:
		return Event{}, fmt.Errorf("codex: unrecognized event type %q", raw.Type)
	}
}
