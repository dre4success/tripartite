package agent

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
)

// GeminiAgent implements Agent for the Gemini CLI.
// Uses stream-json output mode. Model is passed natively via --model.
type GeminiAgent struct{}

func (g *GeminiAgent) Name() string       { return "gemini" }
func (g *GeminiAgent) BinaryName() string { return "gemini" }

func (g *GeminiAgent) CheckInstalled() error {
	_, err := exec.LookPath("gemini")
	if err != nil {
		return fmt.Errorf("gemini binary not found in PATH: %w", err)
	}
	return nil
}

func (g *GeminiAgent) SupportedModels() []string {
	return []string{"2.5-pro", "2.5-flash", "3"}
}

func (g *GeminiAgent) DefaultModel() string   { return "gemini-2.5-pro" }
func (g *GeminiAgent) PromptMode() PromptMode { return PromptArg }

func (g *GeminiAgent) BlockedEnvVars() []string {
	return []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"}
}

// ContinuationArgs returns nil — Gemini has no native resume; v2.1 will use history injection.
func (g *GeminiAgent) ContinuationArgs(sessionID string) []string {
	return nil
}

func (g *GeminiAgent) StreamCommand(prompt string, opts StreamOpts) *exec.Cmd {
	args := []string{"--output-format", "stream-json"}
	switch g.PromptMode() {
	case PromptArg:
		args = append(args, "-p", prompt)
	case PromptTempFile:
		// Fallback to stdin handled by runner
	}
	
	if opts.Sandbox != "" {
		args = append(args, "--sandbox")
	}

	model := opts.Model
	if model == "" {
		model = g.DefaultModel()
	}
	model = ResolveModel("gemini", model)
	args = append(args, "--model", model)

	cmd := exec.Command("gemini", args...)
	if opts.Cwd != "" {
		cmd.Dir = opts.Cwd
	}
	return cmd
}

// ParseEvent normalizes a single line of Gemini's JSONL output into an Event.
func (g *GeminiAgent) ParseEvent(line []byte) (Event, error) {
	var raw struct {
		Type    string `json:"type"`
		Content string `json:"content"`
		Message string `json:"message"`
	}

	if err := json.Unmarshal(line, &raw); err != nil {
		return Event{}, fmt.Errorf("gemini: invalid JSON: %w", err)
	}

	now := time.Now()
	base := Event{
		Agent:     "gemini",
		Timestamp: now,
		Raw:       json.RawMessage(line),
	}

	switch raw.Type {
	case "message":
		base.Type = EventText
		base.Data = raw.Content
		return base, nil

	case "tool_use":
		base.Type = EventToolUse
		base.Data = raw.Content
		return base, nil

	case "tool_result":
		base.Type = EventToolResult
		base.Data = raw.Content
		return base, nil

	case "result":
		base.Type = EventDone
		base.Data = raw.Content
		return base, nil

	case "error":
		base.Type = EventError
		base.Data = raw.Message
		return base, nil

	default:
		return Event{}, fmt.Errorf("gemini: unrecognized event type %q", raw.Type)
	}
}
