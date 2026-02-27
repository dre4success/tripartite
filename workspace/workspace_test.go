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
	t.Run("fast-forward success", func(t *testing.T) {
		repo := initTestRepo(t)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		runGit(t, ctx, repo, "checkout", "-b", "feature/ff")
		writeFile(t, filepath.Join(repo, "feature.txt"), "feature\n")
		runGit(t, ctx, repo, "add", "feature.txt")
		runGit(t, ctx, repo, "commit", "-m", "feature commit")
		featureHead := strings.TrimSpace(runGit(t, ctx, repo, "rev-parse", "HEAD"))

		runGit(t, ctx, repo, "checkout", defaultBranchName(t, ctx, repo))
		if err := MergeBranchFF(ctx, repo, "feature/ff"); err != nil {
			t.Fatalf("MergeBranchFF() error = %v", err)
		}

		mainHead := strings.TrimSpace(runGit(t, ctx, repo, "rev-parse", "HEAD"))
		if mainHead != featureHead {
			t.Fatalf("main HEAD = %s, want %s after ff merge", mainHead, featureHead)
		}
	})

	t.Run("non-fast-forward fails", func(t *testing.T) {
		repo := initTestRepo(t)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		mainBranch := defaultBranchName(t, ctx, repo)

		runGit(t, ctx, repo, "checkout", "-b", "feature/diverge")
		writeFile(t, filepath.Join(repo, "feature.txt"), "feature\n")
		runGit(t, ctx, repo, "add", "feature.txt")
		runGit(t, ctx, repo, "commit", "-m", "feature commit")
		runGit(t, ctx, repo, "checkout", mainBranch)
		writeFile(t, filepath.Join(repo, "main.txt"), "main\n")
		runGit(t, ctx, repo, "add", "main.txt")
		runGit(t, ctx, repo, "commit", "-m", "main commit")

		if err := MergeBranchFF(ctx, repo, "feature/diverge"); err == nil {
			t.Fatal("expected non-fast-forward merge to fail")
		}
	})

	t.Run("validates input", func(t *testing.T) {
		ctx := context.Background()
		if err := MergeBranchFF(ctx, "", "feature/x"); err == nil {
			t.Fatal("expected error for empty repo root")
		}
		if err := MergeBranchFF(ctx, "/tmp", ""); err == nil {
			t.Fatal("expected error for empty branch")
		}
	})
}

func initTestRepo(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "tripartite-workspace-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	runGit(t, ctx, dir, "init")
	runGit(t, ctx, dir, "config", "user.name", "Tripartite Test")
	runGit(t, ctx, dir, "config", "user.email", "tripartite@example.com")
	writeFile(t, filepath.Join(dir, "README.md"), "init\n")
	runGit(t, ctx, dir, "add", "README.md")
	runGit(t, ctx, dir, "commit", "-m", "initial commit")
	return dir
}

func defaultBranchName(t *testing.T, ctx context.Context, repo string) string {
	t.Helper()
	name := strings.TrimSpace(runGit(t, ctx, repo, "rev-parse", "--abbrev-ref", "HEAD"))
	if name == "" {
		return "main"
	}
	return name
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
