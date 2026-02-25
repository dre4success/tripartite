package cycle

import (
	"fmt"
	"sort"
	"strings"
)

// ReviewPassStats summarizes review findings for a single phase/pass.
type ReviewPassStats struct {
	Phase    string `json:"phase"`
	Pass     int    `json:"pass"`
	Total    int    `json:"total"`
	Blockers int    `json:"blockers"`
	Warns    int    `json:"warns"`
	Infos    int    `json:"infos"`
}

// TranscriptStatusSummary is a compact, transcript-derived summary for operator UIs.
type TranscriptStatusSummary struct {
	LastKind    EntryKind        `json:"last_kind,omitempty"`
	LastAgent   string           `json:"last_agent,omitempty"`
	LastSummary string           `json:"last_summary,omitempty"`
	Review      *ReviewPassStats `json:"review,omitempty"`
}

// PhaseBoardItem is one compact, per-agent line for the current phase/pass.
type PhaseBoardItem struct {
	Role    string    `json:"role"`
	Agent   string    `json:"agent"`
	Kind    EntryKind `json:"kind"`
	Summary string    `json:"summary"`
}

// PhaseBoardSummary is a transcript-derived "who said/did what" view for a phase/pass.
type PhaseBoardSummary struct {
	Phase string           `json:"phase"`
	Pass  int              `json:"pass"`
	Items []PhaseBoardItem `json:"items,omitempty"`
}

// LastNonStateChange returns the most recent transcript entry that is not a state transition.
func (t *Transcript) LastNonStateChange() *Entry {
	t.mu.RLock()
	defer t.mu.RUnlock()

	for i := len(t.entries) - 1; i >= 0; i-- {
		if t.entries[i].Kind == KindStateChange {
			continue
		}
		e := t.entries[i]
		return &e
	}
	return nil
}

// LatestPassForPhase returns the highest pass number observed for the given kind+phase.
// It returns 0 when no entries match.
func (t *Transcript) LatestPassForPhase(kind EntryKind, phase string) int {
	t.mu.RLock()
	defer t.mu.RUnlock()

	maxPass := 0
	for _, e := range t.entries {
		if e.Kind != kind || e.Phase != phase {
			continue
		}
		if e.Pass > maxPass {
			maxPass = e.Pass
		}
	}
	return maxPass
}

// ReviewStatsForPass summarizes review findings for a given phase/pass.
func (t *Transcript) ReviewStatsForPass(phase string, pass int) ReviewPassStats {
	stats := ReviewPassStats{Phase: phase, Pass: pass}
	if pass <= 0 {
		return stats
	}

	for _, e := range t.ByKindAndPass(KindReviewFinding, phase, pass) {
		f, ok := e.Payload.(ReviewFindingPayload)
		if !ok {
			continue
		}
		stats.Total++
		switch f.Severity {
		case SeverityBlocker:
			stats.Blockers++
		case SeverityWarn:
			stats.Warns++
		case SeverityInfo:
			stats.Infos++
		}
	}
	return stats
}

// StatusSummary builds a compact transcript-derived summary for a cycle status snapshot.
func (t *Transcript) StatusSummary(currentPhase string, currentPass int) TranscriptStatusSummary {
	var out TranscriptStatusSummary
	if last := t.LastNonStateChange(); last != nil {
		out.LastKind = last.Kind
		out.LastAgent = last.Agent
		out.LastSummary = summarizeTranscriptEntry(*last)
	}

	// If the caller is in a review phase, summarize the current pass findings.
	if (currentPhase == phaseName(StatePlanReview) || currentPhase == phaseName(StateOutputReview)) && currentPass > 0 {
		stats := t.ReviewStatsForPass(currentPhase, currentPass)
		if stats.Total > 0 {
			out.Review = &stats
		}
	}

	return out
}

