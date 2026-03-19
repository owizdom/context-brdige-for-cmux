package cmd

import (
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/context-bridge/bridge/config"
	"github.com/context-bridge/bridge/handoff"
	"github.com/context-bridge/bridge/monitor"
	"github.com/context-bridge/bridge/parser"
	"github.com/spf13/cobra"
)

func daemonCmd() *cobra.Command {
	var autoInject bool
	var summarizeOnSync bool

	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Start the mnemo daemon (autonomous sync)",
		Long: `Runs the background daemon that:
  - Polls all terminal sessions every N seconds
  - Detects which AI agent is running in each pane
  - Extracts and stores context (task, files changed, errors, conversation)
  - Automatically injects context when a new agent session starts in the same project

This is the core of mnemo. Run it once and leave it running.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return err
			}
			setupLogging(cfg.Bridge.LogLevel)

			backend, store, sum, err := buildDeps(cfg)
			if err != nil {
				return err
			}
			defer backend.Close()
			defer store.Close()

			monCfg := monitor.DefaultConfig()
			monCfg.PollInterval = cfg.PollInterval()
			monCfg.MaxScrollback = cfg.Bridge.MaxScrollbackLines
			monCfg.SummarizeOnSync = summarizeOnSync

			engine := handoff.New(backend, store, sum)
			mon := monitor.New(backend, store, sum, monCfg)

			if autoInject {
				mon.OnNewSession = func(ctx parser.Context) {
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

			fmt.Printf("mnemo daemon running (backend: %s, auto-inject: %v). Press Ctrl+C to stop.\n", backend.Name(), autoInject)

			sig := make(chan os.Signal, 1)
			signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
			<-sig

			mon.Stop()
			return nil
		},
	}

	cmd.Flags().BoolVar(&autoInject, "auto-inject", true,
		"Automatically inject context into new agent sessions in the same project")
	cmd.Flags().BoolVar(&summarizeOnSync, "summarize-on-sync", false,
		"Call LLM summarizer on every sync cycle (expensive; use for always-fresh summaries)")

	return cmd
}
