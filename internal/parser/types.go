// Package parser defines the Universal Context Format (UCF) and per-agent parsers.
package parser

import "time"

// AgentType identifies which AI coding agent is running in a session.
type AgentType string

const (
	AgentClaudeCode AgentType = "claude-code"
	AgentCodex      AgentType = "codex"
	AgentGeminiCLI  AgentType = "gemini-cli"
	AgentAider      AgentType = "aider"
	AgentOpenCode   AgentType = "opencode"
	AgentUnknown    AgentType = "unknown"
)

// TaskStatus is the inferred state of the current task.
type TaskStatus string

const (
	StatusInProgress TaskStatus = "in_progress"
	StatusIdle       TaskStatus = "idle"
	StatusBlocked    TaskStatus = "blocked"
	StatusComplete   TaskStatus = "complete"
)

// FileChange records a file operation the agent performed.
type FileChange struct {
	Path string `json:"path"`
	// Op is one of: created | modified | deleted | read
	Op string `json:"op"`
}

// Task holds the extracted goal and progress of the current agent session.
type Task struct {
	Goal               string     `json:"goal"`
	Status             TaskStatus `json:"status"`
	SubtasksCompleted  []string   `json:"subtasks_completed,omitempty"`
	CurrentBlocker     string     `json:"current_blocker,omitempty"`
}

// Context is the Universal Context Format (UCF) — the canonical representation
// of an agent session that can be handed off to any other agent.
type Context struct {
	SessionID   string    `json:"session_id"`
	SurfaceID   string    `json:"surface_id"`
	WorkspaceID string    `json:"workspace_id"`
	Agent       AgentType `json:"agent"`
	Workspace   string    `json:"workspace_title,omitempty"`
	CWD         string    `json:"cwd,omitempty"`
	GitBranch   string    `json:"git_branch,omitempty"`
	CapturedAt  time.Time `json:"captured_at"`

	Task             Task         `json:"task"`
	FileChanges      []FileChange `json:"file_changes,omitempty"`
	KeyDecisions     []string     `json:"key_decisions,omitempty"`
	ErrorsEncountered []string    `json:"errors_encountered,omitempty"`

	// ConversationExcerpt is the last N turns, cleaned up for injection.
	ConversationExcerpt string `json:"conversation_excerpt,omitempty"`

	// RawSnapshot is the full unprocessed scrollback used for re-summarization.
	RawSnapshot string `json:"raw_snapshot"`

	// Summary is filled in by the LLM summarizer before handoff.
	Summary string `json:"summary,omitempty"`
}

// Parser extracts a Context from raw terminal scrollback.
type Parser interface {
	// Detect returns true if this parser recognizes the agent in the scrollback.
	Detect(scrollback string) bool
	// Parse extracts a Context from the scrollback.
	Parse(scrollback string) Context
	// AgentType returns the agent type this parser handles.
	AgentType() AgentType
}

// DetectAgent runs all known parsers against scrollback and returns the first match.
// Falls back to Generic if none match.
func DetectAgent(scrollback string) Parser {
	parsers := []Parser{
		&ClaudeCodeParser{},
		&CodexParser{},
		&GeminiParser{},
		&AiderParser{},
	}
	for _, p := range parsers {
		if p.Detect(scrollback) {
			return p
		}
	}
	return &GenericParser{}
}
