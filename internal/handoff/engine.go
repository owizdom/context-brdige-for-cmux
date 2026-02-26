// Package handoff orchestrates cross-agent context injection.
// It reads a context snapshot, generates a handoff prompt via the LLM,
// creates a new cmux workspace, starts the target agent, and injects the prompt.
package handoff

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/context-bridge/bridge/internal/cmux"
	"github.com/context-bridge/bridge/internal/parser"
	"github.com/context-bridge/bridge/internal/store"
	"github.com/context-bridge/bridge/internal/summarizer"
)

// AgentCommand maps agent types to their CLI invocation commands.
var AgentCommand = map[parser.AgentType]string{
	parser.AgentClaudeCode: "claude",
	parser.AgentCodex:      "codex",
	parser.AgentGeminiCLI:  "gemini",
	parser.AgentAider:      "aider",
	parser.AgentOpenCode:   "opencode",
}

// HandoffRequest describes a handoff operation.
type HandoffRequest struct {
	// FromSessionID is the source session. If empty, uses the most recent active session.
	FromSessionID string
	// ToAgent is the target agent type.
	ToAgent parser.AgentType
	// Note is an optional user instruction to append to the injection prompt.
	Note string
	// CWD overrides the working directory. If empty, inherits from source session.
	CWD string
	// OpenInNewWorkspace: if true, create a new cmux tab for the target agent.
	// If false, inject into the specified target surface.
	OpenInNewWorkspace bool
	// TargetSurfaceID is used when OpenInNewWorkspace is false.
	TargetSurfaceID string
}

// Result holds the outcome of a handoff operation.
type Result struct {
	WorkspaceID   string
	SurfaceID     string
	InjectedPrompt string
	SourceContext  parser.Context
}

// Engine performs handoff operations.
type Engine struct {
	cmuxClient *cmux.Client
	store      *store.Store
	sum        *summarizer.Summarizer
}

// New creates a handoff Engine.
func New(c *cmux.Client, s *store.Store, sum *summarizer.Summarizer) *Engine {
	return &Engine{cmuxClient: c, store: s, sum: sum}
}

// Execute performs the full handoff pipeline:
//  1. Load source context
//  2. LLM-summarize it
//  3. Generate injection prompt for target agent
//  4. Open a new cmux workspace (or use existing surface)
//  5. Start target agent
//  6. Inject prompt
func (e *Engine) Execute(req HandoffRequest) (*Result, error) {
	// 1. Load source context.
	var srcCtx *parser.Context
	var err error
	if req.FromSessionID != "" {
		srcCtx, err = e.store.Get(req.FromSessionID)
	} else {
		all, lerr := e.store.ListAll()
		if lerr != nil || len(all) == 0 {
			return nil, fmt.Errorf("no active sessions found: %w", lerr)
		}
		srcCtx = &all[0]
	}
	if err != nil {
		return nil, fmt.Errorf("load source context: %w", err)
	}

	slog.Info("handoff: source loaded",
		"session", srcCtx.SessionID,
		"agent", srcCtx.Agent,
		"goal", srcCtx.Task.Goal,
	)

	// 2. Summarize if we don't have a summary yet.
	if srcCtx.Summary == "" && e.sum != nil {
		slog.Info("handoff: summarizing context...")
		summary, serr := e.sum.Summarize(*srcCtx)
		if serr != nil {
			slog.Warn("handoff: summarization failed, using excerpt", "err", serr)
			summary = buildFallbackSummary(*srcCtx)
		}
		srcCtx.Summary = summary
		_ = e.store.UpdateSummary(srcCtx.SessionID, summary)
	}
	if srcCtx.Summary == "" {
		srcCtx.Summary = buildFallbackSummary(*srcCtx)
	}

	// 3. Generate injection prompt.
	var injectionPrompt string
	if e.sum != nil {
		injectionPrompt, err = e.sum.GenerateInjectionPrompt(*srcCtx, req.ToAgent, req.Note)
		if err != nil {
			slog.Warn("handoff: injection prompt generation failed, using fallback", "err", err)
			injectionPrompt = buildFallbackInjection(*srcCtx, req.ToAgent, req.Note)
		}
	} else {
		injectionPrompt = buildFallbackInjection(*srcCtx, req.ToAgent, req.Note)
	}

	// 4. Determine working directory.
	cwd := req.CWD
	if cwd == "" {
		cwd = srcCtx.CWD
	}

	var workspaceID, surfaceID string

	if req.OpenInNewWorkspace || req.TargetSurfaceID == "" {
		// 5a. Create a new cmux workspace.
		title := fmt.Sprintf("[bridge] %s → %s", srcCtx.Agent, req.ToAgent)
		workspaceID, err = e.cmuxClient.CreateWorkspace(title)
		if err != nil {
			return nil, fmt.Errorf("create workspace: %w", err)
		}
		slog.Info("handoff: created workspace", "id", workspaceID)

		// Give cmux a moment to initialize the pane.
		time.Sleep(500 * time.Millisecond)

		// Get the new surface.
		surfaces, err := e.cmuxClient.ListSurfaces(workspaceID)
		if err != nil || len(surfaces) == 0 {
			return nil, fmt.Errorf("get surfaces for new workspace: %w", err)
		}
		surfaceID = surfaces[0].ID
	} else {
		// 5b. Use existing surface.
		surfaceID = req.TargetSurfaceID
	}

	// 5c. Change directory if needed.
	if cwd != "" {
		if err := e.cmuxClient.SendText(surfaceID, "cd "+shellQuote(cwd)+"\n"); err != nil {
			slog.Warn("handoff: cd failed", "err", err)
		}
		time.Sleep(200 * time.Millisecond)
	}

	// 5d. Start the target agent.
	agentCmd, ok := AgentCommand[req.ToAgent]
	if !ok {
		agentCmd = string(req.ToAgent)
	}
	if err := e.cmuxClient.SendText(surfaceID, agentCmd+"\n"); err != nil {
		return nil, fmt.Errorf("start agent: %w", err)
	}

	// Wait for agent to initialize before injecting context.
	time.Sleep(2 * time.Second)

	// 6. Inject the context prompt.
	if err := e.cmuxClient.SendText(surfaceID, injectionPrompt+"\n"); err != nil {
		return nil, fmt.Errorf("inject prompt: %w", err)
	}

	// Focus the new workspace.
	if workspaceID != "" {
		_ = e.cmuxClient.SelectWorkspace(workspaceID)
	}

	// Send a notification.
	_ = e.cmuxClient.CreateNotification(
		"Context Bridge: Handoff Complete",
		fmt.Sprintf("%s → %s: \"%s\"", srcCtx.Agent, req.ToAgent, truncate(srcCtx.Task.Goal, 60)),
		surfaceID,
	)

	slog.Info("handoff: complete",
		"from", srcCtx.Agent,
		"to", req.ToAgent,
		"workspace", workspaceID,
		"surface", surfaceID,
	)

	return &Result{
		WorkspaceID:    workspaceID,
		SurfaceID:      surfaceID,
		InjectedPrompt: injectionPrompt,
		SourceContext:  *srcCtx,
	}, nil
}

