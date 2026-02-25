package cycle

import (
	"context"
	"fmt"
	"strings"

	"github.com/dre4success/tripartite/adapter"
	"github.com/dre4success/tripartite/agent"
	"github.com/dre4success/tripartite/display"
	"github.com/dre4success/tripartite/orchestrator"
	"github.com/dre4success/tripartite/router"
	"github.com/dre4success/tripartite/stream"
)

// handleInit validates context and logs the cycle start.
func (cc *cycleContext) handleInit(_ context.Context) error {
	if cc.cfg.Prompt == "" {
		return fmt.Errorf("empty prompt")
	}
	if len(cc.cfg.Adapters) == 0 && len(cc.cfg.Agents) == 0 {
		return fmt.Errorf("no adapters or agents available")
	}

	fmt.Printf("[cycle] %s started\n", cc.cycleID)
	fmt.Printf("[cycle] adapters: %s\n", strings.Join(collectAdapterNames(cc.cfg.Adapters), ", "))
	fmt.Printf("[cycle] agents: %s\n", strings.Join(collectAgentNames(cc.cfg.Agents), ", "))
	return nil
}

// handleIntake classifies the prompt and assigns roles.
func (cc *cycleContext) handleIntake(_ context.Context) error {
	taskResult := router.ClassifyTask(cc.cfg.Prompt, router.Config{DefaultAgent: cc.cfg.DefaultAgent})

	taskType := TaskType(taskResult.TaskType)
	roles := assignRoles(cc.cfg.Agents, cc.cfg.Adapters, taskType)

	cc.intent = &IntentPayload{
		RawPrompt:      cc.cfg.Prompt,
		NormalizedGoal: cc.cfg.Prompt,
		TaskType:       taskType,
		Roles:          roles,
	}

	cc.transcript.Append(KindIntent, "coordinator", cc.state, *cc.intent)
	fmt.Printf("[cycle] INTAKE: task_type=%s, planner=%s, implementer=%s, reviewer=%s\n",
		taskType, roles.Planner, roles.Implementer, roles.Reviewer)
	return nil
}

// handlePlan generates a structured plan.
// For multi-adapter setups, uses orchestrator brainstorm. For single-agent, uses stream.
func (cc *cycleContext) handlePlan(ctx context.Context) error {
	planPrompt := buildPlanPrompt(cc.cfg.Prompt)

	if len(cc.cfg.Adapters) >= 2 || (len(cc.cfg.Adapters) >= 1 && len(cc.cfg.Agents) == 0) {
		// Multi-model brainstorm for plan generation.
		return cc.planViaBrainstorm(ctx, planPrompt)
	}

	// Single-agent plan via stream.
	return cc.planViaStream(ctx, planPrompt)
}

func (cc *cycleContext) planViaBrainstorm(ctx context.Context, prompt string) error {
	cc.planBrainstormRuns++
	rounds, err := orchestrator.Run(ctx, orchestrator.Config{
		Prompt:   prompt,
		Adapters: cc.cfg.Adapters,
		Timeout:  cc.cfg.Timeout,
		Approval: cc.cfg.Approval,
		Store:    cycleBrainstormStore(cc.cfg.Store, cc.cfg.TurnNum, "plan", cc.planBrainstormRuns),
		TurnNum:  0,
		Logger:   cc.cfg.Logger,
	})
	if err != nil {
		return fmt.Errorf("plan brainstorm: %w", err)
	}

	cc.plan = parsePlanFromResponses(rounds)
	cc.fillPlanDefaults()
	cc.transcript.Append(KindPlan, "coordinator", cc.state, *cc.plan)
	cc.printPlan()
	return nil
}

