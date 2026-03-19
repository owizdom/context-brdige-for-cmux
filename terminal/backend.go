// Package terminal defines the backend interface for terminal multiplexers
// and provides implementations for cmux, tmux, and others.
package terminal

// Session represents a terminal pane/surface/window where an agent may be running.
type Session struct {
	ID         string // unique pane/surface identifier
	GroupID    string // workspace/window that contains this session
	Title      string
	CurrentDir string
	GitBranch  string
}

// Group represents a workspace, window, or tab that contains one or more sessions.
type Group struct {
	ID         string
	Title      string
	CurrentDir string
	GitBranch  string
}

// Backend is the interface that all terminal multiplexer integrations implement.
// context-bridge only needs four capabilities from any multiplexer:
//   - List what's running
//   - Read scrollback from a pane
//   - Send text to a pane
//   - Create new panes/tabs
type Backend interface {
	// Name returns the backend identifier (e.g. "cmux", "tmux").
	Name() string

	// ListGroups returns all workspaces/windows/tabs.
	ListGroups() ([]Group, error)

	// ListSessions returns all panes/surfaces in a group.
	// If groupID is empty, returns all sessions across all groups.
	ListSessions(groupID string) ([]Session, error)

	// ReadScrollback reads the last maxLines lines from a session's terminal.
	ReadScrollback(sessionID string, maxLines int) (string, error)

	// SendText types text into a session's terminal.
	SendText(sessionID string, text string) error

	// CreateGroup creates a new workspace/window/tab and returns its ID.
	CreateGroup(title string) (string, error)

	// FocusGroup brings a workspace/window/tab to the foreground.
	FocusGroup(groupID string) error

	// Notify sends a notification (best-effort, no-op if unsupported).
	Notify(title, body, sessionID string) error

	// Close cleans up any resources.
	Close() error
}
