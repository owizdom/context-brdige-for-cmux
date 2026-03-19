package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/context-bridge/bridge/config"
	"github.com/context-bridge/bridge/terminal"
	"github.com/spf13/cobra"
)

func diagnoseCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "diagnose",
		Aliases: []string{"test"},
		Short:   "Test the terminal backend connection",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _ := config.Load(cfgPath)

			backend, err := terminal.Detect(forceBackend, cfg.SocketPath())
			if err != nil {
				fmt.Printf("ERROR: %v\n\n", err)
				fmt.Println("Make sure you're running inside a supported terminal multiplexer:")
				fmt.Println("  cmux: set CMUX_SOCKET_PATH or run inside cmux")
				fmt.Println("  tmux: run inside a tmux session")
				return nil
			}
			defer backend.Close()

			fmt.Printf("Backend: %s\n\n", backend.Name())

			fmt.Println("--- Groups (workspaces/windows) ---")
			groups, err := backend.ListGroups()
			if err != nil {
				fmt.Printf("ERROR: %v\n\n", err)
			} else {
				for _, g := range groups {
					fmt.Printf("  %s  title=%q  cwd=%s\n", g.ID, g.Title, g.CurrentDir)
				}
				fmt.Println()
			}

			fmt.Println("--- Sessions (panes/surfaces) ---")
			sessions, err := backend.ListSessions("")
			if err != nil {
				fmt.Printf("ERROR: %v\n\n", err)
			} else {
				for _, s := range sessions {
					fmt.Printf("  %s  group=%s  title=%q  cwd=%s\n", s.ID, s.GroupID, s.Title, s.CurrentDir)
				}
				fmt.Println()
			}

			// If cmux, also show raw API responses for debugging.
			if cmuxBackend, ok := backend.(*terminal.CmuxBackend); ok {
				for _, endpoint := range []string{"workspace.list", "surface.list"} {
					fmt.Printf("--- raw: %s ---\n", endpoint)
					raw, err := cmuxBackend.RawCall(endpoint, nil)
					if err != nil {
						fmt.Printf("ERROR: %v\n", err)
					} else {
						fmt.Println(prettyJSON(raw))
					}
				}
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
