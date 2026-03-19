# Mnemo

> *From Mnemosyne (Μνημοσύνη), the Greek goddess of memory — mother of all the Muses. Without memory, there is no creation.*

**Every AI coding agent loses its memory the moment you close the tab.**

Claude Code, Codex, Gemini CLI, Aider — they're all brilliant at generating code, but every single one starts from zero. Switch agents mid-task? Re-explain everything. Close a session? Gone. Your teammate opens the same project? Blank slate.

Mnemo is the **context layer that sits between your terminal and your agents**. It watches what they do, extracts what matters, and automatically injects that context into the next session. Cross-agent, cross-session, zero commands.

No framework to learn. No code to write. No IDE to switch to. Just a single binary daemon that makes your existing workflow remember.

**Agent sessions should have memory. Right now they don't. Mnemo fixes that.**

---

## How It Works

```
┌─────────────────────────────────────────────────────────┐
│            Terminal Multiplexer (tmux / cmux)            │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐  │
│  │ Claude Code  │  │    Codex     │  │  Gemini CLI  │  │
│  └──────┬───────┘  └──────┬───────┘  └──────┬───────┘  │
└─────────┼─────────────────┼──────────────────┼──────────┘
          │     terminal.Backend interface
          ▼     (ReadScrollback / SendText / ListSessions)
┌─────────────────────────────────────────────────────────┐
│                      mnemo daemon                        │
│                                                          │
│  Monitor ──► Parsers ──► Store ──► Summarizer            │
│  (polls     (per-agent   (SQLite)  (Claude Haiku API)    │
│   backend)   adapters)                                   │
│                              │                           │
│                              ▼                           │
│                      Handoff Engine                      │
│                 (auto-inject on new session)              │
└──────────────────────────────────────────────────────────┘
```

**Every 5 seconds**, the daemon:
1. Lists all active panes via your terminal multiplexer
2. Reads scrollback from each pane
3. Detects which agent is running (Claude Code, Codex, Gemini, Aider — each has a dedicated parser)
4. Extracts structured context: task goal, file changes, errors, key decisions
5. Persists to a local SQLite database

**When a new agent session opens** in the same project:
1. Mnemo detects it immediately
2. Fetches the most recent related context
3. Compresses it into a handoff brief via Claude Haiku (~0.3s, fractions of a cent)
4. Injects the brief — the agent's first message is already full context

No user interaction required. You just open the session.

```
Before Mnemo:
┌────────────────┬────────────────┬────────────────┐
│  Claude Code   │     Codex      │  Gemini CLI    │
│                │                │                │
│  "Working on   │  (blank slate) │  (blank slate) │
│   auth..."     │                │                │
│  context: ████ │  context: ░░░░ │  context: ░░░░ │
└────────────────┴────────────────┴────────────────┘

After Mnemo:
┌────────────────┬────────────────┬────────────────┐
│  Claude Code   │     Codex      │  Gemini CLI    │
│                │                │                │
│  "Working on   │◄── auto ──────│  "Picking up   │
│   auth..."     │    inject      │   auth work"   │
│  context: ████ │  context: ████ │  context: ████ │
└────────────────┴────────────────┴────────────────┘
```

---

## Supported Backends

Mnemo talks to your terminal multiplexer through a `Backend` interface. It auto-detects which one you're running.

| Backend | How It Works | Auto-Detected Via |
|---|---|---|
| **tmux** | `capture-pane` / `send-keys` / `list-panes` | `$TMUX` env var |
| **cmux** | Unix socket JSON-RPC API | `$CMUX_SOCKET_PATH` env var |

Adding a new backend (Zellij, WezTerm, etc.) = one file implementing the interface. The parsers, store, and handoff engine don't know or care what multiplexer is running.

```bash
# Auto-detect (default)
mnemo daemon

# Or force a backend
mnemo daemon --backend tmux
mnemo daemon --backend cmux
```

---

## Installation

**Prerequisites:** tmux or cmux, Go 1.24+. Anthropic API key optional.

```bash
git clone https://github.com/your-handle/mnemo
cd mnemo
make install
```

Or manually:

```bash
go build -o mnemo .
sudo mv mnemo /usr/local/bin/mnemo
```

---

## Usage

### Start the daemon

```bash
export ANTHROPIC_API_KEY=sk-ant-...   # optional — works without it
mnemo daemon
```

```
mnemo daemon running (backend: tmux, auto-inject: true). Press Ctrl+C to stop.
```

Open a new agent session. Mnemo handles the rest.

### Manual handoff

```bash
mnemo handoff --to codex
mnemo handoff --to gemini --note "focus only on the database layer"
mnemo handoff --from sess-abc123 --to aider
mnemo handoff --to claude-code --no-new-workspace
```

### Snapshots

```bash
# Save before a risky refactor
mnemo snapshot save --name before-auth-refactor

# Restore hours later, into any agent
mnemo snapshot load --name before-auth-refactor --to codex
```

### Status

```bash
mnemo status
```

