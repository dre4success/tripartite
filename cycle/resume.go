package cycle

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dre4success/tripartite/store"
)

var resumableStates = map[State]bool{
	StateAwaitApproval:      true,
	StateAwaitClarification: true,
	StateDecisionGate:       true,
	StateRecovering:         true,
}

type resumeSnapshot struct {
	turnNum      int
	checkpoint   store.CycleCheckpoint
	transcript   *Transcript
	worktreeInfo store.DelegateWorkspace
	resumeFrom   State
	note         string
}

// RunResume resumes a previously persisted cycle from cfg.Store/cfg.TurnNum.
// If cfg.TurnNum <= 0, it resumes the latest cycle turn in the run.
func RunResume(ctx context.Context, cfg Config) (*Result, error) {
	snap, err := loadResumeSnapshot(cfg)
	if err != nil {
		return nil, err
	}
	if snap.resumeFrom == StateAwaitApproval && cfg.Broker == nil {
		return nil, fmt.Errorf("resume target %s requires interactive mode with approval broker", snap.resumeFrom)
	}
	if snap.resumeFrom == StateAwaitClarification && cfg.Clarifier == nil {
		return nil, fmt.Errorf("resume target %s requires interactive mode with clarification broker", snap.resumeFrom)
	}

	cc := rebuildCycleContextForResume(cfg, snap)
	if snap.note != "" {
		fmt.Printf("[cycle] Resume note: %s\n", snap.note)
	}
	fmt.Printf("[cycle] Resuming %s from turn %d (checkpoint: %s)\n", cc.cycleID, snap.turnNum, snap.resumeFrom)

	return runLoop(ctx, cc)
}

func runLoop(ctx context.Context, cc *cycleContext) (*Result, error) {
	start := cc.startedAt
	if start.IsZero() {
		start = time.Now()
		cc.startedAt = start
	}

	maxRuntime := cc.cfg.Guards.MaxTotalRuntime
	if maxRuntime == 0 {
		maxRuntime = DefaultGuards().MaxTotalRuntime
	}
	ctx, cancel := context.WithTimeout(ctx, maxRuntime)
	defer cancel()

	for {
		if err := ctx.Err(); err != nil {
			cc.transcript.Append(KindError, "coordinator", cc.state, cc.currentPhase, cc.currentPass(), "cycle timed out or cancelled")
			cc.state = StateAborted
			break
		}

		if cc.state == StateDone || cc.state == StateAborted {
			break
		}

		if cc.cfg.Store != nil {
			checkpoint(cc.cfg.Store, cc.cfg.TurnNum, cc, time.Since(start))
		}
		cc.pushStatus()

		if err := cc.handle(ctx); err != nil {
			cc.lastError = err
			cc.transcript.Append(KindError, "coordinator", cc.state, cc.currentPhase, cc.currentPass(), err.Error())
			if cc.state != StateExecute {
				cc.state = StateAborted
				break
			}
		}

		fromState := cc.state
		cc.state = transition(fromState, cc)
		cc.appendStateChange(fromState, cc.state)
	}

	elapsed := time.Since(start)
	cc.pushStatus()
	cc.finalizeWorktree()
	if cc.cfg.Store != nil {
		checkpoint(cc.cfg.Store, cc.cfg.TurnNum, cc, elapsed)
		saveFinalTranscript(cc.cfg.Store, cc.cfg.TurnNum, cc)
	}

	return &Result{
		CycleID:    cc.cycleID,
		FinalState: cc.state,
		Transcript: cc.transcript,
		Plan:       cc.plan,
		Decision:   cc.decision,
		Elapsed:    elapsed,
	}, nil
}

func loadResumeSnapshot(cfg Config) (*resumeSnapshot, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("resume requires cfg.Store")
	}

	turnNum, checkpoints, err := loadCycleCheckpoints(cfg.Store.RunDir, cfg.TurnNum)
	if err != nil {
		return nil, err
	}
	cp, resumeFrom, note, err := selectResumeCheckpoint(checkpoints)
	if err != nil {
		return nil, err
	}

	tr, err := loadCycleTranscript(cfg.Store.RunDir, turnNum)
	if err != nil {
		return nil, err
	}

	ws, _ := loadResumeWorkspace(cfg.Store.RunDir, turnNum) // optional

	return &resumeSnapshot{
		turnNum:      turnNum,
		checkpoint:   cp,
		transcript:   tr,
		worktreeInfo: ws,
		resumeFrom:   resumeFrom,
		note:         note,
	}, nil
}

