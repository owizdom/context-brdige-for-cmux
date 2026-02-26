# Twitter Thread — context-bridge launch

> Tag: @lawrencecchen (cmux founder)

---

**Tweet 1**

Imagine a brilliant engineer who loses all memory of their work every time they close a tab.

That's every CLI coding agent session.

Claude, Codex, Gemini, Aider — switch between them mid-task and you're re-explaining everything from scratch.

I built the fix 🧵

---

**Tweet 2**

Every major orchestrator (LangGraph, CrewAI, AutoGen, Swarm) makes you write Python around agents. They don't know what a terminal is.

Cursor/Windsurf auto-sync context — but you're trapped in their IDE.

CLI coding agents? Zero cross-agent context. Completely orphaned.

---

**Tweet 3**

.@lawrencecchen built cmux — the only multiplexer built for AI agents.

Its socket API is exactly what I needed:

debug.terminal.read_text → read any pane's scrollback
surface.send_text → inject text into any pane

Perfect foundation for context-bridge.

---

**Tweet 4**

context-bridge is a Go daemon:

→ polls every pane every 5s via cmux socket API
→ detects the agent running (Claude, Codex, Gemini, Aider)
→ extracts task goal, file changes, errors
→ stores in SQLite

New session in same project → auto-injects context. Zero commands.

---

**Tweet 5**

The new agent's first message is already pre-loaded:

TASK: Add JWT auth to Express API
STATUS: in_progress
FILES: src/middleware/auth.js (created)
BLOCKER: verify() call failing
NEXT: Debug jwks-rsa key load

All extracted from the prior session. No copy-paste.

---

**Tweet 6**

cmux is the best orchestration UI for CLI agents — nothing comes close.

context-bridge is the context layer it was missing.

Go, single binary, SQLite, works without an API key.

Open sourcing it soon.

Thanks for building the right foundation, @lawrencecchen 🔥
