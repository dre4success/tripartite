# Tripartite — Multi-LLM Orchestrator CLI

## What It Does

Tripartite sends a single prompt to three subscription-based AI CLIs (Claude Code, ChatGPT Codex, Gemini CLI), collects their responses, runs a cross-review round, then synthesizes a final answer. All via CLI subscriptions — no API keys.

## Architecture

```
tripartite brainstorm -p "design a REST API for ..."
       │
       ├── Preflight: check binaries, auth, env vars
       │
       ├── Round 1 (parallel): fan-out prompt to all 3 CLIs
       │   ├── claude -p "$PROMPT" --output-format json
       │   ├── codex exec "$PROMPT" --json
       │   └── gemini -p "$PROMPT" --output-format json
       │
       ├── Round 2 (parallel): cross-review
       │   Each model gets: "Review these responses: [other two responses]"
       │
       ├── Round 3 (parallel): synthesis
       │   Each model gets: "Given initial responses + reviews, provide final synthesis"
       │
       └── Output: terminal display + ./runs/<timestamp>/ artifacts
```

## Project Structure

```
tripartite/
├── main.go                  # Entry point, flag parsing (stdlib flag)
├── go.mod
├── PLAN.md                  # This file
├── orchestrator/
│   └── orchestrator.go      # Round-based orchestration logic
├── adapter/
│   ├── adapter.go           # Interface + common types
│   ├── claude.go            # Claude Code adapter
│   ├── codex.go             # Codex adapter
│   └── gemini.go            # Gemini CLI adapter
├── preflight/
│   └── preflight.go         # Binary detection, auth checks, env var enforcement
├── runner/
│   └── runner.go            # Subprocess execution, timeout, retry, ANSI stripping
└── store/
    └── store.go             # Run artifact persistence (./runs/<timestamp>/)
```

## CLI Usage

```bash
# Basic brainstorm
./tripartite brainstorm -p "What is the best way to handle errors in Go?"

# Single model
./tripartite brainstorm -p "Design a REST API" --models claude

# Custom timeout
./tripartite brainstorm -p "Explain concurrency" --timeout 180s

# Allow API keys (don't fail if env vars are set)
./tripartite brainstorm -p "..." --allow-api-keys
```

## CLI Flags

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `-p` | string | required | The prompt to send |
| `--timeout` | duration | 120s | Per-model execution timeout |
| `--allow-api-keys` | bool | false | Don't fail if API key env vars are set |
| `--models` | string | "claude,codex,gemini" | Comma-separated list of models to use |
| `--runs-dir` | string | "./runs" | Directory for run artifacts |

## Adapters

Each adapter wraps a CLI tool:

| Model | Binary | Command Pattern | Blocked Env Vars |
|-------|--------|-----------------|------------------|
| Claude | `claude` | `claude -p "<prompt>" --output-format json` | `ANTHROPIC_API_KEY` |
| Codex | `codex` | `codex exec "<prompt>" --json` | `CODEX_API_KEY`, `OPENAI_API_KEY` |
| Gemini | `gemini` | `gemini -p "<prompt>" --output-format json` | `GEMINI_API_KEY`, `GOOGLE_API_KEY` |

## Three-Round Flow

1. **Round 1 — Initial**: Send the user's prompt to all models in parallel. Collect responses.
2. **Round 2 — Cross-review**: Each model reviews the other two models' responses. Parallel.
3. **Round 3 — Synthesis**: Each model gets all initial responses + all reviews, asked to synthesize a final answer. Parallel.

## Preflight Checks

Before running, the system verifies:
- Each enabled model's binary exists on PATH
- Auth is configured (lightweight version check)
- No blocked API key env vars are set (unless `--allow-api-keys`)
- At least 2 models must be available

## Run Artifacts

Each run is persisted to `./runs/<timestamp>/`:
```
runs/2026-02-21T10-30-00/
├── input.json           # Original prompt + config
├── round-1/
│   ├── claude.json
│   ├── codex.json
│   └── gemini.json
├── round-2/
│   ├── claude.json
│   ├── codex.json
│   └── gemini.json
├── round-3/
│   ├── claude.json
│   ├── codex.json
│   └── gemini.json
└── summary.md
```

## v1 Scope

**IN**: brainstorm subcommand, 3 rounds, parallel execution, preflight checks, artifact persistence, terminal output.

**OUT**: No Cobra, no TUI library, no review/build subcommands, no git worktree mode, no patch application.

## Status

- [x] Project scaffold (git, go.mod, directory structure)
- [x] Adapter interface + three adapters (claude, codex, gemini)
- [x] Runner (subprocess execution, timeout, retry, ANSI stripping)
- [x] Preflight checks
- [x] Store (run artifact persistence)
- [x] Orchestrator (3-round flow)
- [x] main.go (CLI flags, subcommand routing)
- [ ] Testing with live CLIs
