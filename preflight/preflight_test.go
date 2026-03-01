package preflight

import (
	"testing"

	"github.com/dre4success/tripartite/agent"
)

// stubProbe replaces probeAgent with a no-op for the duration of the test,
// so tests don't require the real CLI binaries to be installed.
func stubProbe(t *testing.T) {
	t.Helper()
	orig := probeAgent
	probeAgent = func(string) error { return nil }
	t.Cleanup(func() { probeAgent = orig })
}

func TestCheckAgentsWarnsOnCLAUDECODE(t *testing.T) {
	stubProbe(t)
	t.Setenv("CLAUDECODE", "1")

	agents := []agent.Agent{&agent.ClaudeAgent{}}
	result, _ := CheckAgents(agents, true, 0)

	if len(result.Warnings) == 0 {
		t.Fatal("expected CLAUDECODE warning, got none")
	}
	w, ok := result.Warnings["claude"]
	if !ok {
		t.Fatal("expected warning for 'claude' agent")
	}
	if w == "" {
		t.Fatal("warning message is empty")
	}
}

func TestCheckAgentsNoWarningWithoutCLAUDECODE(t *testing.T) {
	stubProbe(t)
	t.Setenv("CLAUDECODE", "")
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "")

	agents := []agent.Agent{&agent.ClaudeAgent{}}
	result, _ := CheckAgents(agents, true, 0)

	if len(result.Warnings) > 0 {
		t.Fatalf("expected no warnings, got %v", result.Warnings)
	}
}
