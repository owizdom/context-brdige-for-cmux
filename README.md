# context-bridge

> Autonomous context sharing for AI coding agents — built for [cmux](https://github.com/manaflow-ai/cmux).

---

## The Problem Nobody Has Solved Yet

You're 45 minutes deep into a session with Claude Code. You've built out the auth middleware, made key decisions about RS256 vs HS256, hit and fixed three weird edge cases in the token refresh logic, and you're finally in flow.

Then you want to switch to Codex to handle the database layer. Or your teammate is using Gemini CLI and needs to pick up where you left off.

You open a new session. Blank slate. The new agent knows nothing.

So you write a summary. Copy-paste some files. Try to reconstruct the context. Lose 15 minutes re-explaining what happened. And even then, something always gets lost — a decision you made, an error you already debugged, a file you touched that the new agent doesn't know about.

**This is the context gap. Every developer hitting CLI coding agents faces it, and nobody has built the fix.**

---

## Why This Happens

The landscape of AI agent tooling splits cleanly into two worlds:

### World 1: Backend Orchestration Frameworks

These are built for engineers building agentic *products* — autonomous pipelines that run server-side.

| Tool | Context Handoff | Autonomous Sync | Reality |
|---|---|---|---|
| **LangGraph** | Graph state objects | No | Production-grade, but you're writing Python DAGs, not using CLI tools |
| **CrewAI** | Task outputs → next task | Partial (sequential only) | Great for pipelines, useless for terminal workflows |
| **Microsoft AutoGen** | Message-passing | No | Agents call each other as tools — no CLI agent support |
| **OpenAI Swarm** | `context_variables` dict | No (manual) | Best explicit handoff design, but intentionally educational |
| **Phidata / Agno** | SQLite memory/knowledge | Implicit | Database-backed memory — still requires a Python app wrapping it |
| **Semantic Kernel** | `ChatHistoryAgentThread` | No | Triage pattern only — forward requests, don't share full context |
| **BabyAGI** | Pinecone vector embeddings | YES (vector similarity) | Brilliant for task memory, but you're not running Pinecone for a terminal session |
| **SuperAGI** | Orchestrator delegation | Context-driven | Enterprise-focused, no CLI agent integration |

**Every single one of these requires you to write code that wraps the agents.** They're frameworks, not tools. None of them know what Claude Code is. None of them can read a terminal session.

### World 2: IDE-Bound Coding Agents

These have the best context sync — but they're locked inside an editor.

| Tool | Context Handoff | Autonomous Sync | Reality |
|---|---|---|---|
| **Cursor** | Shared codebase embedding index | YES — automatic | Best-in-class sync, but you're inside Cursor. Can't use Claude Code, Codex, etc. |
| **Windsurf (Cascade)** | Multi-file edit context | YES — Cascade maintains project state | Excellent agentic model, still IDE-bound |
| **Continue.dev** | Vectorized codebase + `@context` | Partial — needs manual `@` mentions | Open source, good, but not autonomous |
| **Cline / Roo Code** | MCP + `@file/@dir` | No (explicit selection) | Context limited to what you @-mark. Not autonomous. |

**Cursor and Windsurf are the best at this** — genuinely automatic context across sub-agents. But you're inside their walled garden. You can't use Claude Code's unique features, Codex's diff model, or Aider's git-native workflow alongside them.

### The Orphaned Category: CLI Coding Agents

This is where most serious developers actually live now.

| Tool | Context Handoff | Autonomous Sync |
|---|---|---|
| **Claude Code** | None | None |
| **Codex CLI** | None | None |
| **Gemini CLI** | None | None |
| **Aider** | None | None |

**Zero.** Every session starts from scratch. The entire category is blind to the session that came before it.

And the terminal multiplexer they all run inside — **tmux** — has zero AI awareness. It's a pane manager. It doesn't know what's running.

---

## cmux: The Right Foundation, Missing One Layer

[cmux](https://github.com/manaflow-ai/cmux) is the first terminal multiplexer built *specifically* for AI coding agents. It's a native macOS app on Ghostty that gives you:

- Rich, contextual notifications when Claude needs you (not generic macOS pings)
- GPU-accelerated split panes for running agents in parallel
- Session persistence with workspace metadata (git branch, CWD, PR info)
- A comprehensive Unix socket API for programmatic control of every pane
- Agent-aware tab management

cmux is, without exaggeration, the best environment for running multiple CLI coding agents. The community has recognized it — 1,900+ stars, active development, used daily by a growing number of professional developers.

But cmux's job is *orchestration UI*, not *context fabric*. And that's by design — it's the right abstraction boundary. Each agent session in cmux is isolated. They don't know about each other.

```
cmux today:
┌────────────────┬────────────────┬────────────────┐
│  Claude Code   │     Codex      │  Gemini CLI    │
│                │                │                │
│  "Working on   │  (blank slate) │  (blank slate) │
│   auth..."     │                │                │
│                │                │                │
│  context: ████ │  context: ░░░░ │  context: ░░░░ │
└────────────────┴────────────────┴────────────────┘
                     isolated
```

The socket API cmux exposes — specifically `debug.terminal.read_text` and `surface.send_text` — is the exact interface needed to bridge this gap. It just needed something to sit on top and use it.

---

## context-bridge

`context-bridge` is a lightweight daemon that runs alongside cmux and does one thing: **makes sure every AI agent session you open has the context it needs, automatically.**

```
cmux + context-bridge:
┌────────────────┬────────────────┬────────────────┐
│  Claude Code   │     Codex      │  Gemini CLI    │
│                │                │                │
│  "Working on   │◄───────────────│  "Picking up   │
│   auth..."     │  auto-inject   │   auth work"   │
│                │                │                │
│  context: ████ │  context: ████ │  context: ████ │
└────────────────┴────────────────┴────────────────┘
                   shared context
```

You open a new agent session in the same project. context-bridge detects it, reads the prior session, summarizes it via Claude Haiku, and injects a structured handoff brief before you type your first message. The new agent already knows:

- What task is in progress
- What files were changed and how
- Key decisions that were made
- Errors that were already hit and resolved
- Exactly where to start

No commands. No copy-pasting. No re-explaining. Just continuity.

---

## How It Works

```
┌─────────────────────────────────────────────────────────┐
│                         cmux                            │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐  │
│  │ Claude Code  │  │    Codex     │  │  Gemini CLI  │  │
│  └──────┬───────┘  └──────┬───────┘  └──────┬───────┘  │
└─────────┼─────────────────┼──────────────────┼──────────┘
          │    cmux socket API (read_text / send_text)
          ▼
┌─────────────────────────────────────────────────────────┐
│                  context-bridge daemon                  │
│                                                         │
│  Monitor ──► Parsers ──► Store ──► Summarizer           │
│  (polls    (per-agent  (SQLite)  (Claude Haiku API)     │
│   cmux)     adapters)                                   │
│                             │                           │
│                             ▼                           │
│                     Handoff Engine                      │
│                (auto-inject on new session)             │
└─────────────────────────────────────────────────────────┘
```

**Every 5 seconds**, the daemon:
1. Lists all active cmux workspaces and surfaces via the socket API
2. Reads terminal scrollback from each pane (`debug.terminal.read_text`)
3. Detects which agent is running (Claude Code, Codex, Gemini, Aider — each has a dedicated parser)
4. Extracts structured context: task goal, file changes, errors, key decisions
5. Persists to a local SQLite database

**When a new agent session opens** in the same project (matched by CWD or git branch):
1. The daemon detects it immediately
2. Fetches the most recent related context
3. Calls Claude Haiku to compress it into a handoff brief (~0.3 seconds, fractions of a cent)
4. Waits for the new agent to initialize (~3 seconds)
5. Injects the brief via `surface.send_text` — the new agent's first message is already full context

No user interaction required. You just open the session.

---

## Installation

**Prerequisites:** cmux running, Go 1.21+, Anthropic API key.

```bash
git clone https://github.com/your-handle/context-bridge
cd context-bridge
./install.sh
```

Or build manually:

```bash
go build -o bridge ./cmd/bridge
sudo mv bridge /usr/local/bin/bridge
```

---

## Usage

### 1. Start the daemon (once, leave it running)

```bash
export ANTHROPIC_API_KEY=sk-ant-...
bridge daemon
```

```
context-bridge daemon running (auto-inject: true). Press Ctrl+C to stop.
time=2026-02-26T10:00:00 level=INFO msg="new agent session detected" agent=claude-code surface=surf-abc workspace="my-project" goal="Add JWT auth to Express API"
time=2026-02-26T10:32:15 level=INFO msg="new agent session detected" agent=codex surface=surf-def workspace="my-project" goal=""
time=2026-02-26T10:32:18 level=INFO msg="auto-inject: found related session" from_agent=claude-code to_agent=codex goal="Add JWT auth to Express API"
time=2026-02-26T10:32:21 level=INFO msg="auto-inject: context injected" from=claude-code to=codex surface=surf-def
```

### 2. That's it

Open a new agent session in cmux. context-bridge handles the rest.

---

## Manual Handoff (when you want control)

```bash
# Hand off the most recent session to Codex
bridge handoff --to codex

# Hand off to Gemini with a specific instruction
bridge handoff --to gemini --note "focus only on the database layer"

# Hand off a specific session
bridge handoff --from sess-abc123 --to aider

# Hand off without opening a new tab (inject into existing pane)
bridge handoff --to claude-code --no-new-workspace
```

---

## Snapshots (save your place)

```bash
# Save before a risky refactor
bridge snapshot save --name before-auth-refactor

# Come back to it hours later, in any agent
bridge snapshot load --name before-auth-refactor --to codex

# Works across agents, across sessions, even across days
bridge snapshot load --name before-auth-refactor --to aider
```

---

## Status

```bash
bridge status
```

```
SESSION       AGENT        STATUS       WORKSPACE      GOAL
------------  -----------  -----------  -------------  ----------------------------------------
sess-a3f2...  claude-code  in_progress  my-project     Add JWT authentication to Express API
sess-b7c1...  gemini-cli   idle         my-project     Fix CSS layout on mobile
sess-e2d8...  aider        blocked      other-project  Refactor database connection pooling
```

---

## Debug / Watch Mode

```bash
bridge watch
```

```
Watching cmux sessions (Ctrl+C to stop)...
[10:00:05] NEW SESSION: agent=claude-code goal="Add JWT auth" cwd=/Users/dev/my-project
[10:00:10] UPDATE: agent=claude-code   status=in_progress  goal="Add JWT auth to Express API"
[10:00:15] UPDATE: agent=claude-code   status=in_progress  goal="Add JWT auth to Express API"
[10:32:15] NEW SESSION: agent=codex    goal=""              cwd=/Users/dev/my-project
[10:32:21] UPDATE: agent=codex         status=in_progress  goal="Add JWT auth to Express API"
```

---

## Configuration

```toml
# ~/.config/context-bridge/config.toml

[cmux]
socket_path = ""  # auto-reads CMUX_SOCKET_PATH

[bridge]
poll_interval_seconds = 5
max_scrollback_lines  = 300
db_path               = "~/.context-bridge/sessions.db"
log_level             = "warn"

[anthropic]
api_key    = ""   # or set ANTHROPIC_API_KEY
model      = "claude-haiku-4-5-20251001"
max_tokens = 2048
```

**No Anthropic API key?** context-bridge still works — it falls back to a structured summary built directly from the parsed session (file changes, task goal, errors). LLM summarization is an enhancement, not a requirement.

---

## What Gets Extracted

From every session, context-bridge builds a **Universal Context Format (UCF)** snapshot:

```json
{
  "session_id": "sess-surf-a3f2-1740563200000",
  "agent": "claude-code",
  "cwd": "/Users/dev/my-project",
  "git_branch": "feature/auth",
  "task": {
    "goal": "Add JWT authentication to the Express API",
    "status": "in_progress",
    "current_blocker": "Cannot read property 'verify' of undefined"
  },
  "file_changes": [
    { "path": "src/middleware/auth.js", "op": "created" },
    { "path": "src/routes/user.js",     "op": "modified" }
  ],
  "errors_encountered": [
    "Cannot read property 'verify' of undefined at middleware/auth.js:34"
  ],
  "summary": "TASK: Add JWT authentication...\nDONE:\n- Installed jsonwebtoken...",
  "conversation_excerpt": "...last 40 lines of scrollback..."
}
```

And the injected prompt looks like this (generated by Claude Haiku):

```
# Context Handoff from claude-code

You are continuing work started in a claude-code session.

Working directory: /Users/dev/my-project
Git branch: feature/auth

TASK: Add JWT authentication to the Express API
STATUS: in_progress
BLOCKER: Cannot read property 'verify' of undefined

DONE:
- Installed jsonwebtoken package
- Created auth middleware with RS256 token signing
- Added token refresh endpoint

FILES CHANGED:
- src/middleware/auth.js (created)
- src/routes/user.js (modified)

KEY DECISIONS:
- Using RS256, not HS256, for token signing
- 15-minute token expiry with refresh tokens

ERRORS SEEN:
- "Cannot read property 'verify' of undefined at middleware/auth.js:34"

NEXT STEP: Debug the verify() call — likely the jwks-rsa key hasn't loaded yet.

Please continue this work. Start by reading src/middleware/auth.js.
```

---

## Supported Agents

| Agent | Detection | File Change Extraction | Goal Extraction |
|---|---|---|---|
| Claude Code | Terminal patterns + `Human:`/`Assistant:` turns | `Edit()`, `Write()`, `Read()` tool calls | Last `Human:` turn |
| Codex | `codex>` prompt + "applying patch" lines | "applying patch: path" log lines | Last `user:` turn |
| Gemini CLI | `gemini>` prompt + `[user]` turns | File operation verbs in output | `[user]` turns |
| Aider | `aider>` prompt + "Applied edit to" | "Applied edit to" / "Created" lines | Last `aider>` line |
| Generic | Fallback | None | Last non-prompt line |

---

## The Comparison in Full

| Tool | For CLI Agents? | Auto Context Sync | Cross-Agent Handoff | No Code Required |
|---|---|---|---|---|
| LangGraph | No — Python framework | No | Limited (graph-based) | No |
| CrewAI | No — Python framework | Partial | Sequential only | No |
| AutoGen | No — Python framework | No | Via tool calls | No |
| OpenAI Swarm | No — Python framework | No (manual) | YES (best design) | No |
| Phidata/Agno | No — Python framework | Via SQLite memory | Yes | No |
| Semantic Kernel | No — SDK | No | Triage only | No |
| BabyAGI | No — Python framework | YES (vectors) | No | No |
| Cursor | No — IDE-bound | YES (auto) | YES (sub-agents) | Yes |
| Windsurf | No — IDE-bound | YES (Cascade) | YES (Cascade) | Yes |
| Continue.dev | No — IDE-bound | Partial (@mentions) | Yes | Partial |
| Cline / Roo Code | No — IDE-bound | No (manual @) | MCP-based | Partial |
| TmuxAI | Partial — observational | YES (observes panes) | No | Yes |
| tmux | Yes — but dumb | No | No | Yes |
| cmux (alone) | YES | No | No | Yes |
| **cmux + context-bridge** | **YES** | **YES (autonomous)** | **YES (cross-agent)** | **YES** |

context-bridge is the only tool that:
- Works with CLI coding agents (not IDE-bound, not a Python framework)
- Requires zero code changes to your workflow
- Syncs context autonomously without user intervention
- Supports handoff between different agent types
- Works offline (no LLM required for basic operation)

---

## Why cmux is the Right Foundation

cmux exposes exactly the right primitives via its Unix socket API:

```
debug.terminal.read_text  → read scrollback from any pane
surface.send_text         → inject text into any pane
workspace.create          → open a new tab programmatically
workspace.list            → enumerate all active workspaces
surface.list              → enumerate all panes in a workspace
notification.create       → send rich contextual notifications
```

This is everything context-bridge needs. No screen scraping, no process injection, no fragile hacks. The cmux team built a clean, stable, versioned API (v2 JSON-RPC over Unix socket) and context-bridge uses it exactly as intended.

cmux is a great *orchestration UI*. context-bridge is the *context fabric* that makes it complete.

---

## Architecture

```
context-bridge/
├── cmd/bridge/main.go               # CLI: daemon | status | handoff | snapshot | watch
├── internal/
│   ├── cmux/client.go               # JSON-RPC Unix socket client for cmux API
│   ├── parser/
│   │   ├── types.go                 # Universal Context Format (UCF) schema
│   │   ├── claude.go                # Claude Code scrollback parser
│   │   ├── codex.go                 # Codex parser
│   │   ├── gemini.go                # Gemini CLI parser
│   │   ├── aider.go                 # Aider parser
│   │   └── generic.go               # Fallback parser
│   ├── monitor/session.go           # Autonomous sync engine (polls cmux, fires auto-inject)
│   ├── store/sqlite.go              # UCF persistence in SQLite
│   ├── summarizer/claude_api.go     # Claude Haiku API for context compression
│   ├── handoff/engine.go            # Cross-agent injection orchestrator
│   └── config/config.go             # TOML config loader
└── config.toml                      # Default config
```

**Stack:** Go 1.21+, SQLite (`modernc.org/sqlite` — no CGo), Cobra CLI, TOML config. Single binary, zero runtime dependencies.

---

## The State of Agent Context (2026)

Here's what the industry has figured out about multi-agent context:

**Google's ADK** introduced explicit context slicing — semantic contracts for what transfers on handoff. **OpenAI Agents SDK** formalized the four primitives: Agents, Handoffs, Guardrails, Sessions. **Google's A2A protocol** proposed a JSON-RPC + SSE standard for agent-to-agent communication with explicit `AGENT_HANDOFF` events.

These are all real progress — for backend pipeline builders.

For developers who live in the terminal, running Claude Code and Codex and Gemini side by side in cmux splits — building actual products, not orchestration demos — none of this infrastructure exists. You're still writing manual summaries and copy-pasting context.

context-bridge is that infrastructure, built for the terminal, built for the agents developers actually use.

---

## Roadmap

- [ ] **OpenCode** (Vercel) adapter
- [ ] **Kiro** (AWS) adapter
- [ ] **MCP server mode** — expose context store as an MCP tool so agents can query it directly
- [ ] **Git-aware context** — track commits made during a session, surface as structured diffs
- [ ] **Watch mode broadcasting** — two agents on the same project get live context updates from each other
- [ ] **Context search** — semantic search across all stored sessions (`bridge search "JWT auth"`)
- [ ] **Team sync** — share a context store across machines (S3 / R2 backend)
- [ ] **Context compression** — sliding window that keeps only the last N meaningful events

---

## License

MIT — built to run alongside [cmux](https://github.com/manaflow-ai/cmux).
