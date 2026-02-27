package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dre4success/tripartite/adapter"
	"github.com/dre4success/tripartite/agent"
)

// RunMeta holds metadata about the entire run, saved as input.json.
type RunMeta struct {
	Prompt    string   `json:"prompt"`
	Models    []string `json:"models"`
	Timeout   string   `json:"timeout"`
	Timestamp string   `json:"timestamp"`
	Mode      string   `json:"mode,omitempty"` // "one-shot" or "interactive"
}

// TurnMeta holds metadata for a single interactive turn.
type TurnMeta struct {
	Prompt string `json:"prompt"`
	Turn   int    `json:"turn"`
}

// DelegateWorkspace captures optional worktree metadata for delegate mode.
type DelegateWorkspace struct {
	Enabled      bool             `json:"enabled"`
	TaskID       string           `json:"task_id,omitempty"`
	WorktreePath string           `json:"worktree_path,omitempty"`
	Branch       string           `json:"branch,omitempty"`
	BaseCommit   string           `json:"base_commit,omitempty"`
	HeadCommit   string           `json:"head_commit,omitempty"`
	Commits      []DelegateCommit `json:"commits,omitempty"`
}

// DelegateCommit captures generated commits from a delegate worktree.
type DelegateCommit struct {
	SHA     string `json:"sha"`
	Subject string `json:"subject,omitempty"`
}

// DelegateSummary captures high-level run outcome for delegate mode.
type DelegateSummary struct {
	Agent      string
	Model      string
	Prompt     string
	Sandbox    string
	Duration   time.Duration
	EventCount int
	Error      string
	Worktree   DelegateWorkspace
}

// Store manages persisting run artifacts to disk.
type Store struct {
	BaseDir string // e.g. "./runs"
	RunDir  string // e.g. "./runs/2026-02-21T10-30-00"
}

// New creates a Store and initializes the run directory with a timestamp.
func New(baseDir string) (*Store, error) {
	var suffix [3]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return nil, fmt.Errorf("failed to generate random suffix: %w", err)
	}
	ts := time.Now().Format("2006-01-02T15-04-05") + "-" + hex.EncodeToString(suffix[:])
	runDir := filepath.Join(baseDir, ts)

	// Create round directories upfront.
	for _, round := range []string{"round-1", "round-2", "round-3"} {
		dir := filepath.Join(runDir, round)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("failed to create %s: %w", dir, err)
		}
	}

	return &Store{BaseDir: baseDir, RunDir: runDir}, nil
}

// SaveInput writes the run metadata to input.json.
func (s *Store) SaveInput(meta RunMeta) error {
	return s.writeJSON(filepath.Join(s.RunDir, "input.json"), meta)
}

// SaveDelegateEvent appends one normalized event as JSONL.
func (s *Store) SaveDelegateEvent(ev agent.Event) error {
	data, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("marshal delegate event: %w", err)
	}
	return s.appendLine(filepath.Join(s.RunDir, "events.normalized.jsonl"), data)
}

// SaveDelegateRawLine appends one provider raw line to raw event logs.
func (s *Store) SaveDelegateRawLine(line []byte) error {
	return s.appendLine(filepath.Join(s.RunDir, "events.raw.jsonl"), line)
}

// SaveDelegateStderrLine appends one stderr line.
func (s *Store) SaveDelegateStderrLine(line []byte) error {
	return s.appendLine(filepath.Join(s.RunDir, "stderr.log"), line)
}

// SaveMetaTurnDelegateEvent appends one normalized event for a specific meta session delegate turn.
func (s *Store) SaveMetaTurnDelegateEvent(turn int, ev agent.Event) error {
	data, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("marshal delegate event: %w", err)
	}
	return s.appendMetaTurnDelegateLine(turn, "events.normalized.jsonl", data)
}

// SaveMetaTurnDelegateRawLine appends one provider raw line for a specific meta session delegate turn.
func (s *Store) SaveMetaTurnDelegateRawLine(turn int, line []byte) error {
	return s.appendMetaTurnDelegateLine(turn, "events.raw.jsonl", line)
}

// SaveMetaTurnDelegateStderrLine appends one stderr line for a specific meta session delegate turn.
func (s *Store) SaveMetaTurnDelegateStderrLine(turn int, line []byte) error {
	return s.appendMetaTurnDelegateLine(turn, "stderr.log", line)
}

// SaveMetaTurnDelegateWorkspace persists workspace metadata for a specific meta session delegate turn.
func (s *Store) SaveMetaTurnDelegateWorkspace(turn int, info DelegateWorkspace) error {
	dir, err := s.metaTurnDelegateDir(turn)
	if err != nil {
		return err
	}
	return s.writeJSON(filepath.Join(dir, "workspace.json"), info)
}

