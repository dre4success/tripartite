package cycle

import (
	"maps"
	"sync"
	"time"
)

// SubtaskStatus captures progress of one subtask.
type SubtaskStatus struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Agent       string `json:"agent"`
	Completed   bool   `json:"completed"`
	Error       string `json:"error,omitempty"`
	Revision    int    `json:"revision"`
}

// CycleStatus is a point-in-time snapshot of cycle progress.
type CycleStatus struct {
	CycleID           string                  `json:"cycle_id"`
	State             State                   `json:"state"`
	Phase             string                  `json:"phase"`
	Pass              int                     `json:"pass"`
	StartedAt         time.Time               `json:"started_at"`
	Elapsed           time.Duration           `json:"elapsed"`
	CurrentSubtask    string                  `json:"current_subtask,omitempty"`
	TotalSubtasks     int                     `json:"total_subtasks"`
	CompletedSubtasks int                     `json:"completed_subtasks"`
	Subtasks          []SubtaskStatus         `json:"subtasks,omitempty"`
	RevisionCount     int                     `json:"revision_count"`
	MaxRevisions      int                     `json:"max_revisions"`
	RetryCount        map[string]int          `json:"retry_count,omitempty"`
	PendingApprovals  int                     `json:"pending_approvals"`
	LastError         string                  `json:"last_error,omitempty"`
	TaskType          string                  `json:"task_type,omitempty"`
	Intent            string                  `json:"intent,omitempty"`
	TranscriptLen     int                     `json:"transcript_len"`
	LastTranscript    TranscriptStatusSummary `json:"last_transcript,omitempty"`
	CurrentReview     *ReviewPassStats        `json:"current_review,omitempty"`
	CurrentBoard      *PhaseBoardSummary      `json:"current_board,omitempty"`
	RecentTimeline    []TimelineEvent         `json:"recent_timeline,omitempty"`
	RecentTimelineCap int                     `json:"recent_timeline_cap,omitempty"`
}

// StatusProvider is a thread-safe snapshot store.
// The cycle goroutine calls Update(); the REPL goroutine calls Snapshot().
type StatusProvider struct {
	mu     sync.RWMutex
	status *CycleStatus
}

// NewStatusProvider creates a new StatusProvider.
func NewStatusProvider() *StatusProvider {
	return &StatusProvider{}
}

func cloneCycleStatus(s CycleStatus) CycleStatus {
	cp := s
	if s.Subtasks != nil {
		cp.Subtasks = make([]SubtaskStatus, len(s.Subtasks))
		copy(cp.Subtasks, s.Subtasks)
	}
	if s.RetryCount != nil {
		cp.RetryCount = make(map[string]int, len(s.RetryCount))
		maps.Copy(cp.RetryCount, s.RetryCount)
	}
	if s.LastTranscript.Review != nil {
		rc := *s.LastTranscript.Review
		cp.LastTranscript.Review = &rc
	}
	if s.CurrentReview != nil {
		rc := *s.CurrentReview
		cp.CurrentReview = &rc
	}
	if s.CurrentBoard != nil {
		bc := *s.CurrentBoard
		if s.CurrentBoard.Items != nil {
			bc.Items = make([]PhaseBoardItem, len(s.CurrentBoard.Items))
			copy(bc.Items, s.CurrentBoard.Items)
		}
		cp.CurrentBoard = &bc
	}
	if s.RecentTimeline != nil {
		cp.RecentTimeline = make([]TimelineEvent, len(s.RecentTimeline))
		copy(cp.RecentTimeline, s.RecentTimeline)
	}
	return cp
}

// Update pushes a new status snapshot.
func (sp *StatusProvider) Update(s CycleStatus) {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	cp := cloneCycleStatus(s)
	sp.status = &cp
}

// Snapshot returns a deep copy of the current status, or nil if no cycle is active.
func (sp *StatusProvider) Snapshot() *CycleStatus {
	sp.mu.RLock()
	defer sp.mu.RUnlock()

	if sp.status == nil {
		return nil
	}

	cp := cloneCycleStatus(*sp.status)
	return &cp
}

// Clear resets the provider after a cycle ends.
func (sp *StatusProvider) Clear() {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	sp.status = nil
}
