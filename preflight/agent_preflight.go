package preflight

import (
	"fmt"
	"os"
	"strings"

	"github.com/dre4success/tripartite/agent"
)

// AgentResult holds preflight outcomes for delegate-mode agents.
type AgentResult struct {
	Ready    []agent.Agent
	Skipped  map[string]string
	Warnings map[string]string
}

// envHazards maps agent names to env vars that cause runtime failures
// but shouldn't hard-block preflight (the operator may intend to unset them).
var envHazards = map[string][]string{
	"claude": {"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT"},
}

// probeAgent is the function used to probe whether an agent binary is runnable.
// Tests can replace this to avoid requiring real binaries.
var probeAgent = probeRunnable

// CheckAgents validates delegate-mode agents by checking binary presence and
// blocked env vars. Auth is intentionally left to the operator.
func CheckAgents(agents []agent.Agent, allowAPIKeys bool, minAgents int) (*AgentResult, error) {
	res := &AgentResult{
		Skipped:  make(map[string]string),
		Warnings: make(map[string]string),
	}

	for _, a := range agents {
		if err := a.CheckInstalled(); err != nil {
			res.Skipped[a.Name()] = fmt.Sprintf("not installed: %v", err)
			continue
		}

		if err := probeAgent(a.BinaryName()); err != nil {
			res.Skipped[a.Name()] = err.Error()
			continue
		}

		if !allowAPIKeys {
			if blocked := checkBlockedEnvVarNames(a.BlockedEnvVars()); blocked != "" {
				res.Skipped[a.Name()] = blocked
				continue
			}
		}

		// Check for env vars that may cause runtime failures (warn, don't skip).
		if hazards, ok := envHazards[a.Name()]; ok {
			for _, envVar := range hazards {
				if os.Getenv(envVar) != "" {
					res.Warnings[a.Name()] = fmt.Sprintf(
						"env var %s is set — %s may fail at runtime as a nested session. "+
							"Unset it before running if you encounter errors.",
						envVar, a.Name(),
					)
					break
				}
			}
		}

		res.Ready = append(res.Ready, a)
	}

	if len(res.Ready) < minAgents {
		var reasons []string
		for name, reason := range res.Skipped {
			reasons = append(reasons, fmt.Sprintf("  %s: %s", name, reason))
		}
		return res, fmt.Errorf(
			"need at least %d agents but only %d passed preflight:\n%s",
			minAgents, len(res.Ready), strings.Join(reasons, "\n"),
		)
	}

	return res, nil
}

func checkBlockedEnvVarNames(envVars []string) string {
	for _, envVar := range envVars {
		if val := os.Getenv(envVar); val != "" {
			return fmt.Sprintf(
				"env var %s is set — this forces API-key mode instead of subscription. "+
					"Unset it or pass --allow-api-keys to proceed.",
				envVar,
			)
		}
	}
	return ""
}
