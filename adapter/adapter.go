package adapter

import (
	"os/exec"
	"time"
)

// Response holds the result of running a single model CLI command.
type Response struct {
	Model    string        `json:"model"`
	Raw      []byte        `json:"raw"`
	Content  string        `json:"content"`
	Error    string        `json:"error,omitempty"`
	Duration time.Duration `json:"duration"`
	ExitCode int           `json:"exit_code"`
}

// Adapter defines the interface each CLI model wrapper must implement.
type Adapter interface {
	Name() string
	BinaryName() string
	CheckInstalled() error
	CheckAuth() error
	BlockedEnvVars() []string
	BuildCommand(prompt string) *exec.Cmd
	ParseResponse(stdout []byte) (string, error)
}

// Registry maps model names to their adapter constructors.
var Registry = map[string]func() Adapter{
	"claude": func() Adapter { return &Claude{} },
	"codex":  func() Adapter { return &Codex{} },
	"gemini": func() Adapter { return &Gemini{} },
}
