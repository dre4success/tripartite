package preflight

import (
	"fmt"
	"os"
	"strings"

	"github.com/dre4success/tripartite/adapter"
)

// Result holds the preflight check outcome for all requested models.
type Result struct {
	Ready    []adapter.Adapter // models that passed all checks
	Skipped  map[string]string // model name → reason skipped
}

// Check runs preflight checks on the given adapters.
// Preflight verifies binary presence and blocked env vars only.
// Auth verification is the operator's responsibility — ensure you are
// logged in to each CLI before running tripartite.
// If allowAPIKeys is true, blocked env var checks are skipped.
// Returns an error only if fewer than minModels are ready.
func Check(adapters []adapter.Adapter, allowAPIKeys bool, minModels int) (*Result, error) {
	res := &Result{
		Skipped: make(map[string]string),
	}

	for _, a := range adapters {
		if err := a.CheckInstalled(); err != nil {
			res.Skipped[a.Name()] = fmt.Sprintf("not installed: %v", err)
			continue
		}

		if !allowAPIKeys {
			if blocked := checkBlockedEnvVars(a); blocked != "" {
				res.Skipped[a.Name()] = blocked
				continue
			}
		}

		res.Ready = append(res.Ready, a)
	}

	if len(res.Ready) < minModels {
		var reasons []string
		for name, reason := range res.Skipped {
			reasons = append(reasons, fmt.Sprintf("  %s: %s", name, reason))
		}
		return res, fmt.Errorf(
			"need at least %d models but only %d passed preflight:\n%s",
			minModels, len(res.Ready), strings.Join(reasons, "\n"),
		)
	}

	return res, nil
}

func checkBlockedEnvVars(a adapter.Adapter) string {
	for _, envVar := range a.BlockedEnvVars() {
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
