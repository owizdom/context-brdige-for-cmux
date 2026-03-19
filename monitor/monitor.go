// Package monitor continuously polls the terminal multiplexer and maintains
// live context snapshots for all active agent sessions.
package monitor

import (
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/context-bridge/bridge/parser"
	"github.com/context-bridge/bridge/store"
	"github.com/context-bridge/bridge/summarizer"
	"github.com/context-bridge/bridge/terminal"
)

// Config holds monitor configuration.
type Config struct {
	PollInterval    time.Duration
	MaxScrollback   int
	SummarizeOnSync bool
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		PollInterval:    5 * time.Second,
		MaxScrollback:   300,
		SummarizeOnSync: false,
	}
}

// SurfaceState tracks per-session state between poll cycles.
type SurfaceState struct {
	SessionID string
	SessionTID string // terminal session ID (pane/surface)
	GroupID   string
	Agent     parser.AgentType
	LastSeen  time.Time
	IsNew     bool
}

// Monitor polls the terminal backend and keeps the store up to date.
type Monitor struct {
	backend    terminal.Backend
	store      *store.Store
	summarizer *summarizer.Summarizer
	cfg        Config

	mu       sync.Mutex
	surfaces map[string]*SurfaceState

	OnNewSession    func(ctx parser.Context)
	OnContextUpdate func(ctx parser.Context)

	stopCh  chan struct{}
	stopped bool

	agentCountByGroup map[string]int
	groupPrimed       map[string]bool
}

var shellCommandLineRe = regexp.MustCompile(`(?m)^\s*[^\s]+@[^\s]+.*[\$%>]\s+[^\s]+.*$`)

// New creates a Monitor.
func New(b terminal.Backend, s *store.Store, sum *summarizer.Summarizer, cfg Config) *Monitor {
	return &Monitor{
		backend:           b,
		store:             s,
		summarizer:        sum,
		cfg:               cfg,
		surfaces:          make(map[string]*SurfaceState),
		agentCountByGroup: make(map[string]int),
		groupPrimed:       make(map[string]bool),
		stopCh:            make(chan struct{}),
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
	groups, err := m.backend.ListGroups()
	if err != nil {
		slog.Warn("poll: list groups failed", "backend", m.backend.Name(), "err", err)
		return
	}

	if len(groups) == 0 {
		slog.Debug("poll: 0 groups — open a tab/window first")
		return
	}

	slog.Debug("poll: scanning", "backend", m.backend.Name(), "groups", len(groups))

	seen := map[string]bool{}

	for _, grp := range groups {
		sessions, err := m.backend.ListSessions(grp.ID)
		if err != nil {
			slog.Warn("poll: list sessions failed", "group", grp.ID, "err", err)
			continue
		}
		slog.Debug("poll: group", "id", grp.ID[:min(8, len(grp.ID))], "title", grp.Title, "sessions", len(sessions))
		for _, sess := range sessions {
			seen[sess.ID] = true
			m.processSession(sess, grp)
		}
	}

	// Remove sessions that have disappeared (closed panes).
	m.mu.Lock()
	for id := range m.surfaces {
		if !seen[id] {
			gID := m.surfaces[id].GroupID
			delete(m.surfaces, id)
			if gID != "" && m.agentCountByGroup[gID] > 0 {
				m.agentCountByGroup[gID]--
				if m.agentCountByGroup[gID] <= 0 {
					m.groupPrimed[gID] = false
				}
			}
		}
	}
	m.mu.Unlock()
}

func (m *Monitor) processSession(sess terminal.Session, grp terminal.Group) {
	scrollback, err := m.backend.ReadScrollback(sess.ID, m.cfg.MaxScrollback)
	if err != nil {
		slog.Debug("poll: read scrollback failed", "session", sess.ID, "err", err)
		return
	}
	if strings.TrimSpace(scrollback) == "" {
		return
	}

	detected := parser.DetectAgent(scrollback)
	agentType := detected.AgentType()

	if agentType == parser.AgentUnknown && isLikelyShellSession(scrollback) {
		return
	}
	if agentType == parser.AgentUnknown {
		return
	}

	m.mu.Lock()
	state, exists := m.surfaces[sess.ID]
	isNew := !exists
	groupPrimed := m.groupPrimed[grp.ID]
	if !exists {
		sessionID := makeSessionID(sess.ID)
		state = &SurfaceState{
			SessionID:  sessionID,
			SessionTID: sess.ID,
			GroupID:    grp.ID,
			Agent:      agentType,
			IsNew:      true,
		}
		m.surfaces[sess.ID] = state
		m.agentCountByGroup[grp.ID]++
	}
	state.LastSeen = time.Now()
	sessionID := state.SessionID
	m.mu.Unlock()

	ctx := detected.Parse(scrollback)
	ctx.SessionID = sessionID
	ctx.SurfaceID = sess.ID
	ctx.WorkspaceID = grp.ID
	ctx.Workspace = grp.Title
	ctx.CWD = sess.CurrentDir
	if ctx.CWD == "" {
		ctx.CWD = grp.CurrentDir
	}
	ctx.GitBranch = grp.GitBranch

	if err := m.store.Upsert(ctx); err != nil {
		slog.Warn("store upsert", "session", sessionID, "err", err)
		return
	}

	if m.cfg.SummarizeOnSync && m.summarizer != nil {
		go m.summarizeAsync(ctx)
	}

	if isNew {
		slog.Debug("new agent session detected",
			"agent", agentType,
			"session", sess.ID,
			"group", grp.Title,
			"goal", ctx.Task.Goal,
		)
		if groupPrimed && m.OnNewSession != nil {
			m.OnNewSession(ctx)
		}
		m.groupPrimed[grp.ID] = true
	} else if m.OnContextUpdate != nil {
		m.OnContextUpdate(ctx)
	}
}

func isLikelyShellSession(s string) bool {
	if isJustShellPrompt(s) {
		return true
	}
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.Contains(line, "Last login:") {
			return true
		}
		if shellCommandLineRe.MatchString(line) {
			return true
		}
	}
	return false
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

func makeSessionID(terminalID string) string {
	id := terminalID
	if len(id) > 8 {
		id = id[:8]
	}
	return fmt.Sprintf("sess-%s-%d", id, time.Now().UnixMilli())
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
