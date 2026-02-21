package adapter

import (
	"encoding/json"
	"fmt"
	"os/exec"
)

type Claude struct{}

func (c *Claude) Name() string       { return "claude" }
func (c *Claude) BinaryName() string { return "claude" }

func (c *Claude) CheckInstalled() error {
	_, err := exec.LookPath("claude")
	if err != nil {
		return fmt.Errorf("claude binary not found in PATH: %w", err)
	}
	return nil
}

func (c *Claude) CheckAuth() error {
	// Run a lightweight command to verify claude is authenticated.
	cmd := exec.Command("claude", "--version")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("claude auth check failed (--version returned error): %w", err)
	}
	return nil
}

func (c *Claude) BlockedEnvVars() []string {
	return []string{"ANTHROPIC_API_KEY"}
}

func (c *Claude) BuildCommand(prompt string) *exec.Cmd {
	return exec.Command("claude", "-p", prompt, "--output-format", "json")
}

// ParseResponse extracts the content from Claude's JSON output.
// Claude Code JSON output has a "result" field with the response text.
func (c *Claude) ParseResponse(stdout []byte) (string, error) {
	// Try structured JSON first.
	var result struct {
		Result string `json:"result"`
	}
	if err := json.Unmarshal(stdout, &result); err == nil && result.Result != "" {
		return result.Result, nil
	}

	// Try alternative shape: { "content": "..." }
	var alt struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(stdout, &alt); err == nil && alt.Content != "" {
		return alt.Content, nil
	}

	// Fallback: return raw text.
	if len(stdout) > 0 {
		return string(stdout), nil
	}
	return "", fmt.Errorf("claude: empty response")
}