```
SESSION       AGENT        STATUS       WORKSPACE      GOAL
-------       -----        ------       ---------      ----
sess-a3f2...  claude-code  in_progress  my-project     Add JWT authentication to Express API
sess-b7c1...  gemini-cli   idle         my-project     Fix CSS layout on mobile
sess-e2d8...  aider        blocked      other-project  Refactor database connection pooling
```

### Watch

```bash
mnemo watch
```

```
Watching sessions via tmux (Ctrl+C to stop)...
[10:00:05] NEW SESSION: agent=claude-code goal="Add JWT auth" cwd=/Users/dev/my-project
[10:32:15] NEW SESSION: agent=codex    goal=""              cwd=/Users/dev/my-project
[10:32:21] UPDATE: agent=codex         status=in_progress  goal="Add JWT auth to Express API"
```

### Diagnose

```bash
mnemo diagnose
```

---

## What Gets Extracted

From every session, Mnemo builds a **Universal Context Format (UCF)** snapshot:

```json
{
  "session_id": "sess-a3f2-1740563200000",
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
  ]
}
```

The injected handoff prompt:

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

| Agent | Detection | File Changes | Goal Extraction |
|---|---|---|---|
| Claude Code | `Human:`/`Assistant:` turns | `Edit()`, `Write()`, `Read()` calls | Last `Human:` turn |
| Codex | `codex>` prompt | "applying patch: path" lines | Last `user:` turn |
| Gemini CLI | `gemini>` prompt | File operation verbs | `[user]` turns |
| Aider | `aider>` prompt | "Applied edit to" / "Created" | Last `aider>` line |
| Generic | Fallback | None | Last non-prompt line |

Adding a new parser = one file implementing `Detect` + `Parse` + `AgentType`.

---

## Configuration

```toml
# ~/.config/mnemo/config.toml

[cmux]
socket_path = ""  # only needed for cmux backend

[bridge]
poll_interval_seconds = 5
max_scrollback_lines  = 300
db_path               = "~/.mnemo/sessions.db"
log_level             = "warn"

[anthropic]
api_key    = ""   # or set ANTHROPIC_API_KEY
model      = "claude-haiku-4-5-20251001"
max_tokens = 2048
```

No API key? Mnemo still works — it falls back to structured summaries built from parsed session data. LLM summarization makes handoffs richer, but isn't required.

---

## Architecture

```
mnemo/
├── main.go                          # Entry point
├── cmd/                             # CLI (one file per command)
│   ├── root.go
│   ├── daemon.go
│   ├── status.go
│   ├── handoff.go
│   ├── snapshot.go
│   ├── watch.go
│   ├── diagnose.go
│   └── version.go
├── terminal/                        # Backend abstraction
│   ├── backend.go                   # Backend interface
│   ├── cmux.go                      # cmux (Unix socket JSON-RPC)
│   ├── tmux.go                      # tmux (capture-pane / send-keys)
│   └── detect.go                    # Auto-detect multiplexer
├── parser/                          # Agent parsers
│   ├── context.go                   # Universal Context Format types
│   ├── detect.go                    # Parser interface + DetectAgent
│   ├── claude.go
│   ├── codex.go
│   ├── gemini.go
│   ├── aider.go
│   ├── generic.go
│   ├── helpers.go
│   └── parser_test.go
├── monitor/monitor.go               # Poll loop + session tracking
├── handoff/handoff.go               # Injection orchestrator
├── store/                           # SQLite persistence
│   ├── sqlite.go
│   └── store_test.go
├── summarizer/summarizer.go         # Claude Haiku API
├── config/config.go                 # TOML config
├── Makefile
└── config.example.toml
```

**Stack:** Go 1.24, SQLite (pure Go, no CGo), Cobra, TOML. Single binary, zero runtime dependencies.

---

## Why Not X?

| Tool | Problem |
|---|---|
| LangGraph, CrewAI, AutoGen | Python frameworks — require you to write code wrapping agents. None know what a terminal is. |
| Cursor, Windsurf | Great context sync, but you're locked in their IDE. Can't use Claude Code, Codex, or Aider. |
| tmux / cmux alone | Pane managers. They don't know what's running inside them. |
| Copy-paste | Doesn't scale. You always forget something. |

Mnemo is the only tool that gives CLI coding agents cross-session, cross-agent memory with zero workflow changes.

---

## Roadmap

- [ ] **MCP server mode** — expose the context store as an MCP tool so any agent can query session history directly, not just at handoff time
- [ ] **Git-aware context** — track commits made during a session and surface structured diffs as part of the handoff, not just file paths
- [ ] **Context search** — semantic search across all stored sessions (`mnemo search "JWT auth"`) so you can pull context from days ago, not just the last session
- [ ] **Persistent agent profiles** — learn how each agent type uses context best and tailor injection format per agent (Claude wants markdown, Codex wants diffs, Aider wants file lists)
- [ ] **Team sync** — shared context store across machines so handoffs work across developers, not just across your own tabs

---

## License

MIT
