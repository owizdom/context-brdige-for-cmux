package cmd

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/context-bridge/bridge/config"
	"github.com/context-bridge/bridge/store"
	"github.com/context-bridge/bridge/summarizer"
	"github.com/context-bridge/bridge/terminal"
	"github.com/spf13/cobra"
)

var (
	cfgPath      string
	forceBackend string
)

func Execute() error {
	root := &cobra.Command{
		Use:   "mnemo",
		Short: "mnemo: memory for AI coding agents",
		Long: `Mnemo gives AI coding agents (Claude Code, Codex, Gemini CLI, Aider)
cross-session, cross-agent memory. It watches what agents do, extracts what
matters, and automatically injects context into the next session.

Supports tmux and cmux. Zero workflow changes required.`,
	}

	root.PersistentFlags().StringVar(&cfgPath, "config", "", "config file path (default: ~/.config/mnemo/config.toml)")
	root.PersistentFlags().StringVar(&forceBackend, "backend", "", "force terminal backend: cmux | tmux (default: auto-detect)")

	root.AddCommand(
		daemonCmd(),
		statusCmd(),
		handoffCmd(),
		snapshotCmd(),
		watchCmd(),
		diagnoseCmd(),
		versionCmd(),
	)

	return root.Execute()
}

// buildDeps wires up the core dependencies from config.
func buildDeps(cfg *config.Config) (terminal.Backend, *store.Store, *summarizer.Summarizer, error) {
	backend, err := terminal.Detect(forceBackend, cfg.SocketPath())
	if err != nil {
		return nil, nil, nil, fmt.Errorf("terminal backend: %w", err)
	}

	s, err := store.Open(cfg.Bridge.DBPath)
	if err != nil {
		backend.Close()
		return nil, nil, nil, fmt.Errorf("open store: %w", err)
	}

	var sum *summarizer.Summarizer
	if key := cfg.Anthropic.APIKey; key != "" || os.Getenv("ANTHROPIC_API_KEY") != "" {
		sum, err = summarizer.New(key, cfg.Anthropic.Model, cfg.Anthropic.MaxTokens)
		if err != nil {
			slog.Warn("summarizer unavailable", "err", err)
		}
	} else {
		slog.Debug("ANTHROPIC_API_KEY not set — LLM summarization disabled; fallback summaries will be used")
	}

	return backend, s, sum, nil
}

func setupLogging(level string) {
	var l slog.Level
	switch strings.ToLower(level) {
	case "debug":
		l = slog.LevelDebug
	case "warn":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: l})))
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
