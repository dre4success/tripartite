package adapter

import (
	"fmt"
	"os/exec"
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

// ParseResponse extracts content from Codex JSONL output.
// Scans line-by-line in reverse to find the last valid JSON with content.
func (c *Codex) ParseResponse(stdout []byte) (string, error) {
	if content, ok := ExtractJSON(stdout); ok {
		return content, nil
	}
	if len(stdout) > 0 {
		return string(stdout), nil
	}
	return "", fmt.Errorf("codex: empty response")
}