func rebuildCycleContextForResume(cfg Config, snap *resumeSnapshot) *cycleContext {
	cfg.TurnNum = snap.turnNum
	cc := newCycleContext(cfg)
	cc.cycleID = snap.checkpoint.CycleID
	cc.state = snap.resumeFrom
	cc.transcript = snap.transcript
	cc.worktreeInfo = snap.worktreeInfo
	if snap.checkpoint.Error != "" {
		cc.lastError = fmt.Errorf("%s", snap.checkpoint.Error)
	}

	// Preserve elapsed time in status output across resumes.
	if snap.checkpoint.Elapsed > 0 {
		cc.startedAt = time.Now().Add(-snap.checkpoint.Elapsed)
	} else {
		cc.startedAt = time.Now()
	}

	cc.restoreDerivedStateFromTranscript()
	return cc
}

func (cc *cycleContext) restoreDerivedStateFromTranscript() {
	entries := cc.transcript.Entries()

	// Restore latest typed payloads.
	for _, e := range entries {
		switch e.Kind {
		case KindIntent:
			if p, ok := e.Payload.(IntentPayload); ok {
				pp := p
				cc.intent = &pp
			}
		case KindPlan:
			if p, ok := e.Payload.(PlanPayload); ok {
				pp := p
				cc.plan = &pp
			}
		case KindDecision:
			if p, ok := e.Payload.(DecisionPayload); ok {
				pp := p
				cc.decision = &pp
			}
		case KindClarifyResult:
			if p, ok := e.Payload.(ClarificationResultPayload); ok {
				answer := strings.TrimSpace(p.Answer)
				if answer != "" {
					cc.clarifications = append(cc.clarifications, answer)
					cc.clarificationCount++
				}
			}
		}

		if e.Phase == phaseName(StatePlanReview) && e.Pass > cc.planReviewPassCount {
			cc.planReviewPassCount = e.Pass
		}
		if e.Phase == phaseName(StateOutputReview) && e.Pass > cc.outputReviewPassCount {
			cc.outputReviewPassCount = e.Pass
		}

		if e.Kind != KindArtifact {
			continue
		}
		a, ok := e.Payload.(ArtifactPayload)
		if !ok {
			continue
		}
		if a.Revision > cc.revisionCount {
			cc.revisionCount = a.Revision
		}
		if e.State == StateExecute && a.Revision == 0 && a.Error != "" {
			cc.retryCount[a.SubtaskID]++
		}
	}

	if strings.TrimSpace(cc.cfg.Prompt) == "" && cc.intent != nil {
		cc.cfg.Prompt = strings.TrimSpace(cc.intent.RawPrompt)
		if cc.cfg.Prompt == "" {
			cc.cfg.Prompt = strings.TrimSpace(cc.intent.NormalizedGoal)
		}
	}

	// Restore approval context if we are resuming while awaiting operator action.
	if cc.state == StateAwaitApproval {
		req, unresolved := latestApprovalRequest(entries)
		if req != nil && unresolved {
			cc.resumeState = req.ResumeState
		}
		if cc.resumeState == "" {
			// Fall back to deterministic inference.
			if cc.decision != nil {
				cc.resumeState = StateDone
			} else {
				cc.resumeState = StateExecute
			}
		}
	}
	if cc.state == StateAwaitClarification {
		req, unresolved := latestClarificationRequest(entries)
		if req != nil && unresolved {
			cc.resumeState = req.ResumeState
			cc.pendingClarification = strings.TrimSpace(req.Question)
		}
		if cc.resumeState == "" {
			cc.resumeState = StatePlan
		}
	}

	// Recompute decision action mapping when resuming decision approvals.
	if cc.isDecisionApproval() {
		plan := cc.planDecisionActions()
		cc.decisionApproveAction = plan.Approve
		cc.decisionDenyAction = plan.Deny
	}

	// Rebuild phase-scoped brainstorm run counters from persisted folders so
	// resumed runs continue numbering and avoid overwriting artifacts.
	cc.planBrainstormRuns = countCycleBrainstormRuns(cc.cfg.Store, cc.cfg.TurnNum, "plan")
	cc.planReviewBrainstormRuns = countCycleBrainstormRuns(cc.cfg.Store, cc.cfg.TurnNum, "plan-review")
	cc.outputReviewBrainstormRuns = countCycleBrainstormRuns(cc.cfg.Store, cc.cfg.TurnNum, "output-review")
}

