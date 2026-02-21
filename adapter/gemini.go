package adapter

import (
	"fmt"
	"os/exec"
)

type Gemini struct{}

func (g *Gemini) Name() string       { return "gemini" }
func (g *Gemini) BinaryName() string { return "gemini" }

func (g *Gemini) CheckInstalled() error {
	_, err := exec.LookPath("gemini")
	if err != nil {
		return fmt.Errorf("gemini binary not found in PATH: %w", err)
	}
	return nil
}

func (g *Gemini) BlockedEnvVars() []string {
	return []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"}
}

func (g *Gemini) BuildCommand(prompt string) *exec.Cmd {
	return exec.Command("gemini", "-p", prompt, "--output-format", "json")
}

// ParseResponse extracts content from Gemini's JSON output.
// Scans line-by-line in reverse to handle CLI preamble text (spinners, warnings).
func (g *Gemini) ParseResponse(stdout []byte) (string, error) {
	if content, ok := ExtractJSON(stdout); ok {
		return content, nil
	}
	if len(stdout) > 0 {
		return string(stdout), nil
	}
	return "", fmt.Errorf("gemini: empty response")
}