// SaveDelegateWorkspace persists optional workspace metadata.
func (s *Store) SaveDelegateWorkspace(info DelegateWorkspace) error {
	return s.writeJSON(filepath.Join(s.RunDir, "workspace.json"), info)
}

// SaveDelegateSummary writes a markdown summary for delegate runs.
func (s *Store) SaveDelegateSummary(summary DelegateSummary) error {
	var b strings.Builder
	b.WriteString("# Tripartite Delegate Summary\n\n")
	fmt.Fprintf(&b, "**Agent:** %s\n\n", summary.Agent)
	if summary.Model != "" {
		fmt.Fprintf(&b, "**Model:** %s\n\n", summary.Model)
	}
	if summary.Sandbox != "" {
		fmt.Fprintf(&b, "**Sandbox:** %s\n\n", summary.Sandbox)
	}
	fmt.Fprintf(&b, "**Duration:** %.1fs\n\n", summary.Duration.Seconds())
	fmt.Fprintf(&b, "**Events:** %d\n\n", summary.EventCount)
	if summary.Worktree.Enabled {
		fmt.Fprintf(&b, "**Worktree:** `%s`\n\n", summary.Worktree.WorktreePath)
		fmt.Fprintf(&b, "**Branch:** `%s`\n\n", summary.Worktree.Branch)
		if summary.Worktree.BaseCommit != "" {
			fmt.Fprintf(&b, "**Base Commit:** `%s`\n\n", summary.Worktree.BaseCommit)
		}
		if summary.Worktree.HeadCommit != "" {
			fmt.Fprintf(&b, "**Head Commit:** `%s`\n\n", summary.Worktree.HeadCommit)
		}
		if len(summary.Worktree.Commits) > 0 {
			fmt.Fprintf(&b, "**Generated Commits:** %d\n\n", len(summary.Worktree.Commits))
		}
	}
	if summary.Error != "" {
		fmt.Fprintf(&b, "**Error:** %s\n\n", summary.Error)
	}
	b.WriteString("## Prompt\n\n")
	b.WriteString(summary.Prompt)
	b.WriteString("\n")

	return os.WriteFile(filepath.Join(s.RunDir, "summary.md"), []byte(b.String()), 0o644)
}

// SaveResponse writes a model's response to the appropriate round directory
// (flat layout for one-shot mode).
func (s *Store) SaveResponse(round int, resp adapter.Response) error {
	dir := filepath.Join(s.RunDir, fmt.Sprintf("round-%d", round))
	filename := resp.Model + ".json"
	return s.writeJSON(filepath.Join(dir, filename), resp)
}

// SaveTurnResponse writes a model's response under turn-N/round-N
// (nested layout for interactive mode).
func (s *Store) SaveTurnResponse(turn, round int, resp adapter.Response) error {
	dir := filepath.Join(s.RunDir, fmt.Sprintf("turn-%d", turn), fmt.Sprintf("round-%d", round))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create %s: %w", dir, err)
	}
	filename := resp.Model + ".json"
	return s.writeJSON(filepath.Join(dir, filename), resp)
}

