package terminal

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// CmuxBackend connects to the cmux Unix domain socket API.
type CmuxBackend struct {
	socketPath string
	idCounter  atomic.Uint64
	mu         sync.Mutex

	readTextMethod  string
	readTextIDKey   string
	readTextLineKey string
}

func NewCmux(socketPath string) (*CmuxBackend, error) {
	if socketPath == "" {
		socketPath = os.Getenv("CMUX_SOCKET_PATH")
	}
	if socketPath == "" {
		return nil, fmt.Errorf("cmux socket path not set: provide via config or CMUX_SOCKET_PATH env var")
	}
	conn, err := net.DialTimeout("unix", socketPath, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("connect to cmux socket %s: %w", socketPath, err)
	}
	conn.Close()
	return &CmuxBackend{socketPath: socketPath}, nil
}

func (c *CmuxBackend) Name() string { return "cmux" }

func (c *CmuxBackend) ListGroups() ([]Group, error) {
	raw, err := c.call("workspace.list", nil)
	if err != nil {
		return nil, err
	}
	type workspace struct {
		ID         string `json:"id"`
		Title      string `json:"title"`
		GitBranch  string `json:"git_branch,omitempty"`
		CurrentDir string `json:"current_directory,omitempty"`
	}
	var wrapped struct {
		Workspaces []workspace `json:"workspaces"`
	}
	var list []workspace
	if err := json.Unmarshal(raw, &wrapped); err == nil && wrapped.Workspaces != nil {
		list = wrapped.Workspaces
	} else if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("unexpected workspace.list response: %s", string(raw))
	}
	groups := make([]Group, len(list))
	for i, ws := range list {
		groups[i] = Group{
			ID:         ws.ID,
			Title:      ws.Title,
			CurrentDir: ws.CurrentDir,
			GitBranch:  ws.GitBranch,
		}
	}
	return groups, nil
}

func (c *CmuxBackend) ListSessions(groupID string) ([]Session, error) {
	params := map[string]any{}
	if groupID != "" {
		params["workspace_id"] = groupID
	}
	raw, err := c.call("surface.list", params)
	if err != nil {
		return nil, err
	}
	type surface struct {
		ID          string `json:"id"`
		WorkspaceID string `json:"workspace_id,omitempty"`
		Title       string `json:"title,omitempty"`
		CurrentDir  string `json:"current_directory,omitempty"`
	}
	var wrapped struct {
		Surfaces []surface `json:"surfaces"`
	}
	var list []surface
	if err := json.Unmarshal(raw, &wrapped); err == nil && wrapped.Surfaces != nil {
		list = wrapped.Surfaces
	} else if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("unexpected surface.list response: %s", string(raw))
	}
	sessions := make([]Session, len(list))
	for i, s := range list {
		sessions[i] = Session{
			ID:         s.ID,
			GroupID:    s.WorkspaceID,
			Title:      s.Title,
			CurrentDir: s.CurrentDir,
		}
	}
	return sessions, nil
}

func (c *CmuxBackend) ReadScrollback(sessionID string, maxLines int) (string, error) {
	type candidate struct {
		method  string
		idKey   string
		lineKey string
	}

	c.mu.Lock()
	cached := candidate{c.readTextMethod, c.readTextIDKey, c.readTextLineKey}
	c.mu.Unlock()

	candidates := []candidate{}
	if cached.method != "" {
		candidates = append(candidates, cached)
	}
	candidates = append(candidates,
		candidate{"surface.read_text", "surface_id", "lines"},
		candidate{"surface.read_text", "surface_id", "max_lines"},
		candidate{"surface.read_text", "surface_id", "limit"},
		candidate{"surface.read_text", "pane_id", "lines"},
		candidate{"surface.read_text", "pane_id", "max_lines"},
		candidate{"surface.get_text", "surface_id", "lines"},
		candidate{"surface.get_scrollback", "surface_id", "lines"},
		candidate{"surface.read_text", "id", "lines"},
		candidate{"surface.get_text", "surface_id", "max_lines"},
		candidate{"surface.get_scrollback", "surface_id", "max_lines"},
		candidate{"terminal.get_text", "surface_id", "lines"},
		candidate{"terminal.get_scrollback", "surface_id", "lines"},
		candidate{"terminal.read_text", "surface_id", "lines"},
		candidate{"terminal.read_text", "surface_id", "max_lines"},
		candidate{"terminal.read_text", "surface_id", "limit"},
		candidate{"terminal.get_scrollback", "surface_id", "max_lines"},
		candidate{"pane.read_text", "pane_id", "lines"},
		candidate{"pane.read_text", "pane_id", "max_lines"},
		candidate{"pane.get_text", "pane_id", "lines"},
		candidate{"debug.terminal.read_text", "surface_id", "lines"},
		candidate{"debug.terminal.read_text", "surface_id", "max_lines"},
	)

	seen := map[string]struct{}{}
	var lastErr error
	for _, cand := range candidates {
		key := cand.method + "|" + cand.idKey + "|" + cand.lineKey
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}

		params := map[string]any{cand.idKey: sessionID}
		if maxLines > 0 {
			params[cand.lineKey] = maxLines
		}
		raw, err := c.call(cand.method, params)
		if err != nil {
			lastErr = err
			if isRetryableCmuxError(err) {
				continue
			}
			return "", err
		}

		text := parseTextResult(raw)
		c.mu.Lock()
		if c.readTextMethod == "" {
			c.readTextMethod = cand.method
			c.readTextIDKey = cand.idKey
			c.readTextLineKey = cand.lineKey
		}
		c.mu.Unlock()
		return text, nil
	}
	return "", lastErr
}