func (cc *cycleContext) planViaStream(ctx context.Context, prompt string) error {
	a := cc.pickStreamAgent()
	if a == nil {
		return fmt.Errorf("no agent available for planning")
	}

	var content strings.Builder
	err := stream.Run(ctx, a, prompt, agent.StreamOpts{
		Model:   agent.ResolveModel(a.Name(), a.DefaultModel()),
		Sandbox: cc.cfg.Sandbox,
		Cwd:     resolveCwd(Subtask{}, cc.cfg),
	}, stream.Callbacks{
		OnEvent: func(ev agent.Event) {
			display.PrintEvent(ev)
			if ev.Type == agent.EventText {
				if s, ok := ev.Data.(string); ok {
					content.WriteString(s)
				}
			}
		},
		OnRawLine: func(line []byte) {
			cc.saveRawLine(line)
		},
		OnStderrLine: func(line []byte) {
			fmt.Printf("[%s][stderr] %s\n", a.Name(), string(line))
		},
	})
	if err != nil {
		return fmt.Errorf("plan stream: %w", err)
	}

	cc.plan = parsePlanFromText(content.String())
	cc.fillPlanDefaults()
	cc.transcript.Append(KindPlan, a.Name(), cc.state, *cc.plan)
	cc.printPlan()
	return nil
}

// handlePlanReview cross-reviews the plan using brainstorm.
func (cc *cycleContext) handlePlanReview(ctx context.Context) error {
	if cc.plan == nil {
		return fmt.Errorf("no plan to review")
	}

	reviewPrompt := buildPlanReviewPrompt(cc.plan)

	if len(cc.cfg.Adapters) >= 2 || (len(cc.cfg.Adapters) >= 1 && len(cc.cfg.Agents) == 0) {
		cc.planReviewBrainstormRuns++
		rounds, err := orchestrator.Run(ctx, orchestrator.Config{
			Prompt:   reviewPrompt,
			Adapters: cc.cfg.Adapters,
			Timeout:  cc.cfg.Timeout,
			Approval: cc.cfg.Approval,
			Store:    cycleBrainstormStore(cc.cfg.Store, cc.cfg.TurnNum, "plan-review", cc.planReviewBrainstormRuns),
			TurnNum:  0,
			Logger:   cc.cfg.Logger,
		})
		if err != nil {
			return fmt.Errorf("plan review brainstorm: %w", err)
		}

		findings := parseReviewFindings(rounds)
		for _, f := range findings {
			cc.transcript.Append(KindReviewFinding, f.Reviewer, cc.state, f)
		}
		fmt.Printf("[cycle] PLAN_REVIEW: %d findings\n", len(findings))
		return nil
	}

	// Single-agent review fallback.
	a := cc.pickStreamAgent()
	if a == nil {
		fmt.Println("[cycle] PLAN_REVIEW: skipped (no reviewer available)")
		return nil
	}

	var content strings.Builder
	err := stream.Run(ctx, a, reviewPrompt, agent.StreamOpts{
		Model:   agent.ResolveModel(a.Name(), a.DefaultModel()),
		Sandbox: cc.cfg.Sandbox,
		Cwd:     resolveCwd(Subtask{}, cc.cfg),
	}, stream.Callbacks{
		OnEvent: func(ev agent.Event) {
			display.PrintEvent(ev)
			if ev.Type == agent.EventText {
				if s, ok := ev.Data.(string); ok {
					content.WriteString(s)
				}
			}
		},
		OnStderrLine: func(line []byte) {
			fmt.Printf("[%s][stderr] %s\n", a.Name(), string(line))
		},
	})
	if err != nil {
		return fmt.Errorf("plan review stream: %w", err)
	}

	findings := parseReviewFindingsFromText(a.Name(), content.String())
	for _, f := range findings {
		cc.transcript.Append(KindReviewFinding, f.Reviewer, cc.state, f)
	}
	fmt.Printf("[cycle] PLAN_REVIEW: %d findings\n", len(findings))
	return nil
}

