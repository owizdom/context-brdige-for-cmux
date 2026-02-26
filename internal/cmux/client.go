// Package cmux provides a client for the cmux Unix domain socket API (v2).
// Docs: https://github.com/manaflow-ai/cmux
package cmux

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// Client is a thread-safe JSON-RPC client over a Unix domain socket.
type Client struct {
	socketPath string
	conn       net.Conn
	mu         sync.Mutex
	idCounter  atomic.Uint64
}

// request is the cmux JSON-RPC v2 request envelope.
type request struct {
	ID     string         `json:"id"`
	Method string         `json:"method"`
	Params map[string]any `json:"params,omitempty"`
}

// response is the cmux JSON-RPC v2 response envelope.
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

// NewClient creates a new cmux client. socketPath may be empty, in which case
// the CMUX_SOCKET_PATH environment variable is used.
func NewClient(socketPath string) (*Client, error) {
	if socketPath == "" {
		socketPath = os.Getenv("CMUX_SOCKET_PATH")
	}
	if socketPath == "" {
		return nil, fmt.Errorf("cmux socket path not set: provide via config or CMUX_SOCKET_PATH env var")
	}
	c := &Client{socketPath: socketPath}
	if err := c.connect(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Client) connect() error {
	conn, err := net.DialTimeout("unix", c.socketPath, 5*time.Second)
	if err != nil {
		return fmt.Errorf("connect to cmux socket %s: %w", c.socketPath, err)
	}
	c.conn = conn
	return nil
}

// call sends a JSON-RPC request and returns the raw result bytes.
func (c *Client) call(method string, params map[string]any) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	id := fmt.Sprintf("%d", c.idCounter.Add(1))
	req := request{ID: id, Method: method, Params: params}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')

	_ = c.conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := c.conn.Write(data); err != nil {
		// Try reconnect once.
		if rerr := c.connect(); rerr != nil {
			return nil, fmt.Errorf("write failed and reconnect failed: %w", rerr)
		}
		_ = c.conn.SetDeadline(time.Now().Add(10 * time.Second))
		if _, err := c.conn.Write(data); err != nil {
			return nil, fmt.Errorf("write after reconnect: %w", err)
		}
	}

	dec := json.NewDecoder(c.conn)
	var resp response
	if err := dec.Decode(&resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if !resp.OK {
		if resp.Error != nil {
			return nil, resp.Error
		}
		return nil, fmt.Errorf("cmux returned ok=false with no error detail")
	}
	return resp.Result, nil
}

// Close closes the underlying connection.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.Close()
}

// ---- Typed API methods ----

// Workspace represents a cmux workspace (tab).
type Workspace struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	CustomTitle  string `json:"custom_title,omitempty"`
	GitBranch    string `json:"git_branch,omitempty"`
	CurrentDir   string `json:"current_directory,omitempty"`
	WindowID     string `json:"window_id,omitempty"`
}

// Surface represents a terminal pane within a workspace.
type Surface struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	Title       string `json:"title,omitempty"`
	CurrentDir  string `json:"current_directory,omitempty"`
}

// ListWorkspaces returns all workspaces across all windows.
func (c *Client) ListWorkspaces() ([]Workspace, error) {
	raw, err := c.call("workspace.list", nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Workspaces []Workspace `json:"workspaces"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return result.Workspaces, nil
}

// ListSurfaces returns all surfaces (panes) in a workspace.
func (c *Client) ListSurfaces(workspaceID string) ([]Surface, error) {
	params := map[string]any{}
	if workspaceID != "" {
		params["workspace_id"] = workspaceID
	}
	raw, err := c.call("surface.list", params)
	if err != nil {
		return nil, err
	}
	var result struct {
		Surfaces []Surface `json:"surfaces"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return result.Surfaces, nil
}

// ReadTerminalText reads the scrollback text of a surface.
// maxLines controls how many lines to read (0 = server default).
func (c *Client) ReadTerminalText(surfaceID string, maxLines int) (string, error) {
	params := map[string]any{"surface_id": surfaceID}
	if maxLines > 0 {
		params["lines"] = maxLines
	}
	raw, err := c.call("debug.terminal.read_text", params)
	if err != nil {
		return "", err
	}
	var result struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", err
	}
	return result.Text, nil
}

// SendText sends text to a surface (as if typed by the user).
func (c *Client) SendText(surfaceID, text string) error {
	_, err := c.call("surface.send_text", map[string]any{
		"surface_id": surfaceID,
		"text":       text,
	})
	return err
}

// CreateWorkspace creates a new workspace (tab) and returns its ID.
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
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", err
	}
	return result.WorkspaceID, nil
}

// SelectWorkspace switches focus to a workspace.
func (c *Client) SelectWorkspace(workspaceID string) error {
	_, err := c.call("workspace.select", map[string]any{"workspace_id": workspaceID})
	return err
}

// CreateNotification sends a rich notification tied to a surface.
func (c *Client) CreateNotification(title, body, surfaceID string) error {
	params := map[string]any{
		"title": title,
		"body":  body,
	}
	if surfaceID != "" {
		params["surface_id"] = surfaceID
		_, err := c.call("notification.create_for_surface", params)
		return err
	}
	_, err := c.call("notification.create", params)
	return err
}