func (c *CmuxBackend) SendText(sessionID, text string) error {
	_, err := c.call("surface.send_text", map[string]any{
		"surface_id": sessionID,
		"text":       text,
	})
	return err
}

func (c *CmuxBackend) CreateGroup(title string) (string, error) {
	params := map[string]any{}
	if title != "" {
		params["title"] = title
	}
	raw, err := c.call("workspace.create", params)
	if err != nil {
		return "", err
	}
	var r1 struct {
		WorkspaceID string `json:"workspace_id"`
	}
	if err := json.Unmarshal(raw, &r1); err == nil && r1.WorkspaceID != "" {
		return r1.WorkspaceID, nil
	}
	var r2 struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &r2); err == nil {
		return r2.ID, nil
	}
	return "", fmt.Errorf("unexpected workspace.create response")
}

func (c *CmuxBackend) FocusGroup(groupID string) error {
	_, err := c.call("workspace.select", map[string]any{"workspace_id": groupID})
	return err
}

func (c *CmuxBackend) Notify(title, body, sessionID string) error {
	params := map[string]any{"title": title, "body": body}
	if sessionID != "" {
		params["surface_id"] = sessionID
		_, err := c.call("notification.create_for_surface", params)
		return err
	}
	_, err := c.call("notification.create", params)
	return err
}

func (c *CmuxBackend) Close() error { return nil }

// RawCall exposes raw JSON-RPC for diagnostics.
func (c *CmuxBackend) RawCall(method string, params map[string]any) ([]byte, error) {
	return c.call(method, params)
}

// --- JSON-RPC transport ---

type cmuxRequest struct {
	ID     string         `json:"id"`
	Method string         `json:"method"`
	Params map[string]any `json:"params,omitempty"`
}

type cmuxResponse struct {
	ID     string          `json:"id"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *cmuxRPCError   `json:"error,omitempty"`
}

type cmuxRPCError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *cmuxRPCError) Error() string {
	return fmt.Sprintf("cmux error %s: %s", e.Code, e.Message)
}

func (c *CmuxBackend) call(method string, params map[string]any) (json.RawMessage, error) {
	conn, err := net.DialTimeout("unix", c.socketPath, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("dial cmux socket: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))

	id := fmt.Sprintf("%d", c.idCounter.Add(1))
	req := cmuxRequest{ID: id, Method: method, Params: params}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')

	if _, err := conn.Write(data); err != nil {
		return nil, fmt.Errorf("write request: %w", err)
	}

	var resp cmuxResponse
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if !resp.OK {
		if resp.Error != nil {
			return nil, resp.Error
		}
		return nil, fmt.Errorf("cmux returned ok=false (no detail)")
	}
	return resp.Result, nil
}

func parseTextResult(raw json.RawMessage) string {
	var result struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &result); err == nil && result.Text != "" {
		return result.Text
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}

func isRetryableCmuxError(err error) bool {
	e, ok := err.(*cmuxRPCError)
	if !ok {
		return false
	}
	switch e.Code {
	case "method_not_found", "invalid_params", "invalid_request", "invalid_arguments", "bad_request":
		return true
	default:
		return false
	}
}
