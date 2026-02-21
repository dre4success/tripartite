package workspace

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Info captures delegate worktree metadata for persistence.
type Info struct {
	Enabled      bool   `json:"enabled"`
	TaskID       string `json:"task_id,omitempty"`
	WorktreePath string `json:"worktree_path,omitempty"`
	Branch       string `json:"branch,omitempty"`
	BaseCommit   string `json:"base_commit,omitempty"`
}

// Commit captures commit metadata for delegated worktree output.
type Commit struct {
	SHA     string `json:"sha"`
	Subject string `json:"subject"`
}

// Prepare sets up an isolated worktree/branch for delegate execution.
func Prepare(ctx context.Context, repoRoot, taskID, agentName string) (Info, error) {
	if repoRoot == "" {
		return Info{}, fmt.Errorf("repo root is required")
	}
	if err := ensureGitRepo(ctx, repoRoot); err != nil {
		return Info{}, err
	}

	root := filepath.Join(repoRoot, ".tripartite", "worktrees")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return Info{}, fmt.Errorf("create worktree root: %w", err)
	}

	branch := fmt.Sprintf("tripartite/%s/%s", taskID, agentName)
	worktreePath := filepath.Join(root, fmt.Sprintf("%s-%s", taskID, agentName))
	cmd := exec.CommandContext(ctx, "git", "-C", repoRoot, "worktree", "add", "-b", branch, worktreePath, "HEAD")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return Info{}, fmt.Errorf("git worktree add failed: %v (%s)", err, stderr.String())
	}

	baseCommit, err := currentCommit(ctx, worktreePath)
	if err != nil {
		return Info{}, fmt.Errorf("resolve base commit: %w", err)
	}

	return Info{
		Enabled:      true,
		TaskID:       taskID,
		WorktreePath: worktreePath,
		Branch:       branch,
		BaseCommit:   baseCommit,
	}, nil
}

func ensureGitRepo(ctx context.Context, repoRoot string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", repoRoot, "rev-parse", "--is-inside-work-tree")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("not a git repo: %v (%s)", err, stderr.String())
	}
	return nil
}

// Inspect returns current worktree commit and commits introduced after baseCommit.
func Inspect(ctx context.Context, worktreePath, baseCommit string) (string, []Commit, error) {
	head, err := currentCommit(ctx, worktreePath)
	if err != nil {
		return "", nil, err
	}
	if baseCommit == "" || baseCommit == head {
		return head, nil, nil
	}

	cmd := exec.CommandContext(ctx, "git", "-C", worktreePath, "log", "--format=%H%x09%s", baseCommit+"..HEAD")
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", nil, fmt.Errorf("collect commit range failed: %v (%s)", err, stderr.String())
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	commits := make([]Commit, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		c := Commit{SHA: parts[0]}
		if len(parts) == 2 {
			c.Subject = parts[1]
		}
		commits = append(commits, c)
	}

	return head, commits, nil
}

func currentCommit(ctx context.Context, repoPath string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repoPath, "rev-parse", "HEAD")
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("resolve HEAD failed: %v (%s)", err, stderr.String())
	}
	return strings.TrimSpace(out.String()), nil
}
