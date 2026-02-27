package cycle

import (
	"fmt"
	"strings"

	"github.com/dre4success/tripartite/adapter"
)

// buildPlanPrompt creates a structured planning prompt that asks the model
// to produce output under specific headings for parsing.
func buildPlanPrompt(objective string) string {
	var b strings.Builder
	b.WriteString("You are a planning agent. Given the objective below, produce a structured plan.\n\n")
	fmt.Fprintf(&b, "## Objective\n%s\n\n", objective)
	b.WriteString("Respond using exactly these sections:\n\n")
	b.WriteString("## Goals\n(Bullet list of high-level goals)\n\n")
	b.WriteString("## Subtasks\n(Numbered list: each line is `N. [agent] description` where agent is the executor)\n\n")
	b.WriteString("## Risks\n(Bullet list of risks or unknowns)\n\n")
	b.WriteString("## Permissions\n(One of: read, edit, full)\n\n")
	b.WriteString("## Success Criteria\n(Bullet list of measurable acceptance criteria)\n")
	return b.String()
}

func buildPlanPromptWithClarifications(objective string, clarifications []string) string {
	base := buildPlanPrompt(objective)
	if len(clarifications) == 0 {
		return base
	}
	var b strings.Builder
	b.WriteString(base)
	b.WriteString("\n\n## Operator Clarifications\n")
	for _, c := range clarifications {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		fmt.Fprintf(&b, "- %s\n", c)
	}
	return b.String()
}

// buildPlanReviewPrompt creates a cross-review prompt for a plan.
func buildPlanReviewPrompt(plan *PlanPayload) string {
	var b strings.Builder
	b.WriteString("You are reviewing a plan. Identify weaknesses, missing steps, risks, or improvements.\n\n")
	b.WriteString("## Plan Under Review\n\n")

	b.WriteString("### Goals\n")
	for _, g := range plan.Goals {
		fmt.Fprintf(&b, "- %s\n", g)
	}
	b.WriteString("\n### Subtasks\n")
	for _, s := range plan.Subtasks {
		fmt.Fprintf(&b, "- [%s] %s (agent: %s)\n", s.ID, s.Description, s.Agent)
	}
	b.WriteString("\n### Risks\n")
	for _, r := range plan.Risks {
		fmt.Fprintf(&b, "- %s\n", r)
	}
	b.WriteString("\n### Success Criteria\n")
	for _, c := range plan.SuccessCriteria {
		fmt.Fprintf(&b, "- %s\n", c)
	}
	b.WriteString("\nProvide your review with severity tags: [info], [warn], or [blocker] for each finding.\n")
	b.WriteString("If a finding requires operator clarification before implementation can proceed, include [clarify] in that finding.\n")
	b.WriteString("Format each finding as: [severity] [clarify?] target: summary\n")
	b.WriteString("Examples:\n")
	b.WriteString("- [warn] st-2: Add rollback step for failed migration\n")
	b.WriteString("- [blocker][clarify] clarification: Which schema version should be considered source of truth?\n")
	return b.String()
}

// buildSubtaskPrompt creates the execution prompt for a single subtask,
// including transcript context.
func buildSubtaskPrompt(subtask Subtask, transcript *Transcript) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Task: %s\n\n", subtask.Description)
	b.WriteString("Complete this task. Be thorough and produce working output.\n\n")

	// Include plan context from transcript.
	if planEntry := transcript.Last(KindPlan); planEntry != nil {
		if plan, ok := planEntry.Payload.(PlanPayload); ok {
			b.WriteString("## Context: Full Plan\n")
			for _, g := range plan.Goals {
				fmt.Fprintf(&b, "- %s\n", g)
			}
			b.WriteString("\n")
		}
	}

	// Include prior artifacts for context.
	artifacts := transcript.ByKind(KindArtifact)
	if len(artifacts) > 0 {
		b.WriteString("## Prior Work\n")
		for _, e := range artifacts {
			if a, ok := e.Payload.(ArtifactPayload); ok {
				fmt.Fprintf(&b, "### Subtask %s (by %s)\n%s\n\n", a.SubtaskID, a.Agent, truncateForPrompt(a.Content, 2000))
			}
		}
	}

	return b.String()
}

// buildOutputReviewPrompt creates a review prompt for execution artifacts.
func buildOutputReviewPrompt(artifacts []ArtifactPayload, plan *PlanPayload) string {
	var b strings.Builder
	b.WriteString("You are reviewing execution output. Check for correctness, completeness, and adherence to the plan.\n\n")

	if plan != nil {
		b.WriteString("## Success Criteria\n")
		for _, c := range plan.SuccessCriteria {
			fmt.Fprintf(&b, "- %s\n", c)
		}
		b.WriteString("\n")
	}

	b.WriteString("## Artifacts to Review\n\n")
	for _, a := range artifacts {
		fmt.Fprintf(&b, "### Subtask %s (by %s, revision %d)\n", a.SubtaskID, a.Agent, a.Revision)
		if a.Error != "" {
			fmt.Fprintf(&b, "**Error:** %s\n", a.Error)
		}
		b.WriteString(truncateForPrompt(a.Content, 3000))
		b.WriteString("\n\n")
	}

	b.WriteString("For each finding, use severity tags: [info], [warn], or [blocker].\n")
	b.WriteString("If a finding requires operator clarification before implementation can proceed, include [clarify].\n")
	b.WriteString("Format: [severity] [clarify?] target: summary\n")
	return b.String()
}

