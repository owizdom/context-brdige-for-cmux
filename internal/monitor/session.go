// Package monitor continuously polls cmux and maintains live context snapshots
// for all active agent sessions. It is the autonomous sync engine.
package monitor

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/context-bridge/bridge/internal/cmux"
	"github.com/context-bridge/bridge/internal/parser"
	"github.com/context-bridge/bridge/internal/store"
	"github.com/context-bridge/bridge/internal/summarizer"
)

// Config holds monitor configuration.
type Config struct {
	PollInterval    time.Duration
	MaxScrollback   int    // lines
	SummarizeOnSync bool   // call LLM summarizer on every sync cycle
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		PollInterval:  5 * time.Second,
		MaxScrollback: 300,
		SummarizeOnSync: false, // summarize only on handoff by default
	}
}

// SurfaceState tracks per-surface state between poll cycles.
type SurfaceState struct {
	SessionID   string
	SurfaceID   string
	WorkspaceID string
	Agent       parser.AgentType
	LastSeen    time.Time
	IsNew       bool // true on the first time we see this surface
}

// Monitor polls cmux and keeps the store up to date.
type Monitor struct {
	cmuxClient  *cmux.Client
	store       *store.Store
	summarizer  *summarizer.Summarizer // may be nil
	cfg         Config
	mu          sync.Mutex
	surfaces    map[string]*SurfaceState // keyed by surfaceID
	// OnNewSession is called when a brand-new agent session is detected.
	// This is where autonomous handoff injection happens.
	OnNewSession func(ctx parser.Context)
	// OnContextUpdate is called each time a session's context is refreshed.
	OnContextUpdate func(ctx parser.Context)
	stopCh  chan struct{}
	stopped bool
}

// New creates a Monitor.
func New(c *cmux.Client, s *store.Store, sum *summarizer.Summarizer, cfg Config) *Monitor {
	return &Monitor{
		cmuxClient: c,
		store:      s,
		summarizer: sum,
		cfg:        cfg,
		surfaces:   make(map[string]*SurfaceState),
		stopCh:     make(chan struct{}),
	}
}

// Start begins the polling loop in the background.
func (m *Monitor) Start() {
	go m.loop()
}

// Stop signals the monitor to stop.
func (m *Monitor) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.stopped {
		m.stopped = true
		close(m.stopCh)
	}
}

func (m *Monitor) loop() {
	tick := time.NewTicker(m.cfg.PollInterval)
	defer tick.Stop()
	// Run immediately on start.
	m.poll()
	for {
		select {
		case <-m.stopCh:
			return
		case <-tick.C:
			m.poll()
		}
	}
}

func (m *Monitor) poll() {
	workspaces, err := m.cmuxClient.ListWorkspaces()
	if err != nil {
		slog.Warn("poll: list workspaces", "err", err)
		return
	}

	seen := map[string]bool{}

	for _, ws := range workspaces {
		surfaces, err := m.cmuxClient.ListSurfaces(ws.ID)
		if err != nil {
			slog.Warn("poll: list surfaces", "workspace", ws.ID, "err", err)
			continue
		}
		for _, surf := range surfaces {
			seen[surf.ID] = true
			m.processSurface(surf, ws)
		}
	}

	// Remove surfaces that have disappeared (closed panes).
	m.mu.Lock()
	for id := range m.surfaces {
		if !seen[id] {
			delete(m.surfaces, id)
		}
	}
	m.mu.Unlock()
}

func (m *Monitor) processSurface(surf cmux.Surface, ws cmux.Workspace) {
	scrollback, err := m.cmuxClient.ReadTerminalText(surf.ID, m.cfg.MaxScrollback)
	if err != nil {
		slog.Debug("read terminal", "surface", surf.ID, "err", err)
		return
	}
	if strings.TrimSpace(scrollback) == "" {
		return
	}

	detected := parser.DetectAgent(scrollback)
	agentType := detected.AgentType()
	if agentType == parser.AgentUnknown && isJustShellPrompt(scrollback) {
		// Don't track bare shell sessions with no agent.
		return
	}

	m.mu.Lock()
	state, exists := m.surfaces[surf.ID]
	isNew := !exists
	if !exists {
		sessionID := makeSessionID(surf.ID)
		state = &SurfaceState{
			SessionID:   sessionID,
			SurfaceID:   surf.ID,
			WorkspaceID: ws.ID,
			Agent:       agentType,
			IsNew:       true,
		}
		m.surfaces[surf.ID] = state
	}
	state.LastSeen = time.Now()
	sessionID := state.SessionID
	m.mu.Unlock()

	ctx := detected.Parse(scrollback)
	ctx.SessionID = sessionID
	ctx.SurfaceID = surf.ID
	ctx.WorkspaceID = ws.ID
	ctx.Workspace = ws.Title
	ctx.CWD = surf.CurrentDir
	if ctx.CWD == "" {
		ctx.CWD = ws.CurrentDir
	}
	ctx.GitBranch = ws.GitBranch

	// Persist to store.
	if err := m.store.Upsert(ctx); err != nil {
		slog.Warn("store upsert", "session", sessionID, "err", err)
		return
	}

	// If LLM summarizer is on and enabled for sync, summarize in background.
	if m.cfg.SummarizeOnSync && m.summarizer != nil {
		go m.summarizeAsync(ctx)
	}

	if isNew {
		slog.Info("new agent session detected",
			"agent", agentType,
			"surface", surf.ID,
			"workspace", ws.Title,
			"goal", ctx.Task.Goal,
		)
		if m.OnNewSession != nil {
			m.OnNewSession(ctx)
		}
	} else if m.OnContextUpdate != nil {
		m.OnContextUpdate(ctx)
	}
}

func (m *Monitor) summarizeAsync(ctx parser.Context) {
	summary, err := m.summarizer.Summarize(ctx)
	if err != nil {
		slog.Warn("summarize", "session", ctx.SessionID, "err", err)
		return
	}
	if err := m.store.UpdateSummary(ctx.SessionID, summary); err != nil {
		slog.Warn("update summary", "session", ctx.SessionID, "err", err)
	}
}

// ActiveSessions returns a snapshot of all currently tracked sessions.
func (m *Monitor) ActiveSessions() []SurfaceState {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]SurfaceState, 0, len(m.surfaces))
	for _, s := range m.surfaces {
		out = append(out, *s)
	}
	return out
}

// ForceRefresh triggers an immediate poll outside the normal tick cycle.
func (m *Monitor) ForceRefresh() {
	go m.poll()
}

func makeSessionID(surfaceID string) string {
	return fmt.Sprintf("sess-%s-%d", surfaceID[:min(8, len(surfaceID))], time.Now().UnixMilli())
}

func isJustShellPrompt(s string) bool {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > 5 {
		return false
	}
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l != "" && !strings.HasSuffix(l, "$") && !strings.HasSuffix(l, "%") && !strings.HasSuffix(l, ">") {
			return false
		}
	}
	return true
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
