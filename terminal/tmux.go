package terminal

import (
	"fmt"
	"os/exec"
	"strings"
)

// TmuxBackend interacts with tmux via its CLI.
type TmuxBackend struct{}

func NewTmux() (*TmuxBackend, error) {
	// Verify tmux is running by listing sessions.
	if err := run("tmux", "list-sessions"); err != nil {
		return nil, fmt.Errorf("tmux is not running or not installed: %w", err)
	}
	return &TmuxBackend{}, nil
}

func (t *TmuxBackend) Name() string { return "tmux" }

func (t *TmuxBackend) ListGroups() ([]Group, error) {
	// In tmux: group = window. We list all windows across all sessions.
	out, err := output("tmux", "list-windows", "-a", "-F", "#{session_name}:#{window_index}\t#{window_name}\t#{pane_current_path}")
	if err != nil {
		return nil, fmt.Errorf("tmux list-windows: %w", err)
	}
	var groups []Group
	for _, line := range splitLines(out) {
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 3 {
			continue
		}
		groups = append(groups, Group{
			ID:         parts[0],
			Title:      parts[1],
			CurrentDir: parts[2],
		})
	}
	return groups, nil
}

func (t *TmuxBackend) ListSessions(groupID string) ([]Session, error) {
	// In tmux: session = pane within a window.
	args := []string{"list-panes", "-F", "#{pane_id}\t#{window_index}\t#{pane_title}\t#{pane_current_path}"}
	if groupID != "" {
		// groupID is "session:window_index"
		args = append(args, "-t", groupID)
	} else {
		args = append(args, "-a")
	}
	out, err := output("tmux", args...)
	if err != nil {
		return nil, fmt.Errorf("tmux list-panes: %w", err)
	}
	var sessions []Session
	for _, line := range splitLines(out) {
		parts := strings.SplitN(line, "\t", 4)
		if len(parts) < 4 {
			continue
		}
		sessions = append(sessions, Session{
			ID:         parts[0],
			GroupID:    groupID,
			Title:      parts[2],
			CurrentDir: parts[3],
		})
	}
	return sessions, nil
}

func (t *TmuxBackend) ReadScrollback(sessionID string, maxLines int) (string, error) {
	if maxLines <= 0 {
		maxLines = 300
	}
	// capture-pane -p prints to stdout, -S specifies start line (negative = scrollback)
	out, err := output("tmux", "capture-pane", "-p", "-t", sessionID, "-S", fmt.Sprintf("-%d", maxLines))
	if err != nil {
		return "", fmt.Errorf("tmux capture-pane: %w", err)
	}
	return out, nil
}

func (t *TmuxBackend) SendText(sessionID, text string) error {
	// send-keys with -l for literal mode (no key name lookup)
	return run("tmux", "send-keys", "-t", sessionID, "-l", text)
}

func (t *TmuxBackend) CreateGroup(title string) (string, error) {
	// Create a new window in the current session.
	out, err := output("tmux", "new-window", "-P", "-F", "#{session_name}:#{window_index}", "-n", title)
	if err != nil {
		return "", fmt.Errorf("tmux new-window: %w", err)
	}
	return strings.TrimSpace(out), nil
}

func (t *TmuxBackend) FocusGroup(groupID string) error {
	return run("tmux", "select-window", "-t", groupID)
}

func (t *TmuxBackend) Notify(title, body, sessionID string) error {
	// tmux doesn't have native notifications, use display-message.
	msg := fmt.Sprintf("%s: %s", title, body)
	return run("tmux", "display-message", msg)
}

func (t *TmuxBackend) Close() error { return nil }

// --- helpers ---

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	return cmd.Run()
}

func output(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func splitLines(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