// handleExecute runs each subtask sequentially via stream.
func (cc *cycleContext) handleExecute(ctx context.Context) error {
	if cc.plan == nil || len(cc.plan.Subtasks) == 0 {
		// No structured subtasks — run the whole prompt as a single task.
		if prev := cc.latestArtifact("st-1", 0); prev != nil && prev.Error == "" {
			fmt.Println("[cycle] EXECUTE: subtask st-1 — already completed, skipping retry")
			cc.lastError = nil
			return nil
		}
		if err := cc.executeSingleTask(ctx); err != nil {
			cc.retryCount["st-1"]++
			return err
		}
		cc.lastError = nil
		return nil
	}

	for _, subtask := range cc.plan.Subtasks {
		if err := ctx.Err(); err != nil {
			return err
		}

		if prev := cc.latestArtifact(subtask.ID, 0); prev != nil && prev.Error == "" {
			fmt.Printf("[cycle] EXECUTE: subtask %s — already completed, skipping retry\n", subtask.ID)
			continue
		}

		fmt.Printf("[cycle] EXECUTE: subtask %s — %s\n", subtask.ID, subtask.Description)
		if err := cc.executeSubtask(ctx, subtask, 0); err != nil {
			cc.retryCount[subtask.ID]++
			return fmt.Errorf("subtask %s: %w", subtask.ID, err)
		}
	}

	cc.lastError = nil
	return nil
}

func (cc *cycleContext) executeSingleTask(ctx context.Context) error {
	a := cc.pickStreamAgent()
	if a == nil {
		return fmt.Errorf("no agent available for execution")
	}

	subtask := Subtask{
		ID:          "st-1",
		Description: cc.cfg.Prompt,
		Agent:       a.Name(),
	}
	return cc.executeSubtask(ctx, subtask, 0)
}

func (cc *cycleContext) executeSubtask(ctx context.Context, subtask Subtask, revision int) error {
	a := cc.resolveSubtaskAgent(subtask)
	if a == nil {
		return fmt.Errorf("agent %q not found", subtask.Agent)
	}

	prompt := buildSubtaskPrompt(subtask, cc.transcript)
	var content strings.Builder
	execCwd, err := cc.executionCwd(ctx, subtask)
	if err != nil {
		return err
	}

	err = stream.Run(ctx, a, prompt, agent.StreamOpts{
		Model:   agent.ResolveModel(a.Name(), a.DefaultModel()),
		Sandbox: cc.cfg.Sandbox,
		Cwd:     execCwd,
	}, stream.Callbacks{
		OnEvent: func(ev agent.Event) {
			display.PrintEvent(ev)
			cc.saveEvent(ev)
			if ev.Type == agent.EventText {
				if s, ok := ev.Data.(string); ok {
					content.WriteString(s)
				}
			}
		},
		OnRawLine: func(line []byte) {
			cc.saveRawLine(line)
		},
		OnStderrLine: func(line []byte) {
			fmt.Printf("[%s][stderr] %s\n", a.Name(), string(line))
			cc.saveStderrLine(line)
		},
		OnParseError: func(line []byte, err error) {
			fmt.Printf("[%s][parse-error] %v\n", a.Name(), err)
		},
	})

	artifact := ArtifactPayload{
		SubtaskID: subtask.ID,
		Agent:     a.Name(),
		Content:   content.String(),
		Revision:  revision,
	}
	if err != nil {
		artifact.Error = err.Error()
	}
	cc.transcript.Append(KindArtifact, a.Name(), cc.state, artifact)
	return err
}

