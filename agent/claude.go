package agent

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
)

// ClaudeAgent implements Agent for the Claude Code CLI.
// Uses stream-json output mode for real-time event streaming.
type ClaudeAgent struct{}

func (c *ClaudeAgent) Name() string       { return "claude" }
func (c *ClaudeAgent) BinaryName() string { return "claude" }

func (c *ClaudeAgent) CheckInstalled() error {
	_, err := exec.LookPath("claude")
	if err != nil {
		return fmt.Errorf("claude binary not found in PATH: %w", err)
	}
	return nil
}

func (c *ClaudeAgent) SupportedModels() []string {
	return []string{"opus", "sonnet", "haiku"}
}

func (c *ClaudeAgent) DefaultModel() string  { return "claude-sonnet-4-6" }
func (c *ClaudeAgent) PromptMode() PromptMode { return PromptArg }

func (c *ClaudeAgent) BlockedEnvVars() []string {
	return []string{"ANTHROPIC_API_KEY"}
}

func (c *ClaudeAgent) ContinuationArgs(sessionID string) []string {
	if sessionID == "" {
		return nil
	}
	return []string{"--resume", sessionID}
}

func (c *ClaudeAgent) StreamCommand(prompt string, opts StreamOpts) *exec.Cmd {
	args := []string{"-p", prompt, "--output-format", "stream-json", "--verbose"}

	model := opts.Model
	if model == "" {
		model = c.DefaultModel()
	} else {
		model = ResolveModel("claude", model)
	}
	args = append(args, "--model", model)

	if opts.SessionID != "" {
		args = append(args, c.ContinuationArgs(opts.SessionID)...)
	}

	cmd := exec.Command("claude", args...)
	if opts.Cwd != "" {
		cmd.Dir = opts.Cwd
	}
	return cmd
}

// ParseEvent normalizes a single line of Claude's stream-json output into an Event.
func (c *ClaudeAgent) ParseEvent(line []byte) (Event, error) {
	var raw struct {
		Type    string `json:"type"`
		Message struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
				Name string `json:"name"`
			} `json:"content"`
		} `json:"message"`
		Result string `json:"result"`
	}

	if err := json.Unmarshal(line, &raw); err != nil {
		return Event{}, fmt.Errorf("claude: invalid JSON: %w", err)
	}

	now := time.Now()
	base := Event{
		Agent:     "claude",
		Timestamp: now,
		Raw:       json.RawMessage(line),
	}

	switch raw.Type {
	case "assistant":
		for _, block := range raw.Message.Content {
			switch block.Type {
			case "text":
				base.Type = EventText
				base.Data = block.Text
				return base, nil
			case "tool_use":
				base.Type = EventToolUse
				base.Data = block.Name
				return base, nil
			case "thinking":
				base.Type = EventThinking
				base.Data = block.Text
				return base, nil
			}
		}
		// assistant message with no recognized content blocks
		base.Type = EventThinking
		return base, nil

	case "result":
		base.Type = EventDone
		base.Data = raw.Result
		return base, nil

	default:
		return Event{}, fmt.Errorf("claude: unrecognized event type %q", raw.Type)
	}
}
