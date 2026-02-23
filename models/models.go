package models

import "strings"

// ModelAliases maps short names to full model IDs, keyed by agent name.
var ModelAliases = map[string]map[string]string{
	"claude": {
		"opus":   "claude-opus-4-6",
		"sonnet": "claude-sonnet-4-6",
		"haiku":  "claude-haiku-4-5-20251001",
	},
	"codex": {
		"5.3":     "gpt-5.3-codex",
		"5.2":     "gpt-5.2-codex",
		"max":     "gpt-5.1-codex-max",
		"mini":    "gpt-5.1-codex-mini",
		"o3":      "o3",
		"o4-mini": "o4-mini",
	},
	"gemini": {
		"3":         "gemini-3",
		"3-flash":   "gemini-3-flash-preview",
		"2.5-pro":   "gemini-2.5-pro",
		"2.5-flash": "gemini-2.5-flash",
	},
}

// ResolveModel returns the full model ID for an alias, or the input unchanged
// if no alias matches.
func ResolveModel(agentName, alias string) string {
	key := strings.ToLower(strings.TrimSpace(alias))
	if aliases, ok := ModelAliases[agentName]; ok {
		if full, ok := aliases[key]; ok {
			return full
		}
	}
	return alias
}
