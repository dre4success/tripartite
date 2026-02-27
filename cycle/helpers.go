package cycle

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dre4success/tripartite/adapter"
	"github.com/dre4success/tripartite/agent"
	"github.com/dre4success/tripartite/preflight"
	"github.com/dre4success/tripartite/store"
	"github.com/dre4success/tripartite/workspace"
)

const (
	decisionActionApplyWorktreeFF = "apply_worktree_branch_ff"
	decisionActionAcceptResult    = "accept_result"
	decisionActionKeepProposal    = "keep_proposal"
)

type decisionActionPlan struct {
	Actions []string
	Approve string
	Deny    string
	Note    string
}

// assignRoles maps agents/adapters to planner, implementer, and reviewer roles.
func assignRoles(agents []agent.Agent, adapters []adapter.Adapter, taskType TaskType) RoleMap {
	rm := RoleMap{}

	// For discuss tasks, prefer adapters for all roles.
	if taskType == TaskDiscuss {
		if len(adapters) >= 1 {
			rm.Planner = adapters[0].Name()
		}
		if len(adapters) >= 2 {
			rm.Reviewer = adapters[1].Name()
		}
		if len(adapters) >= 3 {
			rm.Implementer = adapters[2].Name()
		} else if len(agents) >= 1 {
			rm.Implementer = agents[0].Name()
		}
		return rm
	}

	// For code_change/hybrid, prefer agents for implementation.
	if len(agents) >= 1 {
		rm.Implementer = agents[0].Name()
	}
	if len(adapters) >= 1 {
		rm.Planner = adapters[0].Name()
	} else if len(agents) >= 1 {
		rm.Planner = agents[0].Name()
	}
	if len(adapters) >= 2 {
		rm.Reviewer = adapters[1].Name()
	} else if len(agents) >= 2 {
		rm.Reviewer = agents[1].Name()
	} else if rm.Planner != "" {
		rm.Reviewer = rm.Planner // Planner doubles as reviewer.
	}

	return rm
}

// findAgentByName returns the agent matching the given name, or nil.
func findAgentByName(agents []agent.Agent, name string) agent.Agent {
	for _, a := range agents {
		if a.Name() == name {
			return a
		}
	}
	return nil
}

// collectAdapterNames returns adapter names.
func collectAdapterNames(adapters []adapter.Adapter) []string {
	names := make([]string, len(adapters))
	for i, a := range adapters {
		names[i] = a.Name()
	}
	return names
}

// collectAgentNames returns agent names.
func collectAgentNames(agents []agent.Agent) []string {
	names := make([]string, len(agents))
	for i, a := range agents {
		names[i] = a.Name()
	}
	return names
}

// checkpoint saves cycle state to the store.
func checkpoint(s *store.Store, turnNum int, cc *cycleContext, elapsed time.Duration) {
	cp := store.CycleCheckpoint{
		CycleID:    cc.cycleID,
		State:      string(cc.state),
		Timestamp:  time.Now(),
		EntryCount: cc.transcript.Len(),
		Elapsed:    elapsed,
	}
	if cc.lastError != nil {
		cp.Error = cc.lastError.Error()
	}
	if err := s.SaveCycleCheckpoint(turnNum, cp); err != nil {
		fmt.Printf("[warn] failed to save cycle checkpoint: %v\n", err)
	}
	// Persist the transcript snapshot on each checkpoint so interrupted runs
	// can be resumed without waiting for finalization.
	if err := s.SaveCycleTranscript(turnNum, cc.transcript.Entries()); err != nil {
		fmt.Printf("[warn] failed to save cycle transcript checkpoint: %v\n", err)
	}
}

// saveFinalTranscript saves the complete transcript to the store.
func saveFinalTranscript(s *store.Store, turnNum int, cc *cycleContext) {
	entries := cc.transcript.Entries()
	if err := s.SaveCycleTranscript(turnNum, entries); err != nil {
		fmt.Printf("[warn] failed to save cycle transcript: %v\n", err)
	}
}

// cycleBrainstormStore returns a phase-scoped store for cycle brainstorm runs.
// This avoids overwriting turn-N/round-N artifacts when the cycle invokes the
// orchestrator multiple times within a single meta-session turn.
func cycleBrainstormStore(s *store.Store, turnNum int, phase string, seq int) *store.Store {
	if s == nil {
		return nil
	}
	if turnNum < 1 {
		turnNum = 1
	}
	if seq < 1 {
		seq = 1
	}
	runDir := filepath.Join(
		s.RunDir,
		fmt.Sprintf("turn-%d", turnNum),
		"cycle",
		"brainstorm",
		fmt.Sprintf("%s-%02d", phase, seq),
	)
	return &store.Store{
		BaseDir: s.BaseDir,
		RunDir:  runDir,
	}
}

