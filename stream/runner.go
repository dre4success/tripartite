package stream

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/dre4success/tripartite/agent"
)

// Callbacks receives streamed data from an agent subprocess.
type Callbacks struct {
	OnEvent      func(agent.Event)
	OnRawLine    func([]byte)
	OnStderrLine func([]byte)
	OnParseError func([]byte, error)
}

// Run executes an agent command and streams events/callbacks in real time.
func Run(ctx context.Context, a agent.Agent, prompt string, opts agent.StreamOpts, cb Callbacks) error {
	mode := a.PromptMode()
	promptArg := prompt
	var cleanupPromptFile func()
	if mode == agent.PromptTempFile {
		path, cleanup, err := writePromptFile(prompt)
		if err != nil {
			return err
		}
		promptArg = path
		cleanupPromptFile = cleanup
	}
	if cleanupPromptFile != nil {
		defer cleanupPromptFile()
	}

	cmd := a.StreamCommand(promptArg, opts)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}

	var stdin io.WriteCloser
	if mode == agent.PromptStdin {
		stdin, err = cmd.StdinPipe()
		if err != nil {
			return fmt.Errorf("stdin pipe: %w", err)
		}
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start: %w", err)
	}

	if stdin != nil {
		go func() {
			_, _ = io.WriteString(stdin, prompt)
			_ = stdin.Close()
		}()
	}

	stdoutLines := make(chan []byte, 128)
	stdoutErr := make(chan error, 1)
	go scanLines(stdout, stdoutLines, stdoutErr)

	stderrLines := make(chan []byte, 128)
	stderrErr := make(chan error, 1)
	go scanLines(stderr, stderrLines, stderrErr)

	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()
	waitDoneCh := (<-chan error)(waitDone)
	ctxDoneCh := ctx.Done()

	var procErr error
	var procDone, stdoutClosed, stderrClosed bool
	for {
		if procDone && stdoutClosed && stderrClosed {
			return procErr
		}

		select {
		case <-ctxDoneCh:
			// cancelProcess waits for cmd.Wait() and consumes waitDone; after this arm,
			// termination relies on stdout/stderr channel closure to return.
			procErr = cancelProcess(cmd, waitDoneCh)
			ctxDoneCh = nil
			waitDoneCh = nil
			procDone = true
		case err := <-waitDoneCh:
			procErr = err
			waitDoneCh = nil
			ctxDoneCh = nil
			procDone = true
		case line, ok := <-stdoutLines:
			if !ok {
				stdoutClosed = true
				stdoutLines = nil
				if err := <-stdoutErr; err != nil && procErr == nil && !procDone {
					procErr = fmt.Errorf("stdout scan: %w", err)
				}
				continue
			}
			if cb.OnRawLine != nil {
				cb.OnRawLine(line)
			}
			ev, err := a.ParseEvent(line)
			if err != nil {
				if errors.Is(err, agent.ErrSkipEvent) {
					continue
				}
				if cb.OnParseError != nil {
					cb.OnParseError(line, err)
				}
				continue
			}
			if cb.OnEvent != nil {
				cb.OnEvent(ev)
			}
		case line, ok := <-stderrLines:
			if !ok {
				stderrClosed = true
				stderrLines = nil
				if err := <-stderrErr; err != nil && procErr == nil && !procDone {
					procErr = fmt.Errorf("stderr scan: %w", err)
				}
				continue
			}
			if cb.OnStderrLine != nil {
				cb.OnStderrLine(line)
			}
		}
	}
}

func writePromptFile(prompt string) (string, func(), error) {
	f, err := os.CreateTemp("", "tripartite-prompt-*.txt")
	if err != nil {
		return "", nil, fmt.Errorf("create prompt temp file: %w", err)
	}
	cleanup := func() {
		_ = os.Remove(f.Name())
	}
	if _, err := io.WriteString(f, prompt); err != nil {
		_ = f.Close()
		cleanup()
		return "", nil, fmt.Errorf("write prompt temp file: %w", err)
	}
	if err := f.Close(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("close prompt temp file: %w", err)
	}
	return f.Name(), cleanup, nil
}

func scanLines(r io.Reader, out chan<- []byte, outErr chan<- error) {
	defer close(out)
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		out <- line
	}
	outErr <- scanner.Err()
}

func cancelProcess(cmd *exec.Cmd, waitDone <-chan error) error {
	if cmd.Process == nil {
		return context.Canceled
	}
	_ = cmd.Process.Signal(os.Interrupt)
	select {
	case err := <-waitDone:
		if err != nil {
			return err
		}
		return context.Canceled
	case <-time.After(3 * time.Second):
		_ = cmd.Process.Kill()
		err := <-waitDone
		if err != nil {
			return err
		}
		return context.Canceled
	}
}