func latestApprovalRequest(entries []Entry) (*ApprovalRequestPayload, bool) {
	resolved := make(map[string]bool)
	for _, e := range entries {
		if e.Kind != KindApprovalResult {
			continue
		}
		if p, ok := e.Payload.(ApprovalResultPayload); ok {
			resolved[p.TicketID] = true
		}
	}

	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if e.Kind != KindApprovalRequest {
			continue
		}
		p, ok := e.Payload.(ApprovalRequestPayload)
		if !ok {
			continue
		}
		pp := p
		return &pp, !resolved[p.TicketID]
	}
	return nil, false
}

func latestClarificationRequest(entries []Entry) (*ClarificationRequestPayload, bool) {
	resolved := make(map[string]bool)
	for _, e := range entries {
		if e.Kind != KindClarifyResult {
			continue
		}
		if p, ok := e.Payload.(ClarificationResultPayload); ok {
			resolved[p.TicketID] = true
		}
	}

	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if e.Kind != KindClarifyRequest {
			continue
		}
		p, ok := e.Payload.(ClarificationRequestPayload)
		if !ok {
			continue
		}
		pp := p
		return &pp, !resolved[p.TicketID]
	}
	return nil, false
}

func countCycleBrainstormRuns(s *store.Store, turnNum int, phase string) int {
	if s == nil || turnNum < 1 {
		return 0
	}
	root := filepath.Join(s.RunDir, fmt.Sprintf("turn-%d", turnNum), "cycle", "brainstorm")
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0
	}
	prefix := phase + "-"
	n := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if strings.HasPrefix(e.Name(), prefix) {
			n++
		}
	}
	return n
}

func loadCycleCheckpoints(runDir string, requestedTurn int) (int, []store.CycleCheckpoint, error) {
	turnNum := requestedTurn
	if turnNum <= 0 {
		var err error
		turnNum, err = latestCycleTurn(runDir)
		if err != nil {
			return 0, nil, err
		}
	}

	cycleDir := filepath.Join(runDir, fmt.Sprintf("turn-%d", turnNum), "cycle")
	files, err := filepath.Glob(filepath.Join(cycleDir, "checkpoint-*.json"))
	if err != nil {
		return 0, nil, fmt.Errorf("list cycle checkpoints: %w", err)
	}
	if len(files) == 0 {
		return 0, nil, fmt.Errorf("no cycle checkpoints found in %s", cycleDir)
	}

	checkpoints := make([]store.CycleCheckpoint, 0, len(files))
	for _, path := range files {
		var cp store.CycleCheckpoint
		if err := readJSONFile(path, &cp); err != nil {
			return 0, nil, fmt.Errorf("read checkpoint %s: %w", path, err)
		}
		checkpoints = append(checkpoints, cp)
	}
	sort.Slice(checkpoints, func(i, j int) bool {
		return checkpoints[i].Timestamp.Before(checkpoints[j].Timestamp)
	})
	return turnNum, checkpoints, nil
}

func latestCycleTurn(runDir string) (int, error) {
	entries, err := os.ReadDir(runDir)
	if err != nil {
		return 0, fmt.Errorf("read run dir %s: %w", runDir, err)
	}
	maxTurn := 0
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "turn-") {
			continue
		}
		n, err := strconv.Atoi(strings.TrimPrefix(e.Name(), "turn-"))
		if err != nil || n < 1 {
			continue
		}
		_, statErr := os.Stat(filepath.Join(runDir, e.Name(), "cycle"))
		if statErr == nil && n > maxTurn {
			maxTurn = n
		}
	}
	if maxTurn == 0 {
		return 0, fmt.Errorf("no cycle turns found in %s", runDir)
	}
	return maxTurn, nil
}

func selectResumeCheckpoint(checkpoints []store.CycleCheckpoint) (store.CycleCheckpoint, State, string, error) {
	if len(checkpoints) == 0 {
		return store.CycleCheckpoint{}, "", "", fmt.Errorf("no checkpoints available")
	}

	latest := checkpoints[len(checkpoints)-1]
	latestState := State(latest.State)
	switch latestState {
	case StateDone:
		return store.CycleCheckpoint{}, "", "", fmt.Errorf("cycle already completed (latest checkpoint: %s)", latestState)
	case StateAborted:
		if len(checkpoints) < 2 {
			return store.CycleCheckpoint{}, "", "", fmt.Errorf("latest checkpoint is ABORTED and no prior checkpoint is available")
		}
		prev := checkpoints[len(checkpoints)-2]
		prevState := State(prev.State)
		if !resumableStates[prevState] {
			return store.CycleCheckpoint{}, "", "", fmt.Errorf("latest checkpoint is ABORTED after unsafe state %s; cannot safely resume", prevState)
		}
		note := fmt.Sprintf("latest checkpoint is ABORTED; resuming from prior safe checkpoint %s", prevState)
		return prev, prevState, note, nil
	default:
		if !resumableStates[latestState] {
			return store.CycleCheckpoint{}, "", "", fmt.Errorf("latest checkpoint %s is not a safe resume state", latestState)
		}
		return latest, latestState, "", nil
	}
}

