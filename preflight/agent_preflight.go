package preflight

import (
	"fmt"
	"os"
	"strings"

	"github.com/dre4success/tripartite/agent"
)

// AgentResult holds preflight outcomes for delegate-mode agents.
type AgentResult struct {
	Ready   []agent.Agent
	Skipped map[string]string
}

// CheckAgents validates delegate-mode agents by checking binary presence and
// blocked env vars. Auth is intentionally left to the operator.
func CheckAgents(agents []agent.Agent, allowAPIKeys bool, minAgents int) (*AgentResult, error) {
	res := &AgentResult{
		Skipped: make(map[string]string),
	}

	for _, a := range agents {
		if err := a.CheckInstalled(); err != nil {
			res.Skipped[a.Name()] = fmt.Sprintf("not installed: %v", err)
			continue
		}

		if !allowAPIKeys {
			if blocked := checkBlockedEnvVarNames(a.BlockedEnvVars()); blocked != "" {
				res.Skipped[a.Name()] = blocked
				continue
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
