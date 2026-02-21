package adapter

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

type Codex struct{}

func (c *Codex) Name() string       { return "codex" }
func (c *Codex) BinaryName() string { return "codex" }

func (c *Codex) CheckInstalled() error {
	_, err := exec.LookPath("codex")
	if err != nil {
		return fmt.Errorf("codex binary not found in PATH: %w", err)
	}
	return nil
}

func (c *Codex) BlockedEnvVars() []string {
	return []string{"CODEX_API_KEY", "OPENAI_API_KEY"}
}

func (c *Codex) BuildCommand(prompt string) *exec.Cmd {
	return exec.Command("codex", "exec", prompt, "--json")
}

// ParseResponse extracts user-facing text from Codex JSONL output.
func (c *Codex) ParseResponse(stdout []byte) (string, error) {
	raw := strings.TrimSpace(string(stdout))
	if raw == "" {
		return "", fmt.Errorf("codex: empty response")
	}

	type lineEvent struct {
		Type    string          `json:"type"`
		Message string          `json:"message"`
		Detail  string          `json:"detail"`
		Item    json.RawMessage `json:"item"`
	}
	type itemEvent struct {
		Type    string          `json:"type"`
		Content json.RawMessage `json:"content"`
		Text    string          `json:"text"`
		Message string          `json:"message"`
		Detail  string          `json:"detail"`
	}

	var messages []string
	var errors []string
	sawJSON := false

	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var ev lineEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		sawJSON = true

		switch ev.Type {
		case "item.completed":
			var item itemEvent
			if err := json.Unmarshal(ev.Item, &item); err != nil {
				continue
			}
			switch item.Type {
			case "agent_message":
				if msg := firstNonEmpty(extractCodexContent(item.Content), item.Text, item.Message); msg != "" {
					messages = append(messages, msg)
				}
			case "error":
				if msg := firstNonEmpty(item.Detail, item.Message, extractCodexContent(item.Content), item.Text); msg != "" {
					errors = append(errors, msg)
				}
			}
		case "error":
			if msg := firstNonEmpty(ev.Detail, ev.Message); msg != "" {
				errors = append(errors, msg)
			}
		}
	}

	if len(messages) > 0 {
		return strings.Join(messages, "\n\n"), nil
	}
	if len(errors) > 0 {
		return "", fmt.Errorf("codex: %s", strings.Join(errors, "; "))
	}
	if sawJSON {
		// Meta-only events with no user-facing message are treated as empty content.
		return "", nil
	}

	if content, ok := ExtractJSON(stdout); ok {
		return content, nil
	}
	if raw != "" {
		return raw, nil
	}
	return "", fmt.Errorf("codex: empty response")
}

func extractCodexContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return strings.TrimSpace(asString)
	}

	var blocks []struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err == nil {
		parts := make([]string, 0, len(blocks))
		for _, b := range blocks {
			if strings.TrimSpace(b.Text) != "" {
				parts = append(parts, strings.TrimSpace(b.Text))
			}
		}
		return strings.Join(parts, "\n")
	}

	return ""
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
