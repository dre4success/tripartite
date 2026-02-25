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

// TaskType classifies the nature of a prompt for the task cycle state machine.
type TaskType string

const (
	TaskDiscuss    TaskType = "discuss"
	TaskCodeChange TaskType = "code_change"
	TaskHybrid     TaskType = "hybrid"
)

// TaskResult extends Result with a TaskType classification.
type TaskResult struct {
	Result
	TaskType TaskType
}

// ClassifyTask determines the task type for cycle mode.
// Maps IntentBrainstorm → discuss, IntentDelegate → code_change.
// Detects hybrid when a prompt contains both action verbs AND analysis words.
func ClassifyTask(prompt string, cfg Config) TaskResult {
	base := Classify(prompt, cfg)

	trimmed := strings.TrimSpace(prompt)
	if trimmed == "" {
		return TaskResult{Result: base, TaskType: TaskDiscuss}
	}

	lower := strings.ToLower(trimmed)
	words := strings.Fields(lower)

	hasAction := false
	hasAnalysis := false
	for _, w := range words {
		if actionVerbs[w] {
			hasAction = true
		}
		if analysisWords[w] {
			hasAnalysis = true
		}
	}

	if hasAction && hasAnalysis {
		return TaskResult{Result: base, TaskType: TaskHybrid}
	}

	switch base.Intent {
	case IntentDelegate:
		return TaskResult{Result: base, TaskType: TaskCodeChange}
	default:
		return TaskResult{Result: base, TaskType: TaskDiscuss}
	}
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
