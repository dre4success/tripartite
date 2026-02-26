package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMergeBranchFF(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}

	repo := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	runGit(t, ctx, repo, "init")
	runGit(t, ctx, repo, "config", "user.email", "test@example.com")
	runGit(t, ctx, repo, "config", "user.name", "Tripartite Test")

	writeFile(t, filepath.Join(repo, "README.md"), "base\n")
	runGit(t, ctx, repo, "add", "README.md")
	runGit(t, ctx, repo, "commit", "-m", "base")

	baseBranch := strings.TrimSpace(runGit(t, ctx, repo, "rev-parse", "--abbrev-ref", "HEAD"))
	runGit(t, ctx, repo, "checkout", "-b", "feature/test")

	writeFile(t, filepath.Join(repo, "README.md"), "base\nfeature\n")
	runGit(t, ctx, repo, "add", "README.md")
	runGit(t, ctx, repo, "commit", "-m", "feature")
	featureSHA := strings.TrimSpace(runGit(t, ctx, repo, "rev-parse", "HEAD"))

	runGit(t, ctx, repo, "checkout", baseBranch)

	if err := MergeBranchFF(ctx, repo, "feature/test"); err != nil {
		t.Fatalf("MergeBranchFF returned error: %v", err)
	}

	head := strings.TrimSpace(runGit(t, ctx, repo, "rev-parse", "HEAD"))
	if head != featureSHA {
		t.Fatalf("HEAD after merge = %s, want %s", head, featureSHA)
	}
}

func TestMergeBranchFFValidation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := MergeBranchFF(ctx, "", "feature"); err == nil {
		t.Fatal("expected error for empty repoRoot")
	}
	if err := MergeBranchFF(ctx, t.TempDir(), "   "); err == nil {
		t.Fatal("expected error for empty branch")
	}
}

func runGit(t *testing.T, ctx context.Context, repo string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repo}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
	}
	return string(out)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
