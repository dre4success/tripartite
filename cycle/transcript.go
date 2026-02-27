package cycle

import (
	"sync"
	"time"
)

// EntryKind identifies the type of transcript entry.
type EntryKind string

const (
	KindIntent          EntryKind = "intent"
	KindPlan            EntryKind = "plan"
	KindTaskAssignment  EntryKind = "task_assignment"
	KindArtifact        EntryKind = "artifact"
	KindClaim           EntryKind = "claim"
	KindReviewFinding   EntryKind = "review_finding"
	KindDecision        EntryKind = "decision"
	KindDecisionAction  EntryKind = "decision_action"
	KindApprovalRequest EntryKind = "approval_request"
	KindApprovalResult  EntryKind = "approval_result"
	KindClarifyRequest  EntryKind = "clarify_request"
	KindClarifyResult   EntryKind = "clarify_result"
	KindStateChange     EntryKind = "state_change"
	KindError           EntryKind = "error"
)

// Entry is a single transcript record.
type Entry struct {
	ID        int       `json:"id"`
	Kind      EntryKind `json:"kind"`
	Timestamp time.Time `json:"timestamp"`
	Agent     string    `json:"agent,omitempty"`
	State     State     `json:"state"`
	Phase     string    `json:"phase,omitempty"`
	Pass      int       `json:"pass,omitempty"`
	Payload   any       `json:"payload"`
}

// TaskType classifies the overall nature of the user's request.
type TaskType string

const (
	TaskDiscuss    TaskType = "discuss"
	TaskCodeChange TaskType = "code_change"
	TaskHybrid     TaskType = "hybrid"
)

// Severity levels for review findings.
type Severity string

const (
	SeverityInfo    Severity = "info"
	SeverityWarn    Severity = "warn"
	SeverityBlocker Severity = "blocker"
)

// Subtask is one unit of work within a plan.
type Subtask struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Agent       string `json:"agent"`
	NeedsWrite  bool   `json:"needs_write"`
}

// RoleMap assigns agent names to roles for a cycle.
type RoleMap struct {
	Planner     string `json:"planner"`
	Implementer string `json:"implementer"`
	Reviewer    string `json:"reviewer"`
}

// --- Payload types ---

// IntentPayload is the INTAKE output.
type IntentPayload struct {
	RawPrompt      string   `json:"raw_prompt"`
	NormalizedGoal string   `json:"normalized_goal"`
	TaskType       TaskType `json:"task_type"`
	Roles          RoleMap  `json:"roles"`
}

// PlanPayload is the PLAN output.
type PlanPayload struct {
	Goals           []string  `json:"goals"`
	Subtasks        []Subtask `json:"subtasks"`
	Risks           []string  `json:"risks"`
	Permissions     string    `json:"permissions"`
	SuccessCriteria []string  `json:"success_criteria"`
}

// ArtifactPayload is the EXECUTE/REVISE output per subtask.
type ArtifactPayload struct {
	SubtaskID string `json:"subtask_id"`
	Agent     string `json:"agent"`
	Content   string `json:"content"`
	Revision  int    `json:"revision"`
	Error     string `json:"error,omitempty"`
}

// ReviewFindingPayload is the OUTPUT_REVIEW output.
type ReviewFindingPayload struct {
	Reviewer              string   `json:"reviewer"`
	Target                string   `json:"target"`
	Severity              Severity `json:"severity"`
	Summary               string   `json:"summary"`
	Suggested             string   `json:"suggested,omitempty"`
	NeedsClarification    bool     `json:"needs_clarification,omitempty"`
	ClarificationQuestion string   `json:"clarification_question,omitempty"`
}

// DecisionPayload is the DECISION_GATE output.
type DecisionPayload struct {
	Recommendation string   `json:"recommendation"`
	PatchSummary   string   `json:"patch_summary"`
	Tradeoffs      []string `json:"tradeoffs,omitempty"`
	Note           string   `json:"note,omitempty"`
	Actions        []string `json:"actions,omitempty"`
}

// DecisionActionPayload records the concrete operator-selected action at DECISION_GATE.
type DecisionActionPayload struct {
	Action    string `json:"action"`
	Succeeded bool   `json:"succeeded"`
	Summary   string `json:"summary,omitempty"`
	Branch    string `json:"branch,omitempty"`
	Error     string `json:"error,omitempty"`
}

// ApprovalRequestPayload is sent to the operator.
type ApprovalRequestPayload struct {
	TicketID    string       `json:"ticket_id"`
	Kind        ApprovalKind `json:"kind,omitempty"`
	Reason      string       `json:"reason"`
	Scope       string       `json:"scope"`
	ResumeState State        `json:"resume_state"`
}

// ApprovalResultPayload is the operator's response.
type ApprovalResultPayload struct {
	TicketID string `json:"ticket_id"`
	Approved bool   `json:"approved"`
	Comment  string `json:"comment,omitempty"`
}

// ClarificationRequestPayload is sent to the operator when the cycle needs more detail.
type ClarificationRequestPayload struct {
	TicketID    string `json:"ticket_id"`
	Question    string `json:"question"`
	ResumeState State  `json:"resume_state"`
}

// ClarificationResultPayload is the operator's clarification response.
type ClarificationResultPayload struct {
	TicketID string `json:"ticket_id"`
	Answer   string `json:"answer"`
}

// StateChangePayload records a state transition.
type StateChangePayload struct {
	From State `json:"from"`
	To   State `json:"to"`
}

// Transcript is an append-only log of cycle entries.
// It is safe for concurrent reads and writes.
type Transcript struct {
	mu      sync.RWMutex
	entries []Entry
	nextID  int
}

// NewTranscript creates an empty transcript.
func NewTranscript() *Transcript {
	return &Transcript{nextID: 1}
}

// Append adds an entry and returns the new entry (with assigned ID and timestamp).
func (t *Transcript) Append(kind EntryKind, agent string, state State, phase string, pass int, payload any) Entry {
	t.mu.Lock()
	defer t.mu.Unlock()

	e := Entry{
		ID:        t.nextID,
		Kind:      kind,
		Timestamp: time.Now(),
		Agent:     agent,
		State:     state,
		Phase:     phase,
		Pass:      pass,
		Payload:   payload,
	}
	t.nextID++
	t.entries = append(t.entries, e)
	return e
}

// Entries returns a copy of all entries.
func (t *Transcript) Entries() []Entry {
	t.mu.RLock()
	defer t.mu.RUnlock()

	out := make([]Entry, len(t.entries))
	copy(out, t.entries)
	return out
}

// Last returns the most recent entry of the given kind, or nil.
func (t *Transcript) Last(kind EntryKind) *Entry {
	t.mu.RLock()
	defer t.mu.RUnlock()

	for i := len(t.entries) - 1; i >= 0; i-- {
		if t.entries[i].Kind == kind {
			e := t.entries[i]
			return &e
		}
	}
	return nil
}

// ByKind returns all entries matching the given kind.
func (t *Transcript) ByKind(kind EntryKind) []Entry {
	t.mu.RLock()
	defer t.mu.RUnlock()

	var out []Entry
	for _, e := range t.entries {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

// ByKindAndPass returns entries matching the given kind, phase, and pass number.
func (t *Transcript) ByKindAndPass(kind EntryKind, phase string, pass int) []Entry {
	t.mu.RLock()
	defer t.mu.RUnlock()

	var out []Entry
	for _, e := range t.entries {
		if e.Kind == kind && e.Phase == phase && e.Pass == pass {
			out = append(out, e)
		}
	}
	return out
}

// Len returns the number of entries.
func (t *Transcript) Len() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.entries)
}
