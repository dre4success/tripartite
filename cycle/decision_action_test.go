package cycle

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHandleAwaitApprovalRunsDecisionActionOnApprove(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	broker := NewApprovalBroker()
	cc := newCycleContext(Config{
		Broker: broker,
		Guards: DefaultGuards(),
	})
	cc.state = StateAwaitApproval
	cc.currentPhase = phaseName(StateAwaitApproval)
	cc.decision = &DecisionPayload{}
	cc.resumeState = StateDone
	cc.decisionApproveAction = decisionActionAcceptResult
	cc.decisionDenyAction = decisionActionKeepProposal

	errCh := make(chan error, 1)
	go func() {
		errCh <- cc.handleAwaitApproval(ctx)
	}()

	ticket := waitPendingApprovalTicket(t, broker, 800*time.Millisecond)
	if err := broker.Resolve(ticket, true, ""); err != nil {
		t.Fatalf("Resolve approval: %v", err)
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("handleAwaitApproval returned error: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for approval handler")
	}

	last := cc.transcript.Last(KindDecisionAction)
	if last == nil {
		t.Fatal("missing decision_action transcript entry")
	}
	payload, ok := last.Payload.(DecisionActionPayload)
	if !ok {
		t.Fatalf("decision_action payload type = %T", last.Payload)
	}
	if payload.Action != decisionActionAcceptResult || !payload.Succeeded {
		t.Fatalf("decision_action payload = %#v", payload)
	}
}

func TestHandleAwaitApprovalRunsDecisionActionOnDeny(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	broker := NewApprovalBroker()
	cc := newCycleContext(Config{
		Broker: broker,
		Guards: DefaultGuards(),
	})
	cc.state = StateAwaitApproval
	cc.currentPhase = phaseName(StateAwaitApproval)
	cc.decision = &DecisionPayload{}
	cc.resumeState = StateDone
	cc.decisionApproveAction = decisionActionAcceptResult
	cc.decisionDenyAction = decisionActionKeepProposal

	errCh := make(chan error, 1)
	go func() {
		errCh <- cc.handleAwaitApproval(ctx)
	}()

	ticket := waitPendingApprovalTicket(t, broker, 800*time.Millisecond)
	if err := broker.Resolve(ticket, false, ""); err != nil {
		t.Fatalf("Resolve approval: %v", err)
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("handleAwaitApproval returned error: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for approval handler")
	}

	last := cc.transcript.Last(KindDecisionAction)
	if last == nil {
		t.Fatal("missing decision_action transcript entry")
	}
	payload, ok := last.Payload.(DecisionActionPayload)
	if !ok {
		t.Fatalf("decision_action payload type = %T", last.Payload)
	}
	if payload.Action != decisionActionKeepProposal || !payload.Succeeded {
		t.Fatalf("decision_action payload = %#v", payload)
	}
}

func TestHandleAwaitApprovalApplyWorktreeFFSuccess(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}

	repo, branch, featureSHA := setupDecisionTestRepo(t)
	restore := chdir(t, repo)
	defer restore()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	broker := NewApprovalBroker()
	cc := newCycleContext(Config{
		Broker: broker,
		Guards: DefaultGuards(),
	})
	cc.state = StateAwaitApproval
	cc.currentPhase = phaseName(StateAwaitApproval)
	cc.decision = &DecisionPayload{}
	cc.resumeState = StateDone
	cc.decisionApproveAction = decisionActionApplyWorktreeFF
	cc.worktreeInfo.Enabled = true
	cc.worktreeInfo.Branch = branch

	errCh := make(chan error, 1)
	go func() {
		errCh <- cc.handleAwaitApproval(ctx)
	}()

	ticket := waitPendingApprovalTicket(t, broker, 800*time.Millisecond)
	if err := broker.Resolve(ticket, true, ""); err != nil {
		t.Fatalf("Resolve approval: %v", err)
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("handleAwaitApproval returned error: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for approval handler")
	}

	head := strings.TrimSpace(runGitCommand(t, ctx, repo, "rev-parse", "HEAD"))
	if head != featureSHA {
		t.Fatalf("HEAD after action = %s, want %s", head, featureSHA)
	}

	last := cc.transcript.Last(KindDecisionAction)
	if last == nil {
		t.Fatal("missing decision_action transcript entry")
	}
	payload, ok := last.Payload.(DecisionActionPayload)
	if !ok {
		t.Fatalf("decision_action payload type = %T", last.Payload)
	}
	if payload.Action != decisionActionApplyWorktreeFF || !payload.Succeeded {
		t.Fatalf("decision_action payload = %#v", payload)
	}
	if payload.Branch != branch {
		t.Fatalf("decision_action branch = %q, want %q", payload.Branch, branch)
	}
}