// handleOutputReview reviews execution artifacts using brainstorm.
func (cc *cycleContext) handleOutputReview(ctx context.Context) error {
	artifactEntries := cc.transcript.ByKind(KindArtifact)
	var artifacts []ArtifactPayload
	for _, e := range artifactEntries {
		if a, ok := e.Payload.(ArtifactPayload); ok {
			artifacts = append(artifacts, a)
		}
	}

	if len(artifacts) == 0 {
		fmt.Println("[cycle] OUTPUT_REVIEW: no artifacts to review")
		return nil
	}

	reviewPrompt := buildOutputReviewPrompt(artifacts, cc.plan)

	if len(cc.cfg.Adapters) >= 2 || (len(cc.cfg.Adapters) >= 1 && len(cc.cfg.Agents) == 0) {
		cc.outputReviewBrainstormRuns++
		rounds, err := orchestrator.Run(ctx, orchestrator.Config{
			Prompt:   reviewPrompt,
			Adapters: cc.cfg.Adapters,
			Timeout:  cc.cfg.Timeout,
			Approval: cc.cfg.Approval,
			Store:    cycleBrainstormStore(cc.cfg.Store, cc.cfg.TurnNum, "output-review", cc.outputReviewBrainstormRuns),
			TurnNum:  0,
			Logger:   cc.cfg.Logger,
		})
		if err != nil {
			return fmt.Errorf("output review brainstorm: %w", err)
		}

		findings := parseReviewFindings(rounds)
		for _, f := range findings {
			cc.transcript.Append(KindReviewFinding, f.Reviewer, cc.state, f)
		}
		blockers := filterBlockerFindings(findings)
		fmt.Printf("[cycle] OUTPUT_REVIEW: %d findings (%d blockers)\n", len(findings), len(blockers))
		return nil
	}

	// Single-agent review fallback.
	a := cc.pickStreamAgent()
	if a == nil {
		fmt.Println("[cycle] OUTPUT_REVIEW: skipped (no reviewer)")
		return nil
	}

	var content strings.Builder
	err := stream.Run(ctx, a, reviewPrompt, agent.StreamOpts{
		Model:   agent.ResolveModel(a.Name(), a.DefaultModel()),
		Sandbox: cc.cfg.Sandbox,
		Cwd:     resolveCwd(Subtask{}, cc.cfg),
	}, stream.Callbacks{
		OnEvent: func(ev agent.Event) {
			display.PrintEvent(ev)
			if ev.Type == agent.EventText {
				if s, ok := ev.Data.(string); ok {
					content.WriteString(s)
				}
			}
		},
		OnStderrLine: func(line []byte) {
			fmt.Printf("[%s][stderr] %s\n", a.Name(), string(line))
		},
	})
	if err != nil {
		return fmt.Errorf("output review stream: %w", err)
	}

	findings := parseReviewFindingsFromText(a.Name(), content.String())
	for _, f := range findings {
		cc.transcript.Append(KindReviewFinding, f.Reviewer, cc.state, f)
	}
	blockers := filterBlockerFindings(findings)
	fmt.Printf("[cycle] OUTPUT_REVIEW: %d findings (%d blockers)\n", len(findings), len(blockers))
	return nil
}

// handleRevise addresses blocker findings by re-running subtasks.
func (cc *cycleContext) handleRevise(ctx context.Context) error {
	cc.revisionCount++

	// Collect blocker findings.
	var blockers []ReviewFindingPayload
	for _, f := range cc.latestReviewFindings(StateOutputReview) {
		if f.Severity == SeverityBlocker {
			blockers = append(blockers, f)
		}
	}

	if len(blockers) == 0 {
		return nil
	}

	fmt.Printf("[cycle] REVISE: addressing %d blockers (revision %d/%d)\n",
		len(blockers), cc.revisionCount, cc.cfg.Guards.MaxRevisionLoops)

	// Group blockers by subtask and re-run.
	subtaskBlockers := make(map[string][]ReviewFindingPayload)
	for _, b := range blockers {
		subtaskBlockers[b.Target] = append(subtaskBlockers[b.Target], b)
	}

	for _, subtask := range cc.plan.Subtasks {
		sbs, ok := subtaskBlockers[subtask.ID]
		if !ok {
			continue
		}

		a := cc.resolveSubtaskAgent(subtask)
		if a == nil {
			continue
		}

		prompt := buildRevisionPrompt(subtask, sbs, cc.transcript)
		var content strings.Builder
		execCwd, err := cc.executionCwd(ctx, subtask)
		if err != nil {
			return err
		}

		err = stream.Run(ctx, a, prompt, agent.StreamOpts{
			Model:   agent.ResolveModel(a.Name(), a.DefaultModel()),
			Sandbox: cc.cfg.Sandbox,
			Cwd:     execCwd,
		}, stream.Callbacks{
			OnEvent: func(ev agent.Event) {
				display.PrintEvent(ev)
				cc.saveEvent(ev)
				if ev.Type == agent.EventText {
					if s, ok := ev.Data.(string); ok {
						content.WriteString(s)
					}
				}
			},
			OnRawLine: func(line []byte) {
				cc.saveRawLine(line)
			},
			OnStderrLine: func(line []byte) {
				fmt.Printf("[%s][stderr] %s\n", a.Name(), string(line))
			},
		})

		artifact := ArtifactPayload{
			SubtaskID: subtask.ID,
			Agent:     a.Name(),
			Content:   content.String(),
			Revision:  cc.revisionCount,
		}
		if err != nil {
			artifact.Error = err.Error()
		}
		cc.transcript.Append(KindArtifact, a.Name(), cc.state, artifact)
	}

	return nil
}

