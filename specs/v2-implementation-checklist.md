# Tripartite v2.0 Implementation Checklist

**Source spec:** `specs/v2-meta-agent.md`  
**Scope:** v2.0 only (streaming `delegate` + safe foundations)
**Date:** 2026-02-21
**Note:** Checked boxes in sections 1-3 indicate implementation landed; verification remains tracked in sections 4-5.

---

## 1. v2.0 Goals

- [x] Add `tripartite delegate <agent> "<prompt>"` with live event streaming.
- [x] Keep existing `brainstorm` behavior working.
- [x] Persist replayable artifacts (normalized events + raw provider lines + stderr).
- [x] Support opt-in workspace isolation via `--worktree` flag.
- [x] Guarantee clean cancellation (Ctrl+C) with no orphan subprocesses.

---

## 2. Package Plan

### CLI Layer (`main.go`)
- [x] Add `delegate` subcommand and flags:
  - [x] `--model`, `--sandbox`, `--worktree`, `--timeout`, `--runs-dir`
- [x] Route existing `brainstorm` path unchanged.
- [x] Validate agent name and resolve adapter.

### Agent Layer (`agent/`)
- [x] Create `Agent` interface (streaming-first):
  - [x] `Name`, `BinaryName`, `CheckInstalled`
  - [x] `SupportedModels`, `DefaultModel`
  - [x] `PromptMode()`
  - [x] `ContinuationArgs(sessionID)`
  - [x] `StreamCommand(...)`
  - [x] `ParseEvent(...)`
- [x] Define shared types:
  - [x] `PromptMode` (`arg|stdin|tempfile`)
  - [x] `StreamOpts` (includes `SessionID`)
  - [x] `Event` (`Type`, `Data`, `Raw`, metadata)
- [x] Implement agents:
  - [x] `agent/claude.go`
  - [x] `agent/codex.go`
  - [x] `agent/gemini.go`
- [x] Wire `agent/` into runtime (`delegate` subcommand) — deferred to CLI Layer step; package is scaffolding-only for now.

### Stream Runner (`stream/`)
- [x] Implement subprocess runner with:
  - [x] `StdoutPipe` parsing loop
  - [x] separate stderr capture/persist
  - [x] scanner buffer size override for large JSON lines
  - [x] context-aware cancel path
  - [x] graceful interrupt then forced kill fallback
- [x] Handle prompt transport by `PromptMode`.
- [x] Preserve unknown/unparseable lines in raw logs.

### Display (`display/`)
- [x] Add terminal renderer for normalized events.
- [x] Show minimal useful event classes:
  - [x] text
  - [x] tool use/result
  - [x] command
  - [x] done/error

### Workspace Isolation (`workspace/` or `delegate/`) — opt-in via `--worktree`
- [x] When `--worktree` is passed:
  - [x] Create task-scoped worktree: `.tripartite/worktrees/<task-id>-<agent>/`
  - [x] Create agent branch: `tripartite/<task-id>/<agent>`
  - [x] Run delegate in isolated cwd.
  - [x] Record branch/worktree/commit metadata in artifacts (`workspace.json`).
- [x] When `--worktree` is omitted:
  - [x] Run delegate in the current working directory (default for v2.0).
  - [x] Skip worktree/branch creation; no `workspace.json` artifact.

### Store (`store/`)
- [x] Add delegate run artifact schema:
  - [x] `input.json`
  - [x] `events.normalized.jsonl`
  - [x] `events.raw.jsonl`
  - [x] `stderr.log`
  - [x] `summary.md`
  - [x] `workspace.json` (worktree/branch/commits)
- [x] Ensure partial artifacts are flushed on cancellation/failure.

### Preflight (`preflight/`)
- [x] Add delegate preflight:
  - [x] binary presence
  - [x] workspace prerequisites (git repo/worktree support)
  - [x] env-var warnings per provider policy

---

## 3. Behavior Contracts

### Prompt Transport
- [x] `PromptArg` when safe and short.
- [ ] `PromptStdin` supported and tested.
- [x] `PromptTempFile` fallback path defined.

### Event Fidelity
- [x] Every parsed event includes provider raw payload.
- [x] Unknown JSONL lines are not dropped.

### Cancellation
- [x] Ctrl+C behavior:
  - [x] send interrupt
  - [x] wait grace period
  - [x] hard kill if still running
  - [x] persist partial output

### Safety
- [x] When `--worktree` is active, no direct modifications to main branch.
- [x] When `--worktree` is omitted, delegate runs in cwd (user accepts responsibility).
- [ ] Applying worktree results is manual for v2.0 (cherry-pick/merge). `tripartite apply` ships in v2.1.

---

## 4. Testing Checklist

### Unit
- [x] Event parsing per agent (happy path + unknown fields).
- [x] Prompt mode selection logic.
- [x] Cancellation timing path.
- [x] Store writes for normal + partial runs.

### Integration
- [x] `delegate` with each agent in a real repo.
- [x] Long prompt case (no argv overflow).
- [x] Unknown event lines survive into raw artifacts.
- [x] Ctrl+C leaves no orphan subprocesses.

### Regression
- [x] Existing `brainstorm` still runs.
- [x] Existing run artifact behavior remains valid.

---

## 5. Release Gates (v2.0)

- [x] `tripartite delegate` works end-to-end for `claude`, `codex`, `gemini`.
- [x] Live streaming visible in terminal.
- [x] Artifacts are replayable and complete.
- [x] `--worktree` isolation verified when flag is passed.
- [x] No orphan processes after cancellation.
- [x] README/docs updated with examples and limitations.

---

## 6. Explicitly Deferred (v2.1+)

- [ ] `tripartite apply <task-id>` subcommand (`--merge`, `--cherry-pick`, `--squash`, `--cleanup`)
- [ ] Worktree isolation as default (flip `--worktree` to on-by-default, add `--no-worktree`)
- [ ] PTY fallback implementation
- [ ] `tripartite models` runtime discovery
- [ ] Automatic crash failover to another agent
- [ ] Advanced sandbox normalization across providers
- [ ] Full interactive `/use` + inline delegate/brainstorm unification