// buildRevisionPrompt creates a correction prompt for a subtask with blocker findings.
func buildRevisionPrompt(subtask Subtask, blockers []ReviewFindingPayload, transcript *Transcript) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Revision Required: %s\n\n", subtask.Description)
	b.WriteString("The following issues were found in your previous output. Address each one:\n\n")

	for i, f := range blockers {
		fmt.Fprintf(&b, "%d. [%s] %s\n", i+1, f.Severity, f.Summary)
		if f.Suggested != "" {
			fmt.Fprintf(&b, "   Suggestion: %s\n", f.Suggested)
		}
	}
	b.WriteString("\nProvide the corrected output.\n")
	return b.String()
}

// parsePlanFromResponses extracts a PlanPayload from multi-model brainstorm responses.
// It merges goals and subtasks from all successful responses.
func parsePlanFromResponses(rounds [][]adapter.Response) *PlanPayload {
	plan := &PlanPayload{}
	for _, round := range rounds {
		for _, resp := range round {
			if resp.ExitCode != 0 || resp.Content == "" {
				continue
			}
			partial := parsePlanFromText(resp.Content)
			plan.Goals = mergeUnique(plan.Goals, partial.Goals)
			plan.Subtasks = mergeSubtasks(plan.Subtasks, partial.Subtasks)
			plan.Risks = mergeUnique(plan.Risks, partial.Risks)
			plan.SuccessCriteria = mergeUnique(plan.SuccessCriteria, partial.SuccessCriteria)
			if partial.Permissions != "" && plan.Permissions == "" {
				plan.Permissions = partial.Permissions
			}
		}
	}
	return plan
}

// parsePlanFromText extracts a PlanPayload from a single text response
// using heading-based heuristic parsing.
func parsePlanFromText(text string) *PlanPayload {
	plan := &PlanPayload{}
	lines := strings.Split(text, "\n")

	var section string
	subtaskCounter := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)

		// Detect section headings.
		if strings.HasPrefix(lower, "## goals") || strings.HasPrefix(lower, "### goals") {
			section = "goals"
			continue
		}
		if strings.HasPrefix(lower, "## subtasks") || strings.HasPrefix(lower, "### subtasks") {
			section = "subtasks"
			continue
		}
		if strings.HasPrefix(lower, "## risks") || strings.HasPrefix(lower, "### risks") {
			section = "risks"
			continue
		}
		if strings.HasPrefix(lower, "## permissions") || strings.HasPrefix(lower, "### permissions") {
			section = "permissions"
			continue
		}
		if strings.HasPrefix(lower, "## success criteria") || strings.HasPrefix(lower, "### success criteria") {
			section = "criteria"
			continue
		}
		// Any other heading resets section.
		if strings.HasPrefix(trimmed, "## ") || strings.HasPrefix(trimmed, "### ") {
			section = ""
			continue
		}

		if trimmed == "" {
			continue
		}

		content := strings.TrimLeft(trimmed, "-*0123456789. ")
		if content == "" {
			continue
		}

		switch section {
		case "goals":
			plan.Goals = append(plan.Goals, content)
		case "subtasks":
			subtaskCounter++
			st := parseSubtaskLine(content, subtaskCounter)
			plan.Subtasks = append(plan.Subtasks, st)
		case "risks":
			plan.Risks = append(plan.Risks, content)
		case "permissions":
			perm := strings.ToLower(content)
			if strings.Contains(perm, "full") {
				plan.Permissions = "full"
			} else if strings.Contains(perm, "edit") {
				plan.Permissions = "edit"
			} else {
				plan.Permissions = "read"
			}
		case "criteria":
			plan.SuccessCriteria = append(plan.SuccessCriteria, content)
		}
	}

	return plan
}

// parseSubtaskLine extracts agent and description from a subtask line.
// Supports formats: "[agent] description" or just "description".
func parseSubtaskLine(line string, n int) Subtask {
	st := Subtask{
		ID: fmt.Sprintf("st-%d", n),
	}

	// Try to extract [agent] prefix.
	if strings.HasPrefix(line, "[") {
		end := strings.Index(line, "]")
		if end > 1 {
			st.Agent = strings.TrimSpace(line[1:end])
			st.Description = strings.TrimSpace(line[end+1:])
			st.NeedsWrite = containsWriteIndicator(st.Description)
			return st
		}
	}

	st.Description = line
	st.NeedsWrite = containsWriteIndicator(line)
	return st
}