// PhaseBoardSummary builds a compact per-agent board for the given phase/pass.
// It keeps only the latest non-state-change entry per agent within that phase/pass.
func (t *Transcript) PhaseBoardSummary(phase string, pass int, roles *RoleMap) *PhaseBoardSummary {
	if phase == "" {
		return nil
	}

	t.mu.RLock()
	defer t.mu.RUnlock()

	type picked struct {
		id   int
		item PhaseBoardItem
	}
	perAgent := make(map[string]picked)
	for _, e := range t.entries {
		if e.Phase != phase || e.Pass != pass || e.Kind == KindStateChange {
			continue
		}

		agentName := e.Agent
		if agentName == "" {
			agentName = "coordinator"
		}
		perAgent[agentName] = picked{
			id: e.ID,
			item: PhaseBoardItem{
				Role:    roleForAgent(agentName, roles),
				Agent:   agentName,
				Kind:    e.Kind,
				Summary: summarizeTranscriptEntry(e),
			},
		}
	}

	if len(perAgent) == 0 {
		return nil
	}

	type sortable struct {
		id   int
		item PhaseBoardItem
	}
	items := make([]sortable, 0, len(perAgent))
	for _, p := range perAgent {
		items = append(items, sortable{id: p.id, item: p.item})
	}

	sort.Slice(items, func(i, j int) bool {
		ri := boardRoleRank(items[i].item.Role)
		rj := boardRoleRank(items[j].item.Role)
		if ri != rj {
			return ri < rj
		}
		if items[i].item.Agent != items[j].item.Agent {
			return items[i].item.Agent < items[j].item.Agent
		}
		return items[i].id < items[j].id
	})

	out := &PhaseBoardSummary{
		Phase: phase,
		Pass:  pass,
		Items: make([]PhaseBoardItem, 0, len(items)),
	}
	for _, it := range items {
		out.Items = append(out.Items, it.item)
	}
	return out
}

func summarizeTranscriptEntry(e Entry) string {
	switch e.Kind {
	case KindIntent:
		if p, ok := e.Payload.(IntentPayload); ok {
			return truncateInline(p.NormalizedGoal, 100)
		}
	case KindPlan:
		if p, ok := e.Payload.(PlanPayload); ok {
			perm := p.Permissions
			if perm == "" {
				perm = "unspecified"
			}
			return fmt.Sprintf("plan: %d subtasks, permissions=%s", len(p.Subtasks), perm)
		}
	case KindArtifact:
		if p, ok := e.Payload.(ArtifactPayload); ok {
			if p.Error != "" {
				return fmt.Sprintf("artifact %s (rev %d): error: %s", p.SubtaskID, p.Revision, truncateInline(p.Error, 80))
			}
			return fmt.Sprintf("artifact %s (rev %d): complete", p.SubtaskID, p.Revision)
		}
	case KindReviewFinding:
		if p, ok := e.Payload.(ReviewFindingPayload); ok {
			target := p.Target
			if target == "" {
				target = "general"
			}
			return fmt.Sprintf("[%s] %s: %s", p.Severity, target, truncateInline(p.Summary, 80))
		}
	case KindDecision:
		if p, ok := e.Payload.(DecisionPayload); ok {
			return truncateInline(firstNonEmptyLine(p.Recommendation), 100)
		}
	case KindApprovalRequest:
		if p, ok := e.Payload.(ApprovalRequestPayload); ok {
			return fmt.Sprintf("approval requested (%s): %s", p.Scope, truncateInline(p.Reason, 80))
		}
	case KindApprovalResult:
		if p, ok := e.Payload.(ApprovalResultPayload); ok {
			result := "denied"
			if p.Approved {
				result = "approved"
			}
			return fmt.Sprintf("approval %s (%s)", result, p.TicketID)
		}
	case KindError:
		if s, ok := e.Payload.(string); ok {
			return truncateInline(s, 100)
		}
	case KindStateChange:
		if p, ok := e.Payload.(StateChangePayload); ok {
			return fmt.Sprintf("%s -> %s", p.From, p.To)
		}
	}
	return string(e.Kind)
}

func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

func truncateInline(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}

func roleForAgent(agent string, roles *RoleMap) string {
	switch agent {
	case "coordinator":
		return "coordinator"
	case "operator":
		return "operator"
	}
	if roles == nil {
		return "peer"
	}
	switch agent {
	case roles.Planner:
		return "planner"
	case roles.Implementer:
		return "implementer"
	case roles.Reviewer:
		return "reviewer"
	default:
		return "peer"
	}
}

func boardRoleRank(role string) int {
	switch role {
	case "coordinator":
		return 0
	case "planner":
		return 1
	case "implementer":
		return 2
	case "reviewer":
		return 3
	case "peer":
		return 4
	case "operator":
		return 5
	default:
		return 6
	}
}
