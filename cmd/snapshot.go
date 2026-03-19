package cmd

import (
	"fmt"

	"github.com/context-bridge/bridge/config"
	"github.com/context-bridge/bridge/handoff"
	"github.com/context-bridge/bridge/parser"
	"github.com/context-bridge/bridge/store"
	"github.com/spf13/cobra"
)

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
			result, err := engine.Execute(handoff.Request{
				FromSessionID:      ctx.SessionID,
				ToAgent:            parser.AgentType(toAgent),
				OpenInNewWorkspace: true,
			})
			if err != nil {
				return err
			}
			fmt.Printf("Snapshot %q loaded into %s (group: %s)\n", name, toAgent, result.GroupID)
			return nil
		},
	}
	loadCmd.Flags().String("name", "", "Snapshot name")
	loadCmd.Flags().String("to", "", "Target agent")

	cmd.AddCommand(saveCmd, loadCmd)
	return cmd
}
