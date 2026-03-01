package main

import (
	"flag"
	"io"
	"testing"
)

func TestResolveMetaPrompt(t *testing.T) {
	t.Run("flag-first one-shot prompt", func(t *testing.T) {
		fs := flag.NewFlagSet("meta", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		_ = fs.Bool("cycle", true, "")
		if err := fs.Parse([]string{"--cycle=false", "ship this change"}); err != nil {
			t.Fatalf("parse flags: %v", err)
		}

		got, err := resolveMetaPrompt(nil, fs)
		if err != nil {
			t.Fatalf("resolveMetaPrompt() error = %v", err)
		}
		if got == nil || *got != "ship this change" {
			t.Fatalf("resolveMetaPrompt() = %v, want one-shot prompt", got)
		}
	})

	t.Run("explicit prompt with extra positional args errors", func(t *testing.T) {
		fs := flag.NewFlagSet("meta", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		if err := fs.Parse([]string{"extra"}); err != nil {
			t.Fatalf("parse flags: %v", err)
		}

		p := "already provided"
		if _, err := resolveMetaPrompt(&p, fs); err == nil {
			t.Fatal("expected error for extra positional args")
		}
	})

	t.Run("multiple positional args without explicit prompt errors", func(t *testing.T) {
		fs := flag.NewFlagSet("meta", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		if err := fs.Parse([]string{"one", "two"}); err != nil {
			t.Fatalf("parse flags: %v", err)
		}

		if _, err := resolveMetaPrompt(nil, fs); err == nil {
			t.Fatal("expected error for multiple positional args")
		}
	})

	t.Run("no positional args stays interactive", func(t *testing.T) {
		fs := flag.NewFlagSet("meta", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		_ = fs.Bool("cycle", true, "")
		if err := fs.Parse([]string{"--cycle=true"}); err != nil {
			t.Fatalf("parse flags: %v", err)
		}

		got, err := resolveMetaPrompt(nil, fs)
		if err != nil {
			t.Fatalf("resolveMetaPrompt() error = %v", err)
		}
		if got != nil {
			t.Fatalf("resolveMetaPrompt() = %q, want nil", *got)
		}
	})
}
