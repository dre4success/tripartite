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

func (c *Codex) CheckAuth() error {
	cmd := exec.Command("codex", "--version")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("codex auth check failed (--version returned error): %w", err)
	}
	return nil
}

func (c *Codex) BlockedEnvVars() []string {
	return []string{"CODEX_API_KEY", "OPENAI_API_KEY"}
}

func (c *Codex) BuildCommand(prompt string) *exec.Cmd {
	return exec.Command("codex", "exec", prompt, "--json")
}

// ParseResponse extracts content from Codex JSONL output.
// Codex may emit multiple JSON lines; we take the last message content.
func (c *Codex) ParseResponse(stdout []byte) (string, error) {
	lines := strings.Split(strings.TrimSpace(string(stdout)), "\n")

	// Walk lines in reverse to find the last meaningful content.
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var msg struct {
			Content string `json:"content"`
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			Result string `json:"result"`
		}
		if err := json.Unmarshal([]byte(line), &msg); err == nil {
			if msg.Content != "" {
				return msg.Content, nil
			}
			if msg.Message.Content != "" {
				return msg.Message.Content, nil
			}
			if msg.Result != "" {
				return msg.Result, nil
			}
		}
	}

	// Fallback: return raw text.
	if len(stdout) > 0 {
		return string(stdout), nil
	}
	return "", fmt.Errorf("codex: empty response")
}
