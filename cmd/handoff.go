package cmd

import (
	"fmt"

	"github.com/context-bridge/bridge/config"
	"github.com/context-bridge/bridge/handoff"
	"github.com/context-bridge/bridge/parser"
	"github.com/spf13/cobra"
)

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

			req := handoff.Request{
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

			fmt.Printf("Done! Opened in: %s\n", result.GroupID)
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