// handleDecisionGate produces a recommendation and displays it.
func (cc *cycleContext) handleDecisionGate(_ context.Context) error {
	recommendation := buildRecommendation(cc)
	tradeoffs := extractTradeoffs(cc)
	patchSummary := buildPatchSummary(cc)

	cc.decision = &DecisionPayload{
		Recommendation: recommendation,
		PatchSummary:   patchSummary,
		Tradeoffs:      tradeoffs,
	}

	cc.transcript.Append(KindDecision, "coordinator", cc.state, *cc.decision)

	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("  DECISION GATE")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println(recommendation)
	if len(tradeoffs) > 0 {
		fmt.Println("## Tradeoffs")
		for _, t := range tradeoffs {
			fmt.Printf("- %s\n", t)
		}
	}
	fmt.Println(strings.Repeat("=", 60))

	return nil
}

// handleAwaitApproval blocks until the operator approves or denies.
func (cc *cycleContext) handleAwaitApproval(ctx context.Context) error {
	broker := cc.cfg.Broker
	if broker == nil {
		// No broker — auto-approve.
		fmt.Println("[cycle] AWAIT_APPROVAL: auto-approved (no broker)")
		return nil
	}

	pa := broker.Request(
		fmt.Sprintf("Cycle needs approval to proceed to %s", cc.resumeState),
		string(cc.state),
		cc.resumeState,
	)

	cc.transcript.Append(KindApprovalRequest, "coordinator", cc.state, ApprovalRequestPayload{
		TicketID:    pa.TicketID,
		Reason:      pa.Reason,
		Scope:       pa.Scope,
		ResumeState: pa.ResumeState,
	})

	fmt.Printf("[cycle] AWAIT_APPROVAL: waiting for operator (ticket %s)\n", pa.TicketID)
	fmt.Printf("  Reason: %s\n", pa.Reason)
	fmt.Printf("  Use /approve %s or /deny %s\n", pa.TicketID, pa.TicketID)

	resolved, err := broker.Wait(ctx, pa.TicketID)
	if err != nil {
		return fmt.Errorf("approval wait: %w", err)
	}

	cc.lastApproval = resolved
	cc.transcript.Append(KindApprovalResult, "operator", cc.state, ApprovalResultPayload{
		TicketID: resolved.TicketID,
		Approved: resolved.Approved,
		Comment:  resolved.Comment,
	})

	if resolved.Approved {
		fmt.Printf("[cycle] APPROVED: %s\n", resolved.TicketID)
	} else {
		fmt.Printf("[cycle] DENIED: %s\n", resolved.TicketID)
	}
	return nil
}

// handleAwaitClarification blocks on REPL input for clarification.
func (cc *cycleContext) handleAwaitClarification(_ context.Context) error {
	// Placeholder — clarification requires deeper REPL integration.
	fmt.Println("[cycle] AWAIT_CLARIFICATION: not yet implemented, skipping")
	return nil
}

