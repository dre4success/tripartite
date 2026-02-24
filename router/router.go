package router

import "strings"

// Intent represents the classified action type for a prompt.
type Intent string

const (
	IntentBrainstorm Intent = "brainstorm"
	IntentDelegate   Intent = "delegate"
)

// Result holds the classification outcome for a prompt.
type Result struct {
	Intent Intent
	Agent  string // target agent for delegate (empty for brainstorm)
	Reason string // human-readable explanation
}

// Config controls classification behavior.
type Config struct {
	DefaultAgent string // e.g. "claude"
}

var actionVerbs = map[string]bool{
	"fix": true, "write": true, "refactor": true, "implement": true,
	"build": true, "add": true, "create": true, "delete": true,
	"remove": true, "update": true, "migrate": true, "deploy": true,
	"install": true, "run": true, "execute": true, "rename": true,
	"move": true, "setup": true, "configure": true,
}

var analysisWords = map[string]bool{
	"compare": true, "review": true, "explain": true, "analyze": true,
	"design": true, "propose": true, "evaluate": true,
	"what": true, "why": true, "how": true,
	"should": true, "could": true, "would": true, "which": true,
	"is": true, "are": true, "does": true, "can": true,
}

// Classify determines whether a prompt should be routed to brainstorm or delegate.
// Checked in order: action verbs → question marks / analysis words → fallback to brainstorm.
func Classify(prompt string, cfg Config) Result {
	trimmed := strings.TrimSpace(prompt)
	if trimmed == "" {
		return Result{
			Intent: IntentBrainstorm,
			Reason: "empty prompt defaults to brainstorm",
		}
	}

	lower := strings.ToLower(trimmed)
	firstWord := strings.Fields(lower)[0]

	if actionVerbs[firstWord] {
		return Result{
			Intent: IntentDelegate,
			Agent:  cfg.DefaultAgent,
			Reason: "action verb: " + firstWord,
		}
	}

	if strings.Contains(lower, "?") {
		return Result{
			Intent: IntentBrainstorm,
			Reason: "contains question mark",
		}
	}

	if analysisWords[firstWord] {
		return Result{
			Intent: IntentBrainstorm,
			Reason: "analysis/question word: " + firstWord,
		}
	}

	return Result{
		Intent: IntentBrainstorm,
		Reason: "default fallback to brainstorm",
	}
}
