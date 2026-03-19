package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/context-bridge/bridge/config"
	"github.com/context-bridge/bridge/store"
	"github.com/spf13/cobra"
)

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
