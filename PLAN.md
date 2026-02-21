# Tripartite — Multi-LLM Orchestrator CLI

## What It Does

Tripartite sends prompts to three subscription-based AI CLIs (Claude Code, ChatGPT Codex, Gemini CLI), collects their responses, runs a cross-review round, then synthesizes a final answer. All via CLI subscriptions — no API keys.

Supports two modes:
- **One-shot**: `tripartite brainstorm -p "prompt"` — single prompt, 3 rounds, exit
- **Interactive**: `tripartite brainstorm` — REPL with ongoing conversation, full history across turns

## Architecture

```
tripartite brainstorm -p "design a REST API for ..."   # one-shot
tripartite brainstorm                                   # interactive REPL
       │
       ├── Preflight: check binaries, env vars
       │
       ├── [Interactive] Enter REPL loop — read prompt from stdin
       │
       ├── Round 1 (parallel): fan-out prompt (+ conversation history) to all 3 CLIs
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
       ├── [Interactive] Loop back for next prompt (/quit to exit)
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
│   └── orchestrator.go      # Round-based orchestration logic, history support
├── adapter/
│   ├── adapter.go           # Interface + common types
│   ├── claude.go            # Claude Code adapter
│   ├── codex.go             # Codex adapter
│   └── gemini.go            # Gemini CLI adapter
├── session/
│   └── session.go           # Interactive REPL loop, conversation history
├── preflight/
│   └── preflight.go         # Binary detection, env var enforcement
├── runner/
│   └── runner.go            # Subprocess execution, timeout, retry, ANSI stripping
└── store/
    └── store.go             # Run artifact persistence (./runs/<timestamp>/)
```

## CLI Usage

```bash
# One-shot brainstorm
./tripartite brainstorm -p "What is the best way to handle errors in Go?"

# Interactive session (REPL)
./tripartite brainstorm --models claude,codex,gemini

# Single model
./tripartite brainstorm -p "Design a REST API" --models claude

# Custom timeout
./tripartite brainstorm -p "Explain concurrency" --timeout 180s

# Allow API keys (don't fail if env vars are set)
./tripartite brainstorm -p "..." --allow-api-keys
```

### Interactive Commands

| Command | Action |
|---------|--------|
| `/quit`, `/exit` | End the session and save artifacts |
| `/history` | Show conversation turn count |

## CLI Flags

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `-p` | string | (omit for interactive) | The prompt to send |
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
- Each enabled model's binary exists on PATH (`exec.LookPath`)
- No blocked API key env vars are set (unless `--allow-api-keys`)
- At least 2 models must be available

Note: Preflight does **not** verify auth/login status — that's the operator's responsibility.

## Run Artifacts

Each run is persisted to `./runs/<timestamp>-<random>/`:

**One-shot mode:**
```
runs/2026-02-21T10-30-00-a1b2c3/
├── input.json           # Original prompt + config
├── round-1/
│   ├── claude.json
│   ├── codex.json
│   └── gemini.json
├── round-2/...
├── round-3/...
└── summary.md
```

**Interactive mode:**
```
runs/2026-02-21T10-30-00-a1b2c3/
├── input.json           # Session config (mode: "interactive")
├── turn-1/
│   ├── round-1/claude.json, codex.json, gemini.json
│   ├── round-2/...
│   └── round-3/...
├── turn-2/
│   ├── round-1/...
│   ├── round-2/...
│   └── round-3/...
└── summary.md
```

## v1 Scope

**IN**: brainstorm subcommand, 3 rounds, parallel execution, preflight checks, artifact persistence, terminal output.

**OUT**: No Cobra, no TUI library, no review/build subcommands, no git worktree mode, no patch application.

## Post-Review Fixes (from Antigravity + Codex reviews)

- [x] Resilient JSON parsing: all adapters now scan line-by-line in reverse via shared `ExtractJSON()` — handles CLI preamble (spinners, "Thinking...", warnings)
- [x] Shared timeout budget: `Run()` creates one `context.WithTimeout` shared across initial attempt + retry, so a 120s timeout can never become ~242s
- [x] Failed model filtering: rounds 2 and 3 only fan out to models that succeeded in the prior round, and only include successful responses in prompts
- [x] Run directory uniqueness: timestamp now includes a 3-byte random hex suffix to prevent collisions on concurrent/same-second runs

## Status

- [x] Project scaffold (git, go.mod, directory structure)
- [x] Adapter interface + three adapters (claude, codex, gemini)
- [x] Runner (subprocess execution, timeout, retry, ANSI stripping)
- [x] Preflight checks
- [x] Store (run artifact persistence)
- [x] Orchestrator (3-round flow)
- [x] main.go (CLI flags, subcommand routing)
- [x] Post-review hardening (JSON parsing, timeout, filtering, uniqueness)
- [x] Interactive mode (REPL session with conversation history)
- [ ] Testing with live CLIs