// parseReviewFindings extracts review findings from brainstorm responses.
func parseReviewFindings(rounds [][]adapter.Response) []ReviewFindingPayload {
	var findings []ReviewFindingPayload
	for _, round := range rounds {
		for _, resp := range round {
			if resp.ExitCode != 0 || resp.Content == "" {
				continue
			}
			findings = append(findings, parseReviewFindingsFromText(resp.Model, resp.Content)...)
		}
	}
	return findings
}

// parseReviewFindingsFromText extracts severity-tagged findings from a review response.
func parseReviewFindingsFromText(reviewer, text string) []ReviewFindingPayload {
	var findings []ReviewFindingPayload
	lines := strings.Split(text, "\n")

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		trimmed = strings.TrimLeft(trimmed, "-*0123456789. ")
		if trimmed == "" {
			continue
		}

		sev, needsClarification, rest, ok := parseReviewLineTags(trimmed)
		if !ok {
			continue
		}

		target, summary := splitTargetSummary(rest)
		finding := ReviewFindingPayload{
			Reviewer: reviewer,
			Target:   target,
			Severity: sev,
			Summary:  summary,
		}
		if needsClarification {
			finding.NeedsClarification = true
			finding.ClarificationQuestion = summary
		}
		findings = append(findings, finding)
	}
	return findings
}

func parseReviewLineTags(line string) (Severity, bool, string, bool) {
	rest := strings.TrimSpace(line)
	if rest == "" {
		return "", false, "", false
	}

	var sev Severity
	needsClarification := false
	parsedAnyTag := false

	for strings.HasPrefix(rest, "[") {
		end := strings.Index(rest, "]")
		if end <= 1 {
			break
		}
		tag := strings.ToLower(strings.TrimSpace(rest[1:end]))
		parsedAnyTag = true
		switch tag {
		case "blocker":
			sev = SeverityBlocker
		case "warn", "warning":
			sev = SeverityWarn
		case "info":
			sev = SeverityInfo
		case "clarify":
			needsClarification = true
		}
		rest = strings.TrimSpace(rest[end+1:])
	}

	if !parsedAnyTag || sev == "" || rest == "" {
		return "", false, "", false
	}
	return sev, needsClarification, rest, true
}

// buildRecommendation produces a human-readable summary for the decision gate.
func buildRecommendation(cc *cycleContext) string {
	var b strings.Builder
	if cc.plan != nil {
		b.WriteString("## Plan Summary\n")
		for _, g := range cc.plan.Goals {
			fmt.Fprintf(&b, "- %s\n", g)
		}
		b.WriteString("\n")
	}

	artifacts := cc.transcript.ByKind(KindArtifact)
	if len(artifacts) > 0 {
		fmt.Fprintf(&b, "## Execution: %d subtask(s) completed\n", len(artifacts))
		for _, e := range artifacts {
			if a, ok := e.Payload.(ArtifactPayload); ok {
				status := "completed"
				if a.Error != "" {
					status = "error: " + a.Error
				}
				fmt.Fprintf(&b, "- %s [%s] (revision %d)\n", a.SubtaskID, status, a.Revision)
			}
		}
		b.WriteString("\n")
	}

	findings := cc.transcript.ByKind(KindReviewFinding)
	if len(findings) > 0 {
		b.WriteString("## Review Findings\n")
		for _, e := range findings {
			if f, ok := e.Payload.(ReviewFindingPayload); ok {
				fmt.Fprintf(&b, "- [%s] %s: %s\n", f.Severity, f.Target, f.Summary)
			}
		}
		b.WriteString("\n")
	}

	if cc.revisionCount > 0 {
		fmt.Fprintf(&b, "Revision loops used: %d/%d\n", cc.revisionCount, cc.cfg.Guards.MaxRevisionLoops)
	}

	return b.String()
}

// --- helpers ---

func truncateForPrompt(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n... (truncated)"
}

func containsWriteIndicator(s string) bool {
	lower := strings.ToLower(s)
	for _, w := range []string{"write", "create", "edit", "modify", "fix", "implement", "add", "update", "refactor"} {
		if strings.Contains(lower, w) {
			return true
		}
	}
	return false
}

func splitTargetSummary(s string) (target, summary string) {
	if idx := strings.Index(s, ":"); idx > 0 && idx < len(s)-1 {
		return strings.TrimSpace(s[:idx]), strings.TrimSpace(s[idx+1:])
	}
	return "", s
}

func mergeUnique(a, b []string) []string {
	seen := make(map[string]bool, len(a))
	for _, s := range a {
		seen[s] = true
	}
	for _, s := range b {
		if !seen[s] {
			a = append(a, s)
			seen[s] = true
		}
	}
	return a
}

func mergeSubtasks(a, b []Subtask) []Subtask {
	seen := make(map[string]bool, len(a))
	for _, s := range a {
		seen[s.Description] = true
	}
	for _, s := range b {
		if !seen[s.Description] {
			a = append(a, s)
			seen[s.Description] = true
		}
	}
	return a
}
