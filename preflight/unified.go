package preflight

import (
	"fmt"

	"github.com/dre4success/tripartite/adapter"
	"github.com/dre4success/tripartite/agent"
)

// UnifiedResult holds combined preflight outcomes for both adapters and agents.
type UnifiedResult struct {
	Adapters     *Result
	Agents       *AgentResult
	AdapterNames []string
	AgentNames   []string
}

// CheckAll runs preflight checks on both adapters (for brainstorm) and agents
// (for delegate). The meta session needs at least 1 ready adapter OR 1 ready
// agent to be useful.
func CheckAll(adapters []adapter.Adapter, agents []agent.Agent, allowAPIKeys bool) (*UnifiedResult, error) {
	adapterResult, _ := Check(adapters, allowAPIKeys, 0)
	agentResult, _ := CheckAgents(agents, allowAPIKeys, 0)

	var adapterNames []string
	for _, a := range adapterResult.Ready {
		adapterNames = append(adapterNames, a.Name())
	}

	var agentNames []string
	for _, a := range agentResult.Ready {
		agentNames = append(agentNames, a.Name())
	}

	if len(adapterNames)+len(agentNames) == 0 {
		return nil, fmt.Errorf("no adapters or agents passed preflight — nothing is available")
	}

	return &UnifiedResult{
		Adapters:     adapterResult,
		Agents:       agentResult,
		AdapterNames: adapterNames,
		AgentNames:   agentNames,
	}, nil
}