// executionCwd returns the working directory for cycle execution/revision steps.
// When worktree mode is enabled, it lazily prepares one worktree for the cycle and
// reuses it across all execution and revision tasks.
func (cc *cycleContext) executionCwd(ctx context.Context, subtask Subtask) (string, error) {
	if !cc.cfg.Worktree {
		return resolveCwd(subtask, cc.cfg), nil
	}
	return cc.ensureWorktree(ctx, subtask)
}

func (cc *cycleContext) ensureWorktree(ctx context.Context, subtask Subtask) (string, error) {
	if cc.worktreeInfo.Enabled && cc.worktreeInfo.WorktreePath != "" {
		return cc.worktreeInfo.WorktreePath, nil
	}

	repoRoot := resolveCwd(subtask, cc.cfg)
	if err := preflight.CheckWorktreePrereqs(ctx, repoRoot); err != nil {
		return "", err
	}

	taskID := cc.cycleID
	if cc.cfg.TurnNum > 0 {
		taskID = fmt.Sprintf("t%d-%s", cc.cfg.TurnNum, cc.cycleID)
	}

	agentName := cc.worktreeAgentName(subtask)
	info, err := workspace.Prepare(ctx, repoRoot, taskID, agentName)
	if err != nil {
		return "", fmt.Errorf("failed to prepare worktree: %w", err)
	}

	cc.worktreeInfo = store.DelegateWorkspace{
		Enabled:      true,
		TaskID:       info.TaskID,
		WorktreePath: info.WorktreePath,
		Branch:       info.Branch,
		BaseCommit:   info.BaseCommit,
	}

	fmt.Printf("[cycle] Using worktree: %s\n", cc.worktreeInfo.WorktreePath)
	cc.saveWorktreeMetadata()

	return cc.worktreeInfo.WorktreePath, nil
}

func (cc *cycleContext) worktreeAgentName(subtask Subtask) string {
	if subtask.Agent != "" {
		return subtask.Agent
	}
	if cc.intent != nil && cc.intent.Roles.Implementer != "" {
		return cc.intent.Roles.Implementer
	}
	if cc.cfg.DefaultAgent != "" {
		return cc.cfg.DefaultAgent
	}
	if len(cc.cfg.Agents) > 0 {
		return cc.cfg.Agents[0].Name()
	}
	return "cycle"
}

func (cc *cycleContext) saveWorktreeMetadata() {
	if cc.cfg.Store == nil || !cc.worktreeInfo.Enabled {
		return
	}
	if err := cc.cfg.Store.SaveMetaTurnDelegateWorkspace(cc.cfg.TurnNum, cc.worktreeInfo); err != nil {
		fmt.Printf("[warn] failed to save workspace metadata: %v\n", err)
	}
}

func (cc *cycleContext) finalizeWorktree() {
	if !cc.worktreeInfo.Enabled || cc.worktreeInfo.WorktreePath == "" {
		return
	}
	inspectCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := cc.refreshWorktreeInspect(inspectCtx); err != nil {
		fmt.Printf("[warn] failed to inspect cycle worktree commits: %v\n", err)
	}
}

func (cc *cycleContext) refreshWorktreeInspect(ctx context.Context) error {
	if !cc.worktreeInfo.Enabled || cc.worktreeInfo.WorktreePath == "" {
		return nil
	}

	head, commits, err := workspace.Inspect(ctx, cc.worktreeInfo.WorktreePath, cc.worktreeInfo.BaseCommit)
	if err != nil {
		return err
	}

	cc.worktreeInfo.HeadCommit = head
	cc.worktreeInfo.Commits = make([]store.DelegateCommit, 0, len(commits))
	for _, c := range commits {
		cc.worktreeInfo.Commits = append(cc.worktreeInfo.Commits, store.DelegateCommit{
			SHA:     c.SHA,
			Subject: c.Subject,
		})
	}
	cc.saveWorktreeMetadata()
	return nil
}

func (cc *cycleContext) planDecisionActions() decisionActionPlan {
	plan := decisionActionPlan{
		Actions: []string{decisionActionAcceptResult, decisionActionKeepProposal},
		Approve: decisionActionAcceptResult,
		Deny:    decisionActionKeepProposal,
	}

	if !cc.requiresOperatorDecision() {
		plan.Actions = []string{decisionActionAcceptResult}
		plan.Deny = ""
		return plan
	}

	if !cc.worktreeInfo.Enabled || cc.worktreeInfo.Branch == "" {
		plan.Note = "No cycle worktree branch available to apply; operator can accept the result or keep it as a proposal."
		return plan
	}

	if len(cc.worktreeInfo.Commits) == 0 {
		plan.Note = "Cycle worktree has no commits to apply automatically; operator can accept the result or keep it as a proposal."
		return plan
	}

	plan.Actions = []string{decisionActionApplyWorktreeFF, decisionActionAcceptResult, decisionActionKeepProposal}
	plan.Approve = decisionActionApplyWorktreeFF
	plan.Deny = decisionActionKeepProposal
	plan.Note = fmt.Sprintf("Operator can /approve to fast-forward merge %q or /deny to keep the proposal without applying.", cc.worktreeInfo.Branch)
	return plan
}

