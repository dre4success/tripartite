# Tripartite

Tripartite is a **Meta-Agent CLI** that wraps around popular AI CLI tools ([Claude Code](https://docs.anthropic.com/en/docs/agents-and-tools/claude-code/overview), [OpenAI Codex](https://github.com/openai/codex-cli), and [Gemini CLI](https://github.com/google-gemini/gemini-cli)). It allows you to orchestrate multi-agent debates or delegate coding tasks to specific agents—all using your existing subscriptions, with zero API keys required.

## Installation

```bash
go install github.com/dre4success/tripartite@latest
```

Ensure you have installed and authenticated at least one of the underlying AI CLIs:
- `npm install -g @anthropic-ai/claude-code`
- `npm install -g @openai/codex-cli`
- `npm install -g @google/gemini-cli`

## Usage

Tripartite supports two primary subcommands: `delegate` and `brainstorm`.

### 1. Delegate Mode (v2.0)

Delegate a specific coding task to an individual agent. The agent's reasoning, tool use, and commands will be streamed back to your terminal in real-time.

```bash
# Delegate a task to Claude (defaults to sonnet)
tripartite delegate claude "fix the auth bug in login.go"

# Delegate to Codex using a specific model
tripartite delegate codex "write tests for the API" --model o3

# Delegate to Gemini with workspace isolation
tripartite delegate gemini "refactor the database layer" --worktree
```

**Options:**
- `--model`: The model ID or alias to use (e.g., `sonnet`, `o3`, `2.5-pro`).
- `--sandbox`: Controls safety levels (`safe`, `write`, `full`).
- `--worktree`: (Highly Recommended) Runs the agent in a safely isolated `git worktree` and isolated branch so your main code is never directly altered. 
- `--runs-dir`: Directory for run artifacts (default: `./runs`).
- `--timeout`: Maximum time the agent is allowed to run.

### 2. Brainstorm Mode 

Send a prompt to multiple agents in parallel, force them to cross-review each other's responses, and synthesize a final answer.

```bash
# One-shot debate across all 3 CLIs
tripartite brainstorm -p "Should we use Redis or Memcached for our caching layer?"

# Interactive REPL session
tripartite brainstorm
```

## Artifacts & Logs

Every run (both delegate and brainstorm) persists highly-detailed, replayable JSONL event logs and Markdown summaries into the `./runs/<timestamp>` directory. If an agent crashes or is interrupted (`Ctrl+C`), partial logs are still safely flushed.

## Current Limitations (v2.0)

- **Native Session Continuity**: In v2.0, `delegate` handles one-shot discrete tasks. Native thread ID continuation features (to maintain conversation memory within an agent without repeating context) is planned for v2.1.
- **Applying Worktrees**: The `--worktree` flag safely isolates code changes, but merging those changes back to your main branch is completely manual in v2.0. A `tripartite apply` command is coming in v2.1.
- **Failover**: If a delegated agent crashes mid-task, Tripartite will not automatically retry with a different agent. 
