package parser

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
// Falls back to Generic if none match. Order matters — more specific parsers run first.
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
