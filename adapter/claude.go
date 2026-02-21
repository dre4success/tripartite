package adapter

import (
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

func (c *Claude) BlockedEnvVars() []string {
	return []string{"ANTHROPIC_API_KEY"}
}

func (c *Claude) BuildCommand(prompt string, approval ApprovalLevel) *exec.Cmd {
	args := []string{"-p", prompt, "--output-format", "json"}

	switch approval {
	case ApprovalRead:
		args = append(args, "--permission-mode", "plan")
	case ApprovalFull:
		args = append(args, "--dangerously-skip-permissions")
	default:
		args = append(args, "--permission-mode", "acceptEdits")
	}

	return exec.Command("claude", args...)
}

// ParseResponse extracts the content from Claude's JSON output.
// Scans line-by-line in reverse to handle CLI preamble text (spinners, warnings).
func (c *Claude) ParseResponse(stdout []byte) (string, error) {
	if content, ok := ExtractJSON(stdout); ok {
		return content, nil
	}
	if len(stdout) > 0 {
		return string(stdout), nil
	}
	return "", fmt.Errorf("claude: empty response")
}
