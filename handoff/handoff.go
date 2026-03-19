// Package handoff orchestrates cross-agent context injection.
package handoff

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/context-bridge/bridge/parser"
	"github.com/context-bridge/bridge/store"
	"github.com/context-bridge/bridge/summarizer"
	"github.com/context-bridge/bridge/terminal"
)

// AgentCommand maps agent types to their CLI invocation commands.
var AgentCommand = map[parser.AgentType]string{
	parser.AgentClaudeCode: "claude",
	parser.AgentCodex:      "codex",
	parser.AgentGeminiCLI:  "gemini",
	parser.AgentAider:      "aider",
	parser.AgentOpenCode:   "opencode",
}

// Request describes a handoff operation.
type Request struct {
	FromSessionID      string
	ToAgent            parser.AgentType
	Note               string
	CWD                string
	OpenInNewWorkspace bool
	TargetSessionID    string
}

// Result holds the outcome of a handoff operation.
type Result struct {
	GroupID        string
	SessionID      string
	InjectedPrompt string
	SourceContext   parser.Context
}

// Engine performs handoff operations.
type Engine struct {
	backend terminal.Backend
	store   *store.Store
	sum     *summarizer.Summarizer
}

// New creates a handoff Engine.
func New(b terminal.Backend, s *store.Store, sum *summarizer.Summarizer) *Engine {
	return &Engine{backend: b, store: s, sum: sum}
}

// Execute performs the full handoff pipeline.
func (e *Engine) Execute(req Request) (*Result, error) {
	srcCtx, err := e.resolveSource(req.FromSessionID)
	if err != nil {
		return nil, err
	}

	slog.Debug("handoff: source loaded",
		"session", srcCtx.SessionID,
		"agent", srcCtx.Agent,
		"goal", srcCtx.Task.Goal,
	)

	e.ensureSummary(srcCtx)
	injectionPrompt := e.generatePrompt(srcCtx, req.ToAgent, req.Note)

	cwd := req.CWD
	if cwd == "" {
		cwd = srcCtx.CWD
	}

	var groupID, sessionID string

	if req.OpenInNewWorkspace || req.TargetSessionID == "" {
		title := fmt.Sprintf("[bridge] %s → %s", srcCtx.Agent, req.ToAgent)
		groupID, err = e.backend.CreateGroup(title)
		if err != nil {
			return nil, fmt.Errorf("create group: %w", err)
		}
		slog.Debug("handoff: created group", "id", groupID)

		time.Sleep(500 * time.Millisecond)

		sessions, err := e.backend.ListSessions(groupID)
		if err != nil || len(sessions) == 0 {
			return nil, fmt.Errorf("get sessions for new group: %w", err)
		}
		sessionID = sessions[0].ID
	} else {
		sessionID = req.TargetSessionID
	}

	if cwd != "" {
		if err := e.backend.SendText(sessionID, "cd "+shellQuote(cwd)+"\n"); err != nil {
			slog.Warn("handoff: cd failed", "err", err)
		}
		time.Sleep(200 * time.Millisecond)
	}

	agentCmd, ok := AgentCommand[req.ToAgent]
	if !ok {
		agentCmd = string(req.ToAgent)
	}
	if err := e.backend.SendText(sessionID, agentCmd+"\n"); err != nil {
		return nil, fmt.Errorf("start agent: %w", err)
	}

	time.Sleep(2 * time.Second)

	if err := e.backend.SendText(sessionID, injectionPrompt+"\n"); err != nil {
		return nil, fmt.Errorf("inject prompt: %w", err)
	}

	if groupID != "" {
		_ = e.backend.FocusGroup(groupID)
	}

	_ = e.backend.Notify(
		"Context Bridge: Handoff Complete",
		fmt.Sprintf("%s → %s: \"%s\"", srcCtx.Agent, req.ToAgent, truncate(srcCtx.Task.Goal, 60)),
		sessionID,
	)

	slog.Info("handoff complete",
		"from", srcCtx.Agent,
		"to", req.ToAgent,
		"group", groupID,
		"session", sessionID,
	)

	return &Result{
		GroupID:        groupID,
		SessionID:      sessionID,
		InjectedPrompt: injectionPrompt,
		SourceContext:   *srcCtx,
	}, nil
}