func loadCycleTranscript(runDir string, turnNum int) (*Transcript, error) {
	path := filepath.Join(runDir, fmt.Sprintf("turn-%d", turnNum), "cycle", "transcript.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("cycle transcript not found at %s (resume requires transcript.json)", path)
		}
		return nil, fmt.Errorf("read transcript: %w", err)
	}
	return decodeTranscriptJSON(data)
}

func loadResumeWorkspace(runDir string, turnNum int) (store.DelegateWorkspace, error) {
	var ws store.DelegateWorkspace
	path := filepath.Join(runDir, fmt.Sprintf("turn-%d", turnNum), "delegate", "workspace.json")
	if err := readJSONFile(path, &ws); err != nil {
		return store.DelegateWorkspace{}, err
	}
	return ws, nil
}

func readJSONFile(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return err
	}
	return nil
}

type transcriptEntryWire struct {
	ID        int             `json:"id"`
	Kind      EntryKind       `json:"kind"`
	Timestamp time.Time       `json:"timestamp"`
	Agent     string          `json:"agent,omitempty"`
	State     State           `json:"state"`
	Phase     string          `json:"phase,omitempty"`
	Pass      int             `json:"pass,omitempty"`
	Payload   json.RawMessage `json:"payload"`
}

func decodeTranscriptJSON(data []byte) (*Transcript, error) {
	var wire []transcriptEntryWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return nil, fmt.Errorf("decode transcript: %w", err)
	}

	entries := make([]Entry, 0, len(wire))
	maxID := 0
	for _, w := range wire {
		payload, err := decodeEntryPayload(w.Kind, w.Payload)
		if err != nil {
			return nil, fmt.Errorf("decode transcript payload for entry %d (%s): %w", w.ID, w.Kind, err)
		}
		entries = append(entries, Entry{
			ID:        w.ID,
			Kind:      w.Kind,
			Timestamp: w.Timestamp,
			Agent:     w.Agent,
			State:     w.State,
			Phase:     w.Phase,
			Pass:      w.Pass,
			Payload:   payload,
		})
		if w.ID > maxID {
			maxID = w.ID
		}
	}

	return &Transcript{
		entries: entries,
		nextID:  maxID + 1,
	}, nil
}

func decodeEntryPayload(kind EntryKind, raw json.RawMessage) (any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}

	switch kind {
	case KindIntent:
		var p IntentPayload
		return p, json.Unmarshal(raw, &p)
	case KindPlan:
		var p PlanPayload
		return p, json.Unmarshal(raw, &p)
	case KindTaskAssignment:
		var p map[string]any
		return p, json.Unmarshal(raw, &p)
	case KindArtifact:
		var p ArtifactPayload
		return p, json.Unmarshal(raw, &p)
	case KindClaim:
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			return s, nil
		}
		var p map[string]any
		return p, json.Unmarshal(raw, &p)
	case KindReviewFinding:
		var p ReviewFindingPayload
		return p, json.Unmarshal(raw, &p)
	case KindDecision:
		var p DecisionPayload
		return p, json.Unmarshal(raw, &p)
	case KindApprovalRequest:
		var p ApprovalRequestPayload
		return p, json.Unmarshal(raw, &p)
	case KindApprovalResult:
		var p ApprovalResultPayload
		return p, json.Unmarshal(raw, &p)
	case KindClarifyRequest:
		var p ClarificationRequestPayload
		return p, json.Unmarshal(raw, &p)
	case KindClarifyResult:
		var p ClarificationResultPayload
		return p, json.Unmarshal(raw, &p)
	case KindStateChange:
		var p StateChangePayload
		return p, json.Unmarshal(raw, &p)
	case KindError:
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			return s, nil
		}
		var p map[string]any
		return p, json.Unmarshal(raw, &p)
	default:
		var p map[string]any
		return p, json.Unmarshal(raw, &p)
	}
}