// AutoInject is called by the monitor when a new agent session appears.
// It finds the most recently active OTHER session, and if it has a compatible
// context (same CWD or git branch), automatically injects context.
func (e *Engine) AutoInject(newCtx parser.Context) {
	all, err := e.store.ListAll()
	if err != nil || len(all) <= 1 {
		return // no prior session to pull from
	}

	// Find the most recent session that is NOT this surface, has a task goal,
	// and shares context signals (cwd or git branch).
	var candidate *parser.Context
	for i := range all {
		c := &all[i]
		if c.SurfaceID == newCtx.SurfaceID {
			continue
		}
		if c.Task.Goal == "" {
			continue
		}
		if c.CWD == newCtx.CWD || (c.GitBranch != "" && c.GitBranch == newCtx.GitBranch) {
			candidate = c
			break
		}
	}
	if candidate == nil {
		return // no related session found
	}

	slog.Info("auto-inject: found related session",
		"from_agent", candidate.Agent,
		"to_agent", newCtx.Agent,
		"goal", candidate.Task.Goal,
	)

	// Summarize candidate if needed.
	if candidate.Summary == "" && e.sum != nil {
		summary, serr := e.sum.Summarize(*candidate)
		if serr == nil {
			candidate.Summary = summary
			_ = e.store.UpdateSummary(candidate.SessionID, summary)
		}
	}
	if candidate.Summary == "" {
		candidate.Summary = buildFallbackSummary(*candidate)
	}

	// Generate injection prompt.
	var injectionPrompt string
	if e.sum != nil {
		injectionPrompt, err = e.sum.GenerateInjectionPrompt(*candidate, newCtx.Agent, "")
		if err != nil {
			injectionPrompt = buildFallbackInjection(*candidate, newCtx.Agent, "")
		}
	} else {
		injectionPrompt = buildFallbackInjection(*candidate, newCtx.Agent, "")
	}

	// Wait a moment for the new agent to fully initialize.
	time.Sleep(3 * time.Second)

	if err := e.cmuxClient.SendText(newCtx.SurfaceID, injectionPrompt+"\n"); err != nil {
		slog.Warn("auto-inject: send text failed", "err", err)
		return
	}

	_ = e.cmuxClient.CreateNotification(
		"Context Bridge: Auto-Injected",
		fmt.Sprintf("Context from %s → %s", candidate.Agent, newCtx.Agent),
		newCtx.SurfaceID,
	)

	slog.Info("auto-inject: context injected",
		"from", candidate.Agent,
		"to", newCtx.Agent,
		"surface", newCtx.SurfaceID,
	)
}

// buildFallbackSummary creates a summary without LLM when the API is unavailable.
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

// buildFallbackInjection creates an injection prompt without LLM.
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
