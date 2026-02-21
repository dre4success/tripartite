package runner

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/dre4success/tripartite/adapter"
)

// ansiPattern matches ANSI escape sequences for stripping from output.
var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// StripANSI removes ANSI escape codes from a byte slice.
func StripANSI(b []byte) []byte {
	return ansiPattern.ReplaceAll(b, nil)
}

// Run executes the adapter's command with the given prompt. The timeout is a
// total budget shared across the initial attempt and one optional retry. This
// prevents a 120s timeout from becoming ~242s when a retry fires.
func Run(ctx context.Context, a adapter.Adapter, prompt string, timeout time.Duration, approval adapter.ApprovalLevel) adapter.Response {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	resp := attempt(ctx, a, prompt, approval)
	if resp.ExitCode != 0 && ctx.Err() == nil {
		// Retry once with a short backoff, respecting the timeout budget.
		select {
		case <-ctx.Done():
			return resp
		case <-time.After(2 * time.Second):
		}
		resp = attempt(ctx, a, prompt, approval)
	}
	return resp
}

func attempt(ctx context.Context, a adapter.Adapter, prompt string, approval adapter.ApprovalLevel) adapter.Response {

	start := time.Now()
	cmd := a.BuildCommand(prompt, approval)
	cmd.Env = cmd.Environ() // inherit current env

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Start + wait, respecting context cancellation.
	err := cmd.Start()
	if err != nil {
		return adapter.Response{
			Model:    a.Name(),
			Raw:      nil,
			Content:  "",
			Error:    fmt.Sprintf("failed to start: %v", err),
			Duration: time.Since(start),
			ExitCode: -1,
		}
	}

	// Wait in a goroutine so we can select on context.
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		raw := stdout.Bytes()
		cleaned := StripANSI(raw)
		content, parseErr := a.ParseResponse(cleaned)
		stderrText := strings.TrimSpace(string(StripANSI(stderr.Bytes())))
		errMsg := fmt.Sprintf("timeout/cancelled: %v", ctx.Err())
		if stderrText != "" {
			errMsg = fmt.Sprintf("%s | stderr: %s", errMsg, truncateForError(stderrText, 400))
		}
		if parseErr != nil {
			errMsg = fmt.Sprintf("%s | parse: %s", errMsg, truncateForError(parseErr.Error(), 200))
		}
		return adapter.Response{
			Model:    a.Name(),
			Raw:      raw,
			Content:  content,
			Error:    errMsg,
			Duration: time.Since(start),
			ExitCode: -1,
		}
	case err := <-done:
		raw := stdout.Bytes()
		cleaned := string(StripANSI(raw))
		exitCode := 0
		var errMsg string

		if err != nil {
			exitCode = cmd.ProcessState.ExitCode()
			errMsg = stderr.String()
		}

		// Parse response through the adapter.
		content, parseErr := a.ParseResponse([]byte(cleaned))
		if parseErr != nil && errMsg == "" {
			errMsg = parseErr.Error()
		}

		return adapter.Response{
			Model:    a.Name(),
			ModelID:  a.ExtractModel(raw),
			Raw:      raw,
			Content:  content,
			Error:    errMsg,
			Duration: time.Since(start),
			ExitCode: exitCode,
		}
	}
}

func truncateForError(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 || len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}