// SaveSummary writes a summary.md file at the end of the run.
func (s *Store) SaveSummary(meta RunMeta, rounds [][]adapter.Response) error {
	var b strings.Builder

	b.WriteString("# Tripartite Run Summary\n\n")
	fmt.Fprintf(&b, "**Prompt:** %s\n\n", meta.Prompt)
	fmt.Fprintf(&b, "**Models:** %s\n\n", strings.Join(meta.Models, ", "))
	fmt.Fprintf(&b, "**Timestamp:** %s\n\n", meta.Timestamp)
	b.WriteString("---\n\n")

	roundNames := []string{"Initial Response", "Cross-Review", "Synthesis"}
	for i, responses := range rounds {
		roundLabel := fmt.Sprintf("Round %d", i+1)
		if i < len(roundNames) {
			roundLabel += " — " + roundNames[i]
		}
		fmt.Fprintf(&b, "## %s\n\n", roundLabel)

		for _, resp := range responses {
			fmt.Fprintf(&b, "### %s (%.1fs)\n\n", resp.Model, resp.Duration.Seconds())
			if resp.Error != "" {
				fmt.Fprintf(&b, "**Error:** %s\n\n", resp.Error)
			}
			b.WriteString(resp.Content)
			b.WriteString("\n\n---\n\n")
		}
	}

	path := filepath.Join(s.RunDir, "summary.md")
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// SessionTurn holds a prompt and its round responses for session summary writing.
// This mirrors orchestrator.Turn but lives here to avoid circular imports.
type SessionTurn struct {
	Prompt    string
	Responses [][]adapter.Response
}

// SaveSessionSummary writes a summary.md for a multi-turn interactive session.
func (s *Store) SaveSessionSummary(meta RunMeta, turns []SessionTurn) error {
	var b strings.Builder

	b.WriteString("# Tripartite Session Summary\n\n")
	fmt.Fprintf(&b, "**Models:** %s\n\n", strings.Join(meta.Models, ", "))
	fmt.Fprintf(&b, "**Timestamp:** %s\n\n", meta.Timestamp)
	fmt.Fprintf(&b, "**Turns:** %d\n\n", len(turns))
	b.WriteString("---\n\n")

	roundNames := []string{"Initial Response", "Cross-Review", "Synthesis"}
	for ti, turn := range turns {
		fmt.Fprintf(&b, "# Turn %d\n\n", ti+1)
		fmt.Fprintf(&b, "**Prompt:** %s\n\n", turn.Prompt)

		for ri, responses := range turn.Responses {
			roundLabel := fmt.Sprintf("Round %d", ri+1)
			if ri < len(roundNames) {
				roundLabel += " — " + roundNames[ri]
			}
			fmt.Fprintf(&b, "## %s\n\n", roundLabel)

			for _, resp := range responses {
				fmt.Fprintf(&b, "### %s (%.1fs)\n\n", resp.Model, resp.Duration.Seconds())
				if resp.Error != "" {
					fmt.Fprintf(&b, "**Error:** %s\n\n", resp.Error)
				}
				b.WriteString(resp.Content)
				b.WriteString("\n\n---\n\n")
			}
		}
	}

	path := filepath.Join(s.RunDir, "summary.md")
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// MetaSessionTurn captures one turn of a meta session (either engine).
type MetaSessionTurn struct {
	Prompt                string               `json:"prompt"`
	Engine                string               `json:"engine"`                            // "brainstorm" or "delegate"
	Agent                 string               `json:"agent,omitempty"`                   // delegate agent name
	CycleID               string               `json:"cycle_id,omitempty"`                // cycle identifier
	CycleState            string               `json:"cycle_state,omitempty"`             // cycle final state
	DecisionAction        string               `json:"decision_action,omitempty"`         // selected decision gate action
	DecisionActionSummary string               `json:"decision_action_summary,omitempty"` // operator action result
	Responses             [][]adapter.Response `json:"responses,omitempty"`               // brainstorm rounds
	FinalText             string               `json:"final_text,omitempty"`              // delegate collected text
	Error                 string               `json:"error,omitempty"`                   // delegate/cycle error summary
}

// MetaSessionState captures resumable meta-session state.
type MetaSessionState struct {
	Turns         []MetaSessionTurn `json:"turns,omitempty"`
	AgentSessions map[string]string `json:"agent_sessions,omitempty"`
	UpdatedAt     string            `json:"updated_at,omitempty"`
}

// SaveMetaSessionSummary writes a summary.md for a meta session with mixed engine turns.
func (s *Store) SaveMetaSessionSummary(meta RunMeta, turns []MetaSessionTurn) error {
	var b strings.Builder

	b.WriteString("# Tripartite Meta Session Summary\n\n")
	fmt.Fprintf(&b, "**Models:** %s\n\n", strings.Join(meta.Models, ", "))
	fmt.Fprintf(&b, "**Timestamp:** %s\n\n", meta.Timestamp)
	fmt.Fprintf(&b, "**Turns:** %d\n\n", len(turns))
	b.WriteString("---\n\n")

	roundNames := []string{"Initial Response", "Cross-Review", "Synthesis"}
	for ti, turn := range turns {
		fmt.Fprintf(&b, "## Turn %d\n\n", ti+1)
		fmt.Fprintf(&b, "**Prompt:** %s\n\n", turn.Prompt)
		fmt.Fprintf(&b, "**Engine:** %s\n\n", turn.Engine)

		if turn.Engine == "brainstorm" && len(turn.Responses) > 0 {
			for ri, responses := range turn.Responses {
				roundLabel := fmt.Sprintf("Round %d", ri+1)
				if ri < len(roundNames) {
					roundLabel += " — " + roundNames[ri]
				}
				fmt.Fprintf(&b, "### %s\n\n", roundLabel)

				for _, resp := range responses {
					fmt.Fprintf(&b, "#### %s (%.1fs)\n\n", resp.Model, resp.Duration.Seconds())
					if resp.Error != "" {
						fmt.Fprintf(&b, "**Error:** %s\n\n", resp.Error)
					}
					b.WriteString(resp.Content)
					b.WriteString("\n\n---\n\n")
				}
			}
		}

		if turn.Engine == "delegate" {
			if turn.Agent != "" {
				fmt.Fprintf(&b, "**Agent:** %s\n\n", turn.Agent)
			}
			if turn.DecisionAction != "" {
				fmt.Fprintf(&b, "**Decision Action:** %s\n\n", turn.DecisionAction)
			}
			if turn.DecisionActionSummary != "" {
				fmt.Fprintf(&b, "**Decision Action Result:** %s\n\n", turn.DecisionActionSummary)
			}
			if turn.Error != "" {
				fmt.Fprintf(&b, "**Error:** %s\n\n", turn.Error)
			}
			if turn.FinalText != "" {
				b.WriteString(turn.FinalText)
				b.WriteString("\n\n---\n\n")
			}
		}

		if turn.Engine == "cycle" {
			if turn.CycleID != "" {
				fmt.Fprintf(&b, "**Cycle ID:** %s\n\n", turn.CycleID)
			}
			if turn.CycleState != "" {
				fmt.Fprintf(&b, "**Final State:** %s\n\n", turn.CycleState)
			}
			if turn.DecisionAction != "" {
				fmt.Fprintf(&b, "**Decision Action:** %s\n\n", turn.DecisionAction)
			}
			if turn.DecisionActionSummary != "" {
				fmt.Fprintf(&b, "**Decision Action Result:** %s\n\n", turn.DecisionActionSummary)
			}
			if turn.Error != "" {
				fmt.Fprintf(&b, "**Error:** %s\n\n", turn.Error)
			}
			if turn.FinalText != "" {
				b.WriteString(turn.FinalText)
				b.WriteString("\n\n---\n\n")
			}
		}
	}

	path := filepath.Join(s.RunDir, "summary.md")
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// SaveMetaSessionState persists resumable meta-session state.
func (s *Store) SaveMetaSessionState(state MetaSessionState) error {
	if state.UpdatedAt == "" {
		state.UpdatedAt = time.Now().Format(time.RFC3339)
	}
	return s.writeJSON(filepath.Join(s.RunDir, "meta_session_state.json"), state)
}

// LoadMetaSessionState loads previously persisted meta-session state.
func (s *Store) LoadMetaSessionState() (MetaSessionState, error) {
	var state MetaSessionState
	path := filepath.Join(s.RunDir, "meta_session_state.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return state, fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return state, fmt.Errorf("decode %s: %w", path, err)
	}
	return state, nil
}

func (s *Store) writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("json marshal: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

func (s *Store) metaTurnDelegateDir(turn int) (string, error) {
	if turn < 1 {
		turn = 1
	}
	dir := filepath.Join(s.RunDir, fmt.Sprintf("turn-%d", turn), "delegate")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("failed to create %s: %w", dir, err)
	}
	return dir, nil
}

func (s *Store) appendMetaTurnDelegateLine(turn int, filename string, line []byte) error {
	dir, err := s.metaTurnDelegateDir(turn)
	if err != nil {
		return err
	}
	return s.appendLine(filepath.Join(dir, filename), line)
}

// CycleCheckpoint captures cycle state at a point in time.
type CycleCheckpoint struct {
	CycleID    string        `json:"cycle_id"`
	State      string        `json:"state"`
	Timestamp  time.Time     `json:"timestamp"`
	EntryCount int           `json:"entry_count"`
	Elapsed    time.Duration `json:"elapsed"`
	Error      string        `json:"error,omitempty"`
}

// SaveCycleCheckpoint saves a cycle checkpoint to disk.
func (s *Store) SaveCycleCheckpoint(turnNum int, cp CycleCheckpoint) error {
	if turnNum < 1 {
		turnNum = 1
	}
	dir := filepath.Join(s.RunDir, fmt.Sprintf("turn-%d", turnNum), "cycle")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create %s: %w", dir, err)
	}
	filename := fmt.Sprintf("checkpoint-%s.json", cp.State)
	return s.writeJSON(filepath.Join(dir, filename), cp)
}

// SaveCycleTranscript saves the final cycle transcript to disk.
func (s *Store) SaveCycleTranscript(turnNum int, entries any) error {
	if turnNum < 1 {
		turnNum = 1
	}
	dir := filepath.Join(s.RunDir, fmt.Sprintf("turn-%d", turnNum), "cycle")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create %s: %w", dir, err)
	}
	return s.writeJSON(filepath.Join(dir, "transcript.json"), entries)
}

func (s *Store) appendLine(path string, line []byte) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	if _, err := f.Write(line); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if _, err := f.Write([]byte("\n")); err != nil {
		return fmt.Errorf("write newline %s: %w", path, err)
	}
	return nil
}