func TestHandleAwaitApprovalApplyWorktreeFFFailure(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}

	repo, _, _ := setupDecisionTestRepo(t)
	restore := chdir(t, repo)
	defer restore()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	broker := NewApprovalBroker()
	cc := newCycleContext(Config{
		Broker: broker,
		Guards: DefaultGuards(),
	})
	cc.state = StateAwaitApproval
	cc.currentPhase = phaseName(StateAwaitApproval)
	cc.decision = &DecisionPayload{}
	cc.resumeState = StateDone
	cc.decisionApproveAction = decisionActionApplyWorktreeFF
	cc.worktreeInfo.Enabled = true
	cc.worktreeInfo.Branch = "feature/missing"

	errCh := make(chan error, 1)
	go func() {
		errCh <- cc.handleAwaitApproval(ctx)
	}()

	ticket := waitPendingApprovalTicket(t, broker, 800*time.Millisecond)
	if err := broker.Resolve(ticket, true, ""); err != nil {
		t.Fatalf("Resolve approval: %v", err)
	}

	var err error
	select {
	case err = <-errCh:
	case <-ctx.Done():
		t.Fatal("timeout waiting for approval handler")
	}
	if err == nil {
		t.Fatal("expected apply action to fail for missing branch")
	}

	last := cc.transcript.Last(KindDecisionAction)
	if last == nil {
		t.Fatal("missing decision_action transcript entry")
	}
	payload, ok := last.Payload.(DecisionActionPayload)
	if !ok {
		t.Fatalf("decision_action payload type = %T", last.Payload)
	}
	if payload.Action != decisionActionApplyWorktreeFF {
		t.Fatalf("decision_action action = %q, want %q", payload.Action, decisionActionApplyWorktreeFF)
	}
	if payload.Succeeded {
		t.Fatalf("decision_action payload = %#v, expected failed action", payload)
	}
	if payload.Error == "" {
		t.Fatalf("decision_action payload = %#v, expected error message", payload)
	}
}

func waitPendingApprovalTicket(t *testing.T, broker *ApprovalBroker, maxWait time.Duration) string {
	t.Helper()

	deadline := time.Now().Add(maxWait)
	for time.Now().Before(deadline) {
		pending := broker.Pending()
		if len(pending) > 0 {
			return pending[0].TicketID
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("expected pending approval ticket")
	return ""
}

func setupDecisionTestRepo(t *testing.T) (repo string, featureBranch string, featureSHA string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	repo = t.TempDir()
	runGitCommand(t, ctx, repo, "init")
	runGitCommand(t, ctx, repo, "config", "user.email", "test@example.com")
	runGitCommand(t, ctx, repo, "config", "user.name", "Tripartite Test")

	writeDecisionTestFile(t, filepath.Join(repo, "README.md"), "base\n")
	runGitCommand(t, ctx, repo, "add", "README.md")
	runGitCommand(t, ctx, repo, "commit", "-m", "base")

	baseBranch := strings.TrimSpace(runGitCommand(t, ctx, repo, "rev-parse", "--abbrev-ref", "HEAD"))
	featureBranch = "feature/test"
	runGitCommand(t, ctx, repo, "checkout", "-b", featureBranch)

	writeDecisionTestFile(t, filepath.Join(repo, "README.md"), "base\nfeature\n")
	runGitCommand(t, ctx, repo, "add", "README.md")
	runGitCommand(t, ctx, repo, "commit", "-m", "feature")
	featureSHA = strings.TrimSpace(runGitCommand(t, ctx, repo, "rev-parse", "HEAD"))
	runGitCommand(t, ctx, repo, "checkout", baseBranch)

	return repo, featureBranch, featureSHA
}

func runGitCommand(t *testing.T, ctx context.Context, repo string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repo}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
	}
	return string(out)
}

func writeDecisionTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func chdir(t *testing.T, dir string) func() {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir(%s): %v", dir, err)
	}
	return func() {
		if err := os.Chdir(old); err != nil {
			t.Fatalf("restore cwd to %s: %v", old, err)
		}
	}
}
