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

func (g *Gemini) BuildCommand(prompt string, approval ApprovalLevel) *exec.Cmd {
	args := []string{"-p", prompt, "--output-format", "json"}

	switch approval {
	case ApprovalRead:
		args = append(args, "--approval-mode", "plan")
	case ApprovalFull:
		args = append(args, "--yolo")
	default:
		args = append(args, "--approval-mode", "auto_edit")
	}

	return exec.Command("gemini", args...)
}

// ExtractModel returns the primary model from Gemini's stats.models field.
func (g *Gemini) ExtractModel(stdout []byte) string {
	var resp struct {
		Stats struct {
			Models map[string]struct {
				Tokens struct {
					Candidates int `json:"candidates"`
				} `json:"tokens"`
			} `json:"models"`
		} `json:"stats"`
	}
	if err := json.Unmarshal(stdout, &resp); err != nil || len(resp.Stats.Models) == 0 {
		return ""
	}
	var best string
	var bestTokens int
	for name, usage := range resp.Stats.Models {
		if usage.Tokens.Candidates > bestTokens {
			best = name
			bestTokens = usage.Tokens.Candidates
		}
	}
	return best
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
