// Package cmux provides a client for the cmux Unix domain socket API (v2).
package cmux

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

// Client connects to the cmux Unix domain socket.
type Client struct {
	socketPath string
	idCounter  atomic.Uint64
	mu         sync.Mutex

	readTextMethod string // e.g. "surface.read_text"
	readTextIDKey  string // e.g. "surface_id"
	readTextLineKey string // e.g. "lines"
}

type request struct {
	ID     string         `json:"id"`
	Method string         `json:"method"`
	Params map[string]any `json:"params,omitempty"`
}

type response struct {
	ID     string          `json:"id"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("cmux error %s: %s", e.Code, e.Message)
}

func NewClient(socketPath string) (*Client, error) {
	if socketPath == "" {
		socketPath = os.Getenv("CMUX_SOCKET_PATH")
	}
	if socketPath == "" {
		return nil, fmt.Errorf("cmux socket path not set: provide via config or CMUX_SOCKET_PATH env var")
	}
	c := &Client{socketPath: socketPath}
	// Verify the socket is reachable.
	conn, err := net.DialTimeout("unix", socketPath, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("connect to cmux socket %s: %w", socketPath, err)
	}
	conn.Close()
	return c, nil
}

// call opens a fresh connection for each request, sends it, reads the response,
// and closes. Fresh-per-request avoids all decoder buffering issues.
func (c *Client) call(method string, params map[string]any) (json.RawMessage, error) {
	conn, err := net.DialTimeout("unix", c.socketPath, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("dial cmux socket: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))

	id := fmt.Sprintf("%d", c.idCounter.Add(1))
	req := request{ID: id, Method: method, Params: params}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')

	if _, err := conn.Write(data); err != nil {
		return nil, fmt.Errorf("write request: %w", err)
	}

	reader := bufio.NewReader(conn)
	var resp response
	if err := json.NewDecoder(reader).Decode(&resp); err != nil {
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

// RawCall sends a request and returns raw result bytes — used by bridge test.
func (c *Client) RawCall(method string, params map[string]any) ([]byte, error) {
	return c.call(method, params)
}

// Close is a no-op (connections are per-call now).
func (c *Client) Close() error { return nil }

// ---- Typed API methods ----

type Workspace struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	CustomTitle string `json:"custom_title,omitempty"`
	GitBranch   string `json:"git_branch,omitempty"`
	CurrentDir  string `json:"current_directory,omitempty"`
	WindowID    string `json:"window_id,omitempty"`
}

type Surface struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	Title       string `json:"title,omitempty"`
	CurrentDir  string `json:"current_directory,omitempty"` // may be empty — fall back to Title
}

func (c *Client) ListWorkspaces() ([]Workspace, error) {
	raw, err := c.call("workspace.list", nil)
	if err != nil {
		return nil, err
	}
	// Try wrapped format first: {"workspaces": [...]}
	var wrapped struct {
		Workspaces []Workspace `json:"workspaces"`
	}
	if err := json.Unmarshal(raw, &wrapped); err == nil && wrapped.Workspaces != nil {
		return wrapped.Workspaces, nil
	}
	// Try bare array: [...]
	var list []Workspace
	if err := json.Unmarshal(raw, &list); err == nil {
		return list, nil
	}
	return nil, fmt.Errorf("unexpected workspace.list response: %s", string(raw))
}

func (c *Client) ListSurfaces(workspaceID string) ([]Surface, error) {
	params := map[string]any{}
	if workspaceID != "" {
		params["workspace_id"] = workspaceID
	}
	raw, err := c.call("surface.list", params)
	if err != nil {
		return nil, err
	}
	var wrapped struct {
		Surfaces []Surface `json:"surfaces"`
	}
	if err := json.Unmarshal(raw, &wrapped); err == nil && wrapped.Surfaces != nil {
		return wrapped.Surfaces, nil
	}
	var list []Surface
	if err := json.Unmarshal(raw, &list); err == nil {
		return list, nil
	}
	return nil, fmt.Errorf("unexpected surface.list response: %s", string(raw))
}

func (c *Client) ReadTerminalText(surfaceID string, maxLines int) (string, error) {
	type candidate struct {
		method  string
		idKey   string
		lineKey string
	}

	c.mu.Lock()
	cachedMethod := c.readTextMethod
	cachedIDKey := c.readTextIDKey
	cachedLineKey := c.readTextLineKey
	c.mu.Unlock()

	candidates := []candidate{}
	if cachedMethod != "" && cachedIDKey != "" && cachedLineKey != "" {
		candidates = append(candidates, candidate{
			method:  cachedMethod,
			idKey:   cachedIDKey,
			lineKey: cachedLineKey,
		})
	}
	candidates = append(candidates,
		candidate{method: "surface.read_text", idKey: "surface_id", lineKey: "lines"},
		candidate{method: "surface.read_text", idKey: "surface_id", lineKey: "max_lines"},
		candidate{method: "surface.read_text", idKey: "surface_id", lineKey: "limit"},
		candidate{method: "surface.read_text", idKey: "pane_id", lineKey: "lines"},
		candidate{method: "surface.read_text", idKey: "pane_id", lineKey: "max_lines"},
		candidate{method: "surface.get_text", idKey: "surface_id", lineKey: "lines"},
		candidate{method: "surface.get_scrollback", idKey: "surface_id", lineKey: "lines"},
		candidate{method: "surface.read_text", idKey: "id", lineKey: "lines"},
		candidate{method: "surface.get_text", idKey: "surface_id", lineKey: "max_lines"},
		candidate{method: "surface.get_scrollback", idKey: "surface_id", lineKey: "max_lines"},
		candidate{method: "terminal.get_text", idKey: "surface_id", lineKey: "lines"},
		candidate{method: "terminal.get_scrollback", idKey: "surface_id", lineKey: "lines"},
		candidate{method: "terminal.read_text", idKey: "surface_id", lineKey: "lines"},
		candidate{method: "terminal.read_text", idKey: "surface_id", lineKey: "max_lines"},
		candidate{method: "terminal.read_text", idKey: "surface_id", lineKey: "limit"},
		candidate{method: "terminal.get_scrollback", idKey: "surface_id", lineKey: "max_lines"},
		candidate{method: "pane.read_text", idKey: "pane_id", lineKey: "lines"},
		candidate{method: "pane.read_text", idKey: "pane_id", lineKey: "max_lines"},
		candidate{method: "pane.get_text", idKey: "pane_id", lineKey: "lines"},
		candidate{method: "debug.terminal.read_text", idKey: "surface_id", lineKey: "lines"},
		candidate{method: "debug.terminal.read_text", idKey: "surface_id", lineKey: "max_lines"},
	)

	seen := map[string]struct{}{}
	var lastErr error
	for _, candidate := range candidates {
		key := candidate.method + "|" + candidate.idKey + "|" + candidate.lineKey
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}

		text, err := c.readTerminalText(surfaceID, maxLines, candidate)
		if err == nil {
			c.mu.Lock()
			if c.readTextMethod == "" {
				c.readTextMethod = candidate.method
				c.readTextIDKey = candidate.idKey
				c.readTextLineKey = candidate.lineKey
			}
			c.mu.Unlock()
			return text, nil
		}
		lastErr = err
		if !isRetryableReadTextError(err) {
			return "", err
		}
	}

	return "", lastErr
}

func (c *Client) readTerminalText(surfaceID string, maxLines int, candidate struct {
	method  string
	idKey   string
	lineKey string
}) (string, error) {
	params := map[string]any{
		candidate.idKey: surfaceID,
	}
	if maxLines > 0 {
		params[candidate.lineKey] = maxLines
	}
	raw, err := c.call(candidate.method, params)
	if err != nil {
		return "", err
	}
	return c.parseTextResult(raw)
}

func (c *Client) parseTextResult(raw json.RawMessage) (string, error) {
	var result struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &result); err == nil && result.Text != "" {
		return result.Text, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil
	}
	return string(raw), nil
}

func isRetryableReadTextError(err error) bool {
	e, ok := err.(*rpcError)
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

func (c *Client) SendText(surfaceID, text string) error {
	_, err := c.call("surface.send_text", map[string]any{
		"surface_id": surfaceID,
		"text":       text,
	})
	return err
}

func (c *Client) CreateWorkspace(title string) (string, error) {
	params := map[string]any{}
	if title != "" {
		params["title"] = title
	}
	raw, err := c.call("workspace.create", params)
	if err != nil {
		return "", err
	}
	var result struct {
		WorkspaceID string `json:"workspace_id"`
	}
	if err := json.Unmarshal(raw, &result); err != nil || result.WorkspaceID == "" {
		// Try {"id": "..."}
		var r2 struct {
			ID string `json:"id"`
		}
		if err2 := json.Unmarshal(raw, &r2); err2 == nil {
			return r2.ID, nil
		}
	}
	return result.WorkspaceID, nil
}

func (c *Client) SelectWorkspace(workspaceID string) error {
	_, err := c.call("workspace.select", map[string]any{"workspace_id": workspaceID})
	return err
}

func (c *Client) CreateNotification(title, body, surfaceID string) error {
	params := map[string]any{"title": title, "body": body}
	if surfaceID != "" {
		params["surface_id"] = surfaceID
		_, err := c.call("notification.create_for_surface", params)
		return err
	}
	_, err := c.call("notification.create", params)
	return err
}