// AutoInject is called by the monitor when a new agent session appears.
func (e *Engine) AutoInject(newCtx parser.Context) {
	all, err := e.store.ListAll()
	if err != nil || len(all) <= 1 {
		return
	}

	const freshContextWindow = 10 * time.Minute
	var candidate *parser.Context
	for i := range all {
		c := &all[i]
		if c.SurfaceID == newCtx.SurfaceID || c.Agent == parser.AgentUnknown {
			continue
		}
		if c.CapturedAt.IsZero() || c.CapturedAt.Before(time.Now().Add(-freshContextWindow)) {
			continue
		}
		if isHandoffSeedSession(*c) {
			continue
		}
		if !(c.CWD == newCtx.CWD || (c.GitBranch != "" && c.GitBranch == newCtx.GitBranch)) {
			continue
		}
		if isUsefulContext(*c) {
			candidate = c
			break
		}
	}
	if candidate == nil {
		return
	}

	slog.Debug("auto-inject: found related session",
		"from_agent", candidate.Agent,
		"to_agent", newCtx.Agent,
		"goal", candidate.Task.Goal,
	)

	e.ensureSummary(candidate)
	injectionPrompt := e.generatePrompt(candidate, newCtx.Agent, "")

	time.Sleep(3 * time.Second)

	if err := e.backend.SendText(newCtx.SurfaceID, injectionPrompt+"\n"); err != nil {
		slog.Warn("auto-inject: send text failed", "err", err)
		return
	}

	_ = e.backend.Notify(
		"Context Bridge: Auto-Injected",
		fmt.Sprintf("Context from %s → %s", candidate.Agent, newCtx.Agent),
		newCtx.SurfaceID,
	)

	slog.Info("auto-inject complete",
		"from", candidate.Agent,
		"to", newCtx.Agent,
		"session", newCtx.SurfaceID,
	)
}

func (e *Engine) resolveSource(sessionID string) (*parser.Context, error) {
	if sessionID != "" {
		return e.store.Get(sessionID)
	}
	all, err := e.store.ListAll()
	if err != nil || len(all) == 0 {
		return nil, fmt.Errorf("no active sessions found: %w", err)
	}
	return &all[0], nil
}

func (e *Engine) ensureSummary(ctx *parser.Context) {
	if ctx.Summary == "" && e.sum != nil {
		summary, err := e.sum.Summarize(*ctx)
		if err != nil {
			slog.Warn("summarization failed, using fallback", "err", err)
			summary = buildFallbackSummary(*ctx)
		}
		ctx.Summary = summary
		_ = e.store.UpdateSummary(ctx.SessionID, summary)
	}
	if ctx.Summary == "" {
		ctx.Summary = buildFallbackSummary(*ctx)
	}
}

func (e *Engine) generatePrompt(ctx *parser.Context, target parser.AgentType, note string) string {
	if e.sum != nil {
		prompt, err := e.sum.GenerateInjectionPrompt(*ctx, target, note)
		if err == nil {
			return prompt
		}
		slog.Warn("injection prompt generation failed, using fallback", "err", err)
	}
	return buildFallbackInjection(*ctx, target, note)
}

func isHandoffSeedSession(ctx parser.Context) bool {
	return strings.Contains(ctx.RawSnapshot, "# Context Handoff from ") ||
		strings.Contains(strings.ToLower(ctx.Task.Goal), "context handoff from ") ||
		strings.Contains(strings.ToLower(ctx.ConversationExcerpt), "context handoff from ")
}

func isUsefulContext(ctx parser.Context) bool {
	if strings.TrimSpace(ctx.Task.Goal) != "" {
		return true
	}
	if len(ctx.FileChanges) > 0 {
		return true
	}
	if len(ctx.ErrorsEncountered) > 0 {
		return true
	}
	return strings.TrimSpace(ctx.ConversationExcerpt) != ""
}

func buildFallbackSummary(ctx parser.Context) string {
	var b strings.Builder
	fmt.Fprintf(&b, "TASK: %s\n", ctx.Task.Goal)
	fmt.Fprintf(&b, "STATUS: %s\n", ctx.Task.Status)
	if ctx.Task.CurrentBlocker != "" {
		fmt.Fprintf(&b, "BLOCKER: %s\n", ctx.Task.CurrentBlocker)
	}
	if len(ctx.FileChanges) > 0 {
		b.WriteString("\nFILES CHANGED:\n")
		for _, fc := range ctx.FileChanges {
			fmt.Fprintf(&b, "- %s (%s)\n", fc.Path, fc.Op)
		}
	}
	if len(ctx.ErrorsEncountered) > 0 {
		b.WriteString("\nERRORS:\n")
		for _, e := range ctx.ErrorsEncountered {
			fmt.Fprintf(&b, "- %s\n", e)
		}
	}
	return b.String()
}

func buildFallbackInjection(ctx parser.Context, target parser.AgentType, note string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Context Handoff from %s\n\n", ctx.Agent)
	fmt.Fprintf(&b, "You are continuing work started in a %s session.\n\n", ctx.Agent)
	if ctx.CWD != "" {
		fmt.Fprintf(&b, "Working directory: %s\n", ctx.CWD)
	}
	if ctx.GitBranch != "" {
		fmt.Fprintf(&b, "Git branch: %s\n", ctx.GitBranch)
	}
	b.WriteString("\n")
	b.WriteString(ctx.Summary)
	if note != "" {
		fmt.Fprintf(&b, "\nAdditional instruction: %s\n", note)
	}
	fmt.Fprintf(&b, "\nPlease continue this work as %s. Start by reading the files listed above.\n", target)
	return b.String()
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
