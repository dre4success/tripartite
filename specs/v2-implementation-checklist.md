# Tripartite v2.0 Implementation Checklist

**Source spec:** `specs/v2-meta-agent.md`  
**Scope:** v2.0 only (streaming `delegate` + safe foundations)
**Date:** 2026-02-21

---

## 1. v2.0 Goals

- [ ] Add `tripartite delegate <agent> "<prompt>"` with live event streaming.
- [ ] Keep existing `brainstorm` behavior working.
- [ ] Persist replayable artifacts (normalized events + raw provider lines + stderr).
- [ ] Support opt-in workspace isolation via `--worktree` flag.
- [ ] Guarantee clean cancellation (Ctrl+C) with no orphan subprocesses.

---

## 2. Package Plan

### CLI Layer (`main.go`)
- [ ] Add `delegate` subcommand and flags:
  - [ ] `--model`, `--sandbox`, `--worktree`, `--timeout`, `--runs-dir`
- [ ] Route existing `brainstorm` path unchanged.
- [ ] Validate agent name and resolve adapter.

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
- [ ] Implement subprocess runner with:
  - [ ] `StdoutPipe` parsing loop
  - [ ] separate stderr capture/persist
  - [ ] scanner buffer size override for large JSON lines
  - [ ] context-aware cancel path
  - [ ] graceful interrupt then forced kill fallback
- [ ] Handle prompt transport by `PromptMode`.
- [ ] Preserve unknown/unparseable lines in raw logs.

### Display (`display/`)
- [ ] Add terminal renderer for normalized events.
- [ ] Show minimal useful event classes:
  - [ ] text
  - [ ] tool use/result
  - [ ] command
  - [ ] done/error

### Workspace Isolation (`workspace/` or `delegate/`) — opt-in via `--worktree`
- [ ] When `--worktree` is passed:
  - [ ] Create task-scoped worktree: `.tripartite/worktrees/<task-id>-<agent>/`
  - [ ] Create agent branch: `tripartite/<task-id>/<agent>`
  - [ ] Run delegate in isolated cwd.
  - [ ] Record branch/worktree/commit metadata in artifacts (`workspace.json`).
- [ ] When `--worktree` is omitted:
  - [ ] Run delegate in the current working directory (default for v2.0).
  - [ ] Skip worktree/branch creation; no `workspace.json` artifact.

### Store (`store/`)
- [ ] Add delegate run artifact schema:
  - [ ] `input.json`
  - [ ] `events.normalized.jsonl`
  - [ ] `events.raw.jsonl`
  - [ ] `stderr.log`
  - [ ] `summary.md`
  - [ ] `workspace.json` (worktree/branch/commits)
- [ ] Ensure partial artifacts are flushed on cancellation/failure.

### Preflight (`preflight/`)
- [ ] Add delegate preflight:
  - [ ] binary presence
  - [ ] workspace prerequisites (git repo/worktree support)
  - [ ] env-var warnings per provider policy

---

## 3. Behavior Contracts

### Prompt Transport
- [ ] `PromptArg` when safe and short.
- [ ] `PromptStdin` supported and tested.
- [ ] `PromptTempFile` fallback path defined.

### Event Fidelity
- [ ] Every parsed event includes provider raw payload.
- [ ] Unknown JSONL lines are not dropped.

### Cancellation
- [ ] Ctrl+C behavior:
  - [ ] send interrupt
  - [ ] wait grace period
  - [ ] hard kill if still running
  - [ ] persist partial output

### Safety
- [ ] When `--worktree` is active, no direct modifications to main branch.
- [ ] When `--worktree` is omitted, delegate runs in cwd (user accepts responsibility).
- [ ] Applying worktree results is manual for v2.0 (cherry-pick/merge). `tripartite apply` ships in v2.1.

---

## 4. Testing Checklist

### Unit
- [ ] Event parsing per agent (happy path + unknown fields).
- [ ] Prompt mode selection logic.
- [ ] Cancellation timing path.
- [ ] Store writes for normal + partial runs.

### Integration
- [ ] `delegate` with each agent in a real repo.
- [ ] Long prompt case (no argv overflow).
- [ ] Unknown event lines survive into raw artifacts.
- [ ] Ctrl+C leaves no orphan subprocesses.

### Regression
- [ ] Existing `brainstorm` still runs.
- [ ] Existing run artifact behavior remains valid.

---

## 5. Release Gates (v2.0)

- [ ] `tripartite delegate` works end-to-end for `claude`, `codex`, `gemini`.
- [ ] Live streaming visible in terminal.
- [ ] Artifacts are replayable and complete.
- [ ] `--worktree` isolation verified when flag is passed.
- [ ] No orphan processes after cancellation.
- [ ] README/docs updated with examples and limitations.

---

## 6. Explicitly Deferred (v2.1+)

- [ ] `tripartite apply <task-id>` subcommand (`--merge`, `--cherry-pick`, `--squash`, `--cleanup`)
- [ ] Worktree isolation as default (flip `--worktree` to on-by-default, add `--no-worktree`)
- [ ] PTY fallback implementation
- [ ] `tripartite models` runtime discovery
- [ ] Automatic crash failover to another agent
- [ ] Advanced sandbox normalization across providers
- [ ] Full interactive `/use` + inline delegate/brainstorm unification

