package adapter

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
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

// ParseResponse extracts user-facing content from Gemini output.
func (g *Gemini) ParseResponse(stdout []byte) (string, error) {
	raw := strings.TrimSpace(string(stdout))
	if raw == "" {
		return "", fmt.Errorf("gemini: empty response")
	}

	if response := extractGeminiWrapper(raw); response != "" {
		return response, nil
	}

	if content := extractGeminiJSONL(raw); content != "" {
		return content, nil
	}

	if content, ok := ExtractJSON(stdout); ok {
		return content, nil
	}

	if raw != "" {
		return raw, nil
	}
	return "", fmt.Errorf("gemini: empty response")
}

func extractGeminiWrapper(raw string) string {
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return ""
	}

	keys := []string{"response", "text", "answer", "result", "content"}
	for _, k := range keys {
		if v, ok := payload[k].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}

	if message, ok := payload["message"].(map[string]any); ok {
		if v, ok := message["content"].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}

	return ""
}

func extractGeminiJSONL(raw string) string {
	type lineEvent struct {
		Type    string `json:"type"`
		Content string `json:"content"`
	}

	var parts []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var ev lineEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}

		switch ev.Type {
		case "message", "result":
			if strings.TrimSpace(ev.Content) != "" {
				parts = append(parts, strings.TrimSpace(ev.Content))
			}
		}
	}

	return strings.Join(parts, "\n\n")
}
