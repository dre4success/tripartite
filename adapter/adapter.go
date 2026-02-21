package adapter

import (
	"encoding/json"
	"os/exec"
	"strings"
	"time"
)

// Response holds the result of running a single model CLI command.
type Response struct {
	Model    string        `json:"model"`
	Raw      []byte        `json:"raw"`
	Content  string        `json:"content"`
	Error    string        `json:"error,omitempty"`
	Duration time.Duration `json:"duration"`
	ExitCode int           `json:"exit_code"`
}

// Adapter defines the interface each CLI model wrapper must implement.
// Auth verification is the operator's responsibility — preflight only checks
// binary presence and blocked env vars.
type Adapter interface {
	Name() string
	BinaryName() string
	CheckInstalled() error
	BlockedEnvVars() []string
	BuildCommand(prompt string) *exec.Cmd
	ParseResponse(stdout []byte) (string, error)
}

// Registry maps model names to their adapter constructors.
var Registry = map[string]func() Adapter{
	"claude": func() Adapter { return &Claude{} },
	"codex":  func() Adapter { return &Codex{} },
	"gemini": func() Adapter { return &Gemini{} },
}

// ExtractJSON scans stdout for JSON with a recognized content field.
// It handles three common CLI noise patterns:
//  1. Clean JSON on its own line (most common with --json flags)
//  2. Inline noise before JSON on the same line (e.g. "Thinking...\r{...}")
//  3. Pretty-printed multi-line JSON (try unmarshalling joined lines as fallback)
func ExtractJSON(stdout []byte) (string, bool) {
	raw := strings.TrimSpace(string(stdout))
	if raw == "" {
		return "", false
	}

	// Try the whole buffer first — handles pretty-printed multi-line JSON.
	if content, ok := tryParseContent([]byte(raw)); ok {
		return content, true
	}

	// Scan line-by-line in reverse for the last line containing valid JSON.
	lines := strings.Split(raw, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		// Try the full line.
		if content, ok := tryParseContent([]byte(line)); ok {
			return content, true
		}
		// Handle inline noise: find the first '{' on the line (e.g. "Thinking...\r{...}").
		if idx := strings.IndexByte(line, '{'); idx > 0 {
			if content, ok := tryParseContent([]byte(line[idx:])); ok {
				return content, true
			}
		}
	}
	return "", false
}

func tryParseContent(data []byte) (string, bool) {
	var msg struct {
		Result  string `json:"result"`
		Content string `json:"content"`
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(data, &msg); err != nil {
		return "", false
	}
	if msg.Result != "" {
		return msg.Result, true
	}
	if msg.Content != "" {
		return msg.Content, true
	}
	if msg.Message.Content != "" {
		return msg.Message.Content, true
	}
	return "", false
}
