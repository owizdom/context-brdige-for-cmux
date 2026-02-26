package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/context-bridge/bridge/internal/cmux"
	"github.com/context-bridge/bridge/internal/config"
	"github.com/context-bridge/bridge/internal/handoff"
	"github.com/context-bridge/bridge/internal/monitor"
	"github.com/context-bridge/bridge/internal/parser"
	"github.com/context-bridge/bridge/internal/store"
	"github.com/context-bridge/bridge/internal/summarizer"
	"github.com/spf13/cobra"
)

var cfgPath string

func main() {
	root := &cobra.Command{
		Use:   "bridge",
		Short: "context-bridge: autonomous cross-agent context sharing for cmux",
		Long: `context-bridge sits alongside cmux and automatically shares context
between AI coding agents (Claude Code, Codex, Gemini CLI, Aider, etc.).

When you open a new agent session in the same project, context-bridge
automatically injects the prior session's context so you can continue
working seamlessly — no manual commands required.`,
	}

	root.PersistentFlags().StringVar(&cfgPath, "config", "", "config file path (default: ~/.config/context-bridge/config.toml)")

	root.AddCommand(
		daemonCmd(),
		statusCmd(),
		handoffCmd(),
		snapshotCmd(),
		watchCmd(),
		testCmd(),
	)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

// ---- daemon ----------------------------------------------------------------

func daemonCmd() *cobra.Command {
	var autoInject bool
	var summarizeOnSync bool

	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Start the context-bridge daemon (autonomous sync)",
		Long: `Runs the background daemon that:
  - Polls all cmux sessions every N seconds
  - Detects which AI agent is running in each pane
  - Extracts and stores context (task, files changed, errors, conversation)
  - Automatically injects context when a new agent session starts in the same project

This is the core of context-bridge. Run it once and leave it running.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return err
			}
			setupLogging(cfg.Bridge.LogLevel)

			cmuxClient, store, sum, err := buildDeps(cfg)
			if err != nil {
				return err
			}
			defer cmuxClient.Close()
			defer store.Close()

			monCfg := monitor.DefaultConfig()
			monCfg.PollInterval = cfg.PollInterval()
			monCfg.MaxScrollback = cfg.Bridge.MaxScrollbackLines
			monCfg.SummarizeOnSync = summarizeOnSync

			engine := handoff.New(cmuxClient, store, sum)
			mon := monitor.New(cmuxClient, store, sum, monCfg)

			if autoInject {
				mon.OnNewSession = func(ctx parser.Context) {
					// Run auto-inject in a goroutine to avoid blocking the poll loop.
					go engine.AutoInject(ctx)
				}
			}

			mon.OnContextUpdate = func(ctx parser.Context) {
				slog.Debug("context updated",
					"agent", ctx.Agent,
					"goal", ctx.Task.Goal,
					"status", ctx.Task.Status,
					"surface", ctx.SurfaceID,
				)
			}

			mon.Start()

			fmt.Printf("context-bridge daemon running (auto-inject: %v). Press Ctrl+C to stop.\n", autoInject)

			sig := make(chan os.Signal, 1)
			signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
			<-sig

			mon.Stop()
			return nil
		},
	}

	cmd.Flags().BoolVar(&autoInject, "auto-inject", true,
		"Automatically inject context into new agent sessions in the same project (default: true)")
	cmd.Flags().BoolVar(&summarizeOnSync, "summarize-on-sync", false,
		"Call LLM summarizer on every sync cycle (expensive; use for always-fresh summaries)")

	return cmd
}

// ---- status ----------------------------------------------------------------

func statusCmd() *cobra.Command {
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show all tracked agent sessions",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return err
			}

			s, err := store.Open(cfg.Bridge.DBPath)
			if err != nil {
				return fmt.Errorf("open store: %w", err)
			}
			defer s.Close()

			sessions, err := s.ListAll()
			if err != nil {
				return err
			}

			if len(sessions) == 0 {
				fmt.Println("No active sessions tracked. Is the daemon running?")
				return nil
			}

			if jsonOut {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(sessions)
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "SESSION\tAGENT\tSTATUS\tWORKSPACE\tGOAL")
			fmt.Fprintln(w, "-------\t-----\t------\t---------\t----")
			for _, s := range sessions {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
					s.SessionID[:min(12, len(s.SessionID))],
					s.Agent,
					s.Task.Status,
					s.Workspace,
					truncate(s.Task.Goal, 50),
				)
			}
			return w.Flush()
		},
	}

	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON")
	return cmd
}

// ---- handoff ---------------------------------------------------------------

func handoffCmd() *cobra.Command {
	var fromSession string
	var toAgent string
	var note string
	var cwd string
	var noNewWorkspace bool

	cmd := &cobra.Command{
		Use:   "handoff",
		Short: "Hand off context from one agent session to another",
		Example: `  # Hand off the most recent session to Codex
  bridge handoff --to codex

  # Hand off a specific session to Gemini with a custom note
  bridge handoff --from sess-abc123 --to gemini --note "focus on the auth middleware"

  # Hand off to Aider without opening a new workspace
  bridge handoff --to aider --no-new-workspace`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if toAgent == "" {
				return fmt.Errorf("--to is required (claude-code | codex | gemini | aider | opencode)")
			}

			cfg, err := config.Load(cfgPath)
			if err != nil {
				return err
			}
			setupLogging(cfg.Bridge.LogLevel)

			cmuxClient, s, sum, err := buildDeps(cfg)
			if err != nil {
				return err
			}
			defer cmuxClient.Close()
			defer s.Close()

			engine := handoff.New(cmuxClient, s, sum)

			req := handoff.HandoffRequest{
				FromSessionID:      fromSession,
				ToAgent:            parser.AgentType(toAgent),
				Note:               note,
				CWD:                cwd,
				OpenInNewWorkspace: !noNewWorkspace,
			}

			fmt.Printf("Handing off context → %s...\n", toAgent)
			result, err := engine.Execute(req)
			if err != nil {
				return fmt.Errorf("handoff failed: %w", err)
			}

			fmt.Printf("Done! Opened workspace: %s\n", result.WorkspaceID)
			fmt.Printf("Injected prompt:\n---\n%s\n---\n", result.InjectedPrompt)
			return nil
		},
	}

	cmd.Flags().StringVar(&fromSession, "from", "", "Source session ID (default: most recent)")
	cmd.Flags().StringVar(&toAgent, "to", "", "Target agent type: claude-code | codex | gemini | aider | opencode")
	cmd.Flags().StringVar(&note, "note", "", "Extra instruction to append to the injection prompt")
	cmd.Flags().StringVar(&cwd, "cwd", "", "Override working directory for target session")
	cmd.Flags().BoolVar(&noNewWorkspace, "no-new-workspace", false, "Inject into the current pane instead of opening a new tab")

	return cmd
}

// ---- snapshot --------------------------------------------------------------

func snapshotCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "snapshot",
		Short: "Save and restore named context snapshots",
	}

	saveCmd := &cobra.Command{
		Use:     "save --name <name> [--session <id>]",
		Short:   "Save the current session context as a named snapshot",
		Example: `  bridge snapshot save --name auth-checkpoint`,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, _ := cmd.Flags().GetString("name")
			sessionID, _ := cmd.Flags().GetString("session")
			if name == "" {
				return fmt.Errorf("--name is required")
			}

			cfg, err := config.Load(cfgPath)
			if err != nil {
				return err
			}

			s, err := store.Open(cfg.Bridge.DBPath)
			if err != nil {
				return err
			}
			defer s.Close()

			var ctx *parser.Context
			if sessionID != "" {
				ctx, err = s.Get(sessionID)
			} else {
				all, lerr := s.ListAll()
				if lerr != nil || len(all) == 0 {
					return fmt.Errorf("no sessions found")
				}
				ctx = &all[0]
				err = nil
			}
			if err != nil {
				return err
			}

			if err := s.SaveSnapshot(name, ctx.SessionID, *ctx); err != nil {
				return err
			}
			fmt.Printf("Snapshot %q saved (session: %s, goal: %s)\n", name, ctx.SessionID, ctx.Task.Goal)
			return nil
		},
	}
	saveCmd.Flags().String("name", "", "Snapshot name")
	saveCmd.Flags().String("session", "", "Session ID (default: most recent)")

	loadCmd := &cobra.Command{
		Use:     "load --name <name> --to <agent>",
		Short:   "Restore a named snapshot into a new agent session",
		Example: `  bridge snapshot load --name auth-checkpoint --to codex`,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, _ := cmd.Flags().GetString("name")
			toAgent, _ := cmd.Flags().GetString("to")
			if name == "" || toAgent == "" {
				return fmt.Errorf("--name and --to are required")
			}

			cfg, err := config.Load(cfgPath)
			if err != nil {
				return err
			}
			setupLogging(cfg.Bridge.LogLevel)

			cmuxClient, s, sum, err := buildDeps(cfg)
			if err != nil {
				return err
			}
			defer cmuxClient.Close()
			defer s.Close()

			ctx, err := s.LoadSnapshot(name)
			if err != nil {
				return err
			}

			// Re-upsert so the engine can find it by session ID.
			_ = s.Upsert(*ctx)

			engine := handoff.New(cmuxClient, s, sum)
			result, err := engine.Execute(handoff.HandoffRequest{
				FromSessionID:      ctx.SessionID,
				ToAgent:            parser.AgentType(toAgent),
				OpenInNewWorkspace: true,
			})
			if err != nil {
				return err
			}
			fmt.Printf("Snapshot %q loaded into %s (workspace: %s)\n", name, toAgent, result.WorkspaceID)
			return nil
		},
	}
	loadCmd.Flags().String("name", "", "Snapshot name")
	loadCmd.Flags().String("to", "", "Target agent")

	cmd.AddCommand(saveCmd, loadCmd)
	return cmd
}

// ---- watch -----------------------------------------------------------------

func watchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Watch live session context updates (useful for debugging)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return err
			}
			setupLogging("debug")

			cmuxClient, s, sum, err := buildDeps(cfg)
			if err != nil {
				return err
			}
			defer cmuxClient.Close()
			defer s.Close()

			monCfg := monitor.DefaultConfig()
			monCfg.PollInterval = cfg.PollInterval()
			monCfg.MaxScrollback = cfg.Bridge.MaxScrollbackLines

			mon := monitor.New(cmuxClient, s, sum, monCfg)
			mon.OnNewSession = func(ctx parser.Context) {
				fmt.Printf("\n[%s] NEW SESSION: agent=%s goal=%q cwd=%s\n",
					time.Now().Format("15:04:05"), ctx.Agent, ctx.Task.Goal, ctx.CWD)
				if len(ctx.FileChanges) > 0 {
					fmt.Printf("  Files: ")
					for _, f := range ctx.FileChanges {
						fmt.Printf("%s(%s) ", f.Path, f.Op)
					}
					fmt.Println()
				}
			}
			mon.OnContextUpdate = func(ctx parser.Context) {
				fmt.Printf("[%s] UPDATE: agent=%-12s status=%-11s goal=%q\n",
					time.Now().Format("15:04:05"), ctx.Agent, ctx.Task.Status, truncate(ctx.Task.Goal, 40))
			}
			mon.Start()

			fmt.Println("Watching cmux sessions (Ctrl+C to stop)...")
			sig := make(chan os.Signal, 1)
			signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
			<-sig
			mon.Stop()
			return nil
		},
	}
	return cmd
}

// ---- test ------------------------------------------------------------------

func testCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "test",
		Short: "Test the cmux connection and print raw API responses",
		RunE: func(cmd *cobra.Command, args []string) error {
			socketPath := os.Getenv("CMUX_SOCKET_PATH")
			fmt.Printf("CMUX_SOCKET_PATH = %q\n\n", socketPath)
			if socketPath == "" {
				fmt.Println("ERROR: CMUX_SOCKET_PATH is not set.")
				fmt.Println("You must run bridge from inside a cmux terminal pane.")
				fmt.Println("Open cmux, open a terminal inside it, then run: bridge test")
				return nil
			}

			cfg, _ := config.Load(cfgPath)
			c, err := cmux.NewClient(cfg.SocketPath())
			if err != nil {
				fmt.Printf("ERROR connecting to cmux socket: %v\n", err)
				return nil
			}
			defer c.Close()
			fmt.Println("Connected to cmux socket OK.\n")

			// Test workspace.list
			fmt.Println("--- workspace.list ---")
			raw, err := c.RawCall("workspace.list", nil)
			if err != nil {
				fmt.Printf("ERROR: %v\n\n", err)
			} else {
				fmt.Printf("%s\n\n", prettyJSON(raw))
			}

			// Test surface.list
			fmt.Println("--- surface.list ---")
			raw, err = c.RawCall("surface.list", nil)
			if err != nil {
				fmt.Printf("ERROR: %v\n\n", err)
			} else {
				fmt.Printf("%s\n\n", prettyJSON(raw))
			}

			// Test pane.list
			fmt.Println("--- pane.list ---")
			raw, err = c.RawCall("pane.list", nil)
			if err != nil {
				fmt.Printf("ERROR: %v\n\n", err)
			} else {
				fmt.Printf("%s\n\n", prettyJSON(raw))
			}

			// Test window.list
			fmt.Println("--- window.list ---")
			raw, err = c.RawCall("window.list", nil)
			if err != nil {
				fmt.Printf("ERROR: %v\n\n", err)
			} else {
				fmt.Printf("%s\n\n", prettyJSON(raw))
			}

			return nil
		},
	}
}

func prettyJSON(raw []byte) string {
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	_ = enc.Encode(v)
	return buf.String()
}

// ---- helpers ---------------------------------------------------------------

func buildDeps(cfg *config.Config) (*cmux.Client, *store.Store, *summarizer.Summarizer, error) {
	cmuxClient, err := cmux.NewClient(cfg.SocketPath())
	if err != nil {
		return nil, nil, nil, fmt.Errorf("connect to cmux: %w\n\nMake sure cmux is running and CMUX_SOCKET_PATH is set.", err)
	}

	s, err := store.Open(cfg.Bridge.DBPath)
	if err != nil {
		cmuxClient.Close()
		return nil, nil, nil, fmt.Errorf("open store: %w", err)
	}

	// Summarizer is optional — if no API key, we skip LLM features.
	var sum *summarizer.Summarizer
	if key := cfg.Anthropic.APIKey; key != "" || os.Getenv("ANTHROPIC_API_KEY") != "" {
		sum, err = summarizer.New(key, cfg.Anthropic.Model, cfg.Anthropic.MaxTokens)
		if err != nil {
			slog.Warn("summarizer unavailable", "err", err)
		}
	} else {
		slog.Debug("ANTHROPIC_API_KEY not set — LLM summarization disabled; fallback summaries will be used")
	}

	return cmuxClient, s, sum, nil
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

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
