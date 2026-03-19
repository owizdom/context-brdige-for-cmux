package terminal

import (
	"fmt"
	"os"
	"os/exec"
)

// Detect auto-detects the running terminal multiplexer and returns the
// appropriate backend. Priority: cmux > tmux.
//
// If forceBackend is set (e.g. "cmux" or "tmux"), it skips detection.
func Detect(forceBackend, cmuxSocketPath string) (Backend, error) {
	if forceBackend != "" {
		return create(forceBackend, cmuxSocketPath)
	}

	// 1. Try cmux — check for socket env var.
	if sp := cmuxSocketPath; sp != "" {
		if b, err := NewCmux(sp); err == nil {
			return b, nil
		}
	}
	if sp := os.Getenv("CMUX_SOCKET_PATH"); sp != "" {
		if b, err := NewCmux(sp); err == nil {
			return b, nil
		}
	}

	// 2. Try tmux — check if TMUX env var is set and tmux binary exists.
	if os.Getenv("TMUX") != "" {
		if _, err := exec.LookPath("tmux"); err == nil {
			if b, err := NewTmux(); err == nil {
				return b, nil
			}
		}
	}

	return nil, fmt.Errorf("no supported terminal multiplexer detected (set CMUX_SOCKET_PATH for cmux, or run inside tmux)")
}

func create(name, cmuxSocketPath string) (Backend, error) {
	switch name {
	case "cmux":
		return NewCmux(cmuxSocketPath)
	case "tmux":
		return NewTmux()
	default:
		return nil, fmt.Errorf("unknown backend %q (supported: cmux, tmux)", name)
	}
}
