package logger

import (
	"bytes"
	"strings"
	"testing"
)

func TestDebugEnabled(t *testing.T) {
	var b bytes.Buffer
	log := newWithWriter(true, &b)

	log.Debug("debug message", "model", "codex")
	out := b.String()
	if !strings.Contains(out, "level=DEBUG") {
		t.Fatalf("expected DEBUG line, got %q", out)
	}
	if !strings.Contains(out, "debug message") {
		t.Fatalf("expected debug message, got %q", out)
	}
}

func TestDebugDisabled(t *testing.T) {
	var b bytes.Buffer
	log := newWithWriter(false, &b)

	log.Debug("debug message")
	if b.Len() != 0 {
		t.Fatalf("expected no debug output, got %q", b.String())
	}

	log.Warn("warn message")
	if !strings.Contains(b.String(), "warn message") {
		t.Fatalf("expected warn output, got %q", b.String())
	}
}

func TestNilSafeMethods(t *testing.T) {
	var log *Logger
	log.Debug("debug")
	log.Warn("warn")
	log.Error("error")
}
