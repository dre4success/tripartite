package preflight

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// CheckWorktreePrereqs ensures the current directory is a git repository before
// attempting delegate --worktree setup.
func CheckWorktreePrereqs(ctx context.Context, cwd string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", cwd, "rev-parse", "--is-inside-work-tree")
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("worktree preflight failed: not a git repo (%s)", strings.TrimSpace(stderr.String()))
	}

	if strings.TrimSpace(out.String()) != "true" {
		return fmt.Errorf("worktree preflight failed: directory is not inside a git work tree")
	}
	return nil
}
