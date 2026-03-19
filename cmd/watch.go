package cmd

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/context-bridge/bridge/config"
	"github.com/context-bridge/bridge/monitor"
	"github.com/context-bridge/bridge/parser"
	"github.com/spf13/cobra"
)

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

			backend, s, sum, err := buildDeps(cfg)
			if err != nil {
				return err
			}
			defer backend.Close()
			defer s.Close()

			monCfg := monitor.DefaultConfig()
			monCfg.PollInterval = cfg.PollInterval()
			monCfg.MaxScrollback = cfg.Bridge.MaxScrollbackLines

			mon := monitor.New(backend, s, sum, monCfg)
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

			fmt.Printf("Watching sessions via %s (Ctrl+C to stop)...\n", backend.Name())
			sig := make(chan os.Signal, 1)
			signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
			<-sig
			mon.Stop()
			return nil
		},
	}
	return cmd
}