// handleRecovering logs the error and clears for retry.
func (cc *cycleContext) handleRecovering(_ context.Context) error {
	if cc.lastError != nil {
		fmt.Printf("[cycle] RECOVERING: %v\n", cc.lastError)
	}
	cc.lastError = nil
	return nil
}

// --- internal helpers ---

func (cc *cycleContext) pickStreamAgent() agent.Agent {
	if len(cc.cfg.Agents) > 0 {
		return cc.cfg.Agents[0]
	}
	return nil
}

func (cc *cycleContext) resolveSubtaskAgent(subtask Subtask) agent.Agent {
	if subtask.Agent != "" {
		if a := findAgentByName(cc.cfg.Agents, subtask.Agent); a != nil {
			return a
		}
	}
	return cc.pickStreamAgent()
}

func (cc *cycleContext) fillPlanDefaults() {
	if cc.plan == nil {
		return
	}
	if cc.plan.Permissions == "" {
		cc.plan.Permissions = cc.defaultPlanPermissions()
	}
	// Assign agents to subtasks that don't have one.
	defaultAgent := ""
	if cc.intent != nil && cc.intent.Roles.Implementer != "" {
		defaultAgent = cc.intent.Roles.Implementer
	} else if len(cc.cfg.Agents) > 0 {
		defaultAgent = cc.cfg.Agents[0].Name()
	}
	for i := range cc.plan.Subtasks {
		if cc.plan.Subtasks[i].Agent == "" {
			cc.plan.Subtasks[i].Agent = defaultAgent
		}
	}
}

func (cc *cycleContext) defaultPlanPermissions() string {
	// Discuss-only tasks should default to read to avoid unnecessary approval gates
	// when the planner omits a permissions section.
	if cc.intent != nil && cc.intent.TaskType == TaskDiscuss {
		return "read"
	}

	switch cc.cfg.Approval {
	case adapter.ApprovalRead:
		return "read"
	case adapter.ApprovalFull:
		return "full"
	default:
		// Code/hybrid work defaults to edit unless the operator constrained it.
		if cc.intent != nil && (cc.intent.TaskType == TaskCodeChange || cc.intent.TaskType == TaskHybrid) {
			return "edit"
		}
		return "read"
	}
}

func (cc *cycleContext) printPlan() {
	if cc.plan == nil {
		return
	}
	fmt.Println("\n[cycle] PLAN:")
	if len(cc.plan.Goals) > 0 {
		fmt.Println("  Goals:")
		for _, g := range cc.plan.Goals {
			fmt.Printf("    - %s\n", g)
		}
	}
	if len(cc.plan.Subtasks) > 0 {
		fmt.Println("  Subtasks:")
		for _, s := range cc.plan.Subtasks {
			fmt.Printf("    %s: %s (agent: %s)\n", s.ID, s.Description, s.Agent)
		}
	}
	if cc.plan.Permissions != "" {
		fmt.Printf("  Permissions: %s\n", cc.plan.Permissions)
	}
}

func (cc *cycleContext) saveEvent(ev agent.Event) {
	if cc.cfg.Store == nil {
		return
	}
	if err := cc.cfg.Store.SaveMetaTurnDelegateEvent(cc.cfg.TurnNum, ev); err != nil {
		fmt.Printf("[warn] failed to save event: %v\n", err)
	}
}

func (cc *cycleContext) saveRawLine(line []byte) {
	if cc.cfg.Store == nil {
		return
	}
	if err := cc.cfg.Store.SaveMetaTurnDelegateRawLine(cc.cfg.TurnNum, line); err != nil {
		fmt.Printf("[warn] failed to save raw line: %v\n", err)
	}
}

func (cc *cycleContext) saveStderrLine(line []byte) {
	if cc.cfg.Store == nil {
		return
	}
	if err := cc.cfg.Store.SaveMetaTurnDelegateStderrLine(cc.cfg.TurnNum, line); err != nil {
		fmt.Printf("[warn] failed to save stderr: %v\n", err)
	}
}

// Ensure display is used.
var _ = display.PrintEvent

// Ensure adapter is used.
var _ adapter.Response
