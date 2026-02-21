package logger

import (
	"io"
	"log/slog"
	"os"
)

// Logger is a thin slog wrapper with debug gating and nil-safe methods.
type Logger struct {
	log   *slog.Logger
	debug bool
}

// New constructs a logger writing to stderr.
func New(debug bool) *Logger {
	return newWithWriter(debug, os.Stderr)
}

func newWithWriter(debug bool, w io.Writer) *Logger {
	level := slog.LevelWarn
	if debug {
		level = slog.LevelDebug
	}
	handler := slog.NewTextHandler(w, &slog.HandlerOptions{Level: level})
	return &Logger{
		log:   slog.New(handler),
		debug: debug,
	}
}

// Debug logs debug diagnostics when debug mode is enabled.
func (l *Logger) Debug(msg string, attrs ...any) {
	if l == nil || !l.debug {
		return
	}
	l.log.Debug(msg, attrs...)
}

// Warn logs warning diagnostics.
func (l *Logger) Warn(msg string, attrs ...any) {
	if l == nil {
		return
	}
	l.log.Warn(msg, attrs...)
}

// Error logs error diagnostics.
func (l *Logger) Error(msg string, attrs ...any) {
	if l == nil {
		return
	}
	l.log.Error(msg, attrs...)
}