func (cc *cycleContext) decisionApprovalRequest() (reason, scope string) {
	scope = string(cc.state)
	if !cc.isDecisionApproval() {
		return fmt.Sprintf("Cycle needs approval to proceed to %s", cc.resumeState), scope
	}

	approveAction := cc.decisionApproveAction
	if approveAction == "" {
		approveAction = decisionActionAcceptResult
	}
	denyAction := cc.decisionDenyAction
	if denyAction == "" {
		denyAction = decisionActionKeepProposal
	}

	scope = ApprovalScopeDecisionGate
	return fmt.Sprintf("Decision required: /approve => %s, /deny => %s", approveAction, denyAction), scope
}

func (cc *cycleContext) runDecisionAction(ctx context.Context, action string) error {
	if action == "" {
		return nil
	}

	payload := DecisionActionPayload{
		Action: action,
	}
	var actionErr error
	switch action {
	case decisionActionAcceptResult:
		payload.Succeeded = true
		payload.Summary = "decision action: accepted cycle result without applying changes"
		fmt.Println("[cycle] Decision action: accepted result (no apply)")
	case decisionActionKeepProposal:
		payload.Succeeded = true
		payload.Summary = "decision action: kept result as proposal (no apply)"
		fmt.Println("[cycle] Decision action: kept proposal (no apply)")
	case decisionActionApplyWorktreeFF:
		payload.Branch = strings.TrimSpace(cc.worktreeInfo.Branch)
		if !cc.worktreeInfo.Enabled || payload.Branch == "" {
			actionErr = fmt.Errorf("decision action %q unavailable: no worktree branch", action)
			payload.Error = actionErr.Error()
			break
		}
		repoRoot := resolveCwd(Subtask{}, cc.cfg)
		if err := workspace.MergeBranchFF(ctx, repoRoot, payload.Branch); err != nil {
			actionErr = fmt.Errorf("apply worktree branch %q: %w", payload.Branch, err)
			payload.Error = actionErr.Error()
			break
		}
		payload.Succeeded = true
		payload.Summary = fmt.Sprintf("decision action: applied worktree branch %q via fast-forward merge", payload.Branch)
		fmt.Printf("[cycle] Decision action: applied worktree branch %s (ff-only)\n", payload.Branch)
	default:
		actionErr = fmt.Errorf("unknown decision action %q", action)
		payload.Error = actionErr.Error()
	}

	cc.transcript.Append(KindDecisionAction, "coordinator", cc.state, cc.currentPhase, cc.currentPass(), payload)
	return actionErr
}

// resolveCwd determines the working directory for a subtask.
func resolveCwd(subtask Subtask, cfg Config) string {
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return cwd
}

// filterBlockerFindings returns only blocker-severity findings.
func filterBlockerFindings(findings []ReviewFindingPayload) []ReviewFindingPayload {
	var out []ReviewFindingPayload
	for _, f := range findings {
		if f.Severity == SeverityBlocker {
			out = append(out, f)
		}
	}
	return out
}

// extractTradeoffs collects tradeoff notes from review findings.
func extractTradeoffs(cc *cycleContext) []string {
	var tradeoffs []string
	findings := cc.transcript.ByKind(KindReviewFinding)
	for _, e := range findings {
		if f, ok := e.Payload.(ReviewFindingPayload); ok {
			if f.Severity == SeverityWarn {
				tradeoffs = append(tradeoffs, f.Summary)
			}
		}
	}
	return tradeoffs
}

// buildPatchSummary produces a summary of all execution artifacts.
func buildPatchSummary(cc *cycleContext) string {
	artifacts := cc.transcript.ByKind(KindArtifact)
	if len(artifacts) == 0 {
		return "No artifacts produced."
	}
	var parts []string
	for _, e := range artifacts {
		if a, ok := e.Payload.(ArtifactPayload); ok {
			label := fmt.Sprintf("%s (by %s)", a.SubtaskID, a.Agent)
			if a.Error != "" {
				label += " [error]"
			}
			parts = append(parts, label)
		}
	}
	return fmt.Sprintf("%d artifact(s): %s", len(parts), join(parts, ", "))
}

func join(parts []string, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	result := parts[0]
	for _, p := range parts[1:] {
		result += sep + p
	}
	return result
}
