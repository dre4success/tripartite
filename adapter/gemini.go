package adapter

import (
	"encoding/json"
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

func (g *Gemini) CheckAuth() error {
	cmd := exec.Command("gemini", "--version")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("gemini auth check failed (--version returned error): %w", err)
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
func (g *Gemini) ParseResponse(stdout []byte) (string, error) {
	var result struct {
		Result string `json:"result"`
	}
	if err := json.Unmarshal(stdout, &result); err == nil && result.Result != "" {
		return result.Result, nil
	}

	var alt struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(stdout, &alt); err == nil && alt.Content != "" {
		return alt.Content, nil
	}

	if len(stdout) > 0 {
		return string(stdout), nil
	}
	return "", fmt.Errorf("gemini: empty response")
}
