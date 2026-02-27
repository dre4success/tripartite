package meta

import (
	"fmt"
	"strings"
	"time"

	"github.com/dre4success/tripartite/cycle"
)

type LiveCycleVerbosity string

const (
	LiveCycleOff     LiveCycleVerbosity = "off"
	LiveCycleCompact LiveCycleVerbosity = "compact"
	LiveCycleVerbose LiveCycleVerbosity = "verbose"
)

func ParseLiveCycleVerbosity(s string) (LiveCycleVerbosity, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", string(LiveCycleCompact):
		return LiveCycleCompact, nil
	case string(LiveCycleOff):
		return LiveCycleOff, nil
	case string(LiveCycleVerbose):
		return LiveCycleVerbose, nil
	default:
		return "", fmt.Errorf("invalid cycle live mode %q (want: off|compact|verbose)", s)
	}
}

type liveCycleUpdatePrinter struct {
	mode        LiveCycleVerbosity
	headerSig   string
	pendingSig  string
	activitySig string
	reviewSig   string
	boardPhase  string
	boardPass   int
	boardSeen   map[string]string
	errorSig    string
}

func (p *liveCycleUpdatePrinter) Next(snap *cycle.CycleStatus) []string {
	if snap == nil {
		return nil
	}
	if p.mode == LiveCycleOff {
		return nil
	}

	var lines []string

	headerSig := fmt.Sprintf("%s|%s|%d|%s|%d/%d|%d/%d|%d|%d",
		snap.State,
		snap.Phase,
		snap.Pass,
		snap.CurrentSubtask,
		snap.CompletedSubtasks,
		snap.TotalSubtasks,
		snap.RevisionCount,
		snap.MaxRevisions,
		snap.PendingApprovals,
		snap.PendingClarifications,
	)
	if headerSig != p.headerSig {
		p.headerSig = headerSig
		active := ""
		if snap.CurrentSubtask != "" {
			active = fmt.Sprintf(" active=%s", snap.CurrentSubtask)
		}
		lines = append(lines, fmt.Sprintf(
			"[cycle][live] state=%s phase=%s#%d subtasks=%d/%d revisions=%d/%d approvals=%d clarifications=%d%s",
			snap.State,
			snap.Phase,
			snap.Pass,
			snap.CompletedSubtasks,
			snap.TotalSubtasks,
			snap.RevisionCount,
			snap.MaxRevisions,
			snap.PendingApprovals,
			snap.PendingClarifications,
			active,
		))
	}
	pendingSig := fmt.Sprintf("%d|%d", snap.PendingApprovals, snap.PendingClarifications)
	if pendingSig != p.pendingSig {
		p.pendingSig = pendingSig
		if snap.PendingApprovals > 0 || snap.PendingClarifications > 0 {
			lines = append(lines, fmt.Sprintf(
				"[cycle][live] pending: approvals=%d (/approve|/deny), clarifications=%d (/clarify). Run /status for ticket IDs.",
				snap.PendingApprovals,
				snap.PendingClarifications,
			))
		}
	}

	if p.mode == LiveCycleVerbose && snap.LastTranscript.LastSummary != "" {
		agent := snap.LastTranscript.LastAgent
		if agent == "" {
			agent = "coordinator"
		}
		activitySig := fmt.Sprintf("%s|%s|%s", snap.LastTranscript.LastKind, agent, snap.LastTranscript.LastSummary)
		if activitySig != p.activitySig {
			p.activitySig = activitySig
			lines = append(lines, fmt.Sprintf(
				"[cycle][live] last=[%s][%s] %s",
				snap.LastTranscript.LastKind,
				agent,
				truncate(snap.LastTranscript.LastSummary, 120),
			))
		}
	}

	if rs := snap.CurrentReview; rs != nil {
		reviewSig := fmt.Sprintf("%s|%d|%d|%d|%d|%d", rs.Phase, rs.Pass, rs.Total, rs.Blockers, rs.Warns, rs.Infos)
		if reviewSig != p.reviewSig {
			p.reviewSig = reviewSig
			lines = append(lines, fmt.Sprintf(
				"[cycle][live] review=%s#%d findings=%d (blocker=%d warn=%d info=%d)",
				rs.Phase, rs.Pass, rs.Total, rs.Blockers, rs.Warns, rs.Infos,
			))
		}
	}

	if board := snap.CurrentBoard; board != nil && len(board.Items) > 0 {
		if p.boardSeen == nil {
			p.boardSeen = make(map[string]string)
		}
		if board.Phase != p.boardPhase || board.Pass != p.boardPass {
			p.boardPhase = board.Phase
			p.boardPass = board.Pass
			clear(p.boardSeen)
			if p.mode == LiveCycleVerbose {
				lines = append(lines, fmt.Sprintf("[cycle][live] board %s#%d", board.Phase, board.Pass))
			}
		}
		for _, item := range board.Items {
			key := fmt.Sprintf("%s|%s", item.Role, item.Agent)
			sig := fmt.Sprintf("%s|%s", item.Kind, item.Summary)
			if p.boardSeen[key] == sig {
				continue
			}
			p.boardSeen[key] = sig
			if p.mode == LiveCycleCompact && item.Role == "coordinator" && item.Kind == cycle.KindError {
				// Compact mode already emits error lines; avoid duplicate coordinator error board lines.
				continue
			}
			lines = append(lines, fmt.Sprintf(
				"[cycle][live]   [%s][%s][%s] %s",
				item.Role,
				item.Agent,
				item.Kind,
				truncate(item.Summary, 100),
			))
		}
	}

	if snap.LastError != "" {
		errSig := snap.LastError
		if errSig != p.errorSig {
			p.errorSig = errSig
			lines = append(lines, fmt.Sprintf("[cycle][live] error=%s", truncate(snap.LastError, 140)))
		}
	}

	return lines
}

func startCycleLiveWatcher(stop <-chan struct{}, sp *cycle.StatusProvider, mode LiveCycleVerbosity) {
	if sp == nil || mode == LiveCycleOff {
		return
	}
	go func() {
		ticker := time.NewTicker(400 * time.Millisecond)
		defer ticker.Stop()

		printer := liveCycleUpdatePrinter{mode: mode}
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				snap := sp.Snapshot()
				for _, line := range printer.Next(snap) {
					fmt.Println(line)
				}
			}
		}
	}()
}
