package parser

import (
	"regexp"
	"strings"
	"time"
)

// GeminiParser parses Google Gemini CLI terminal sessions.
type GeminiParser struct{}

func (p *GeminiParser) AgentType() AgentType { return AgentGeminiCLI }

var (
	geminiPromptRe = regexp.MustCompile(`(?m)^gemini>\s*|^\$\s+gemini\s`)
	geminiFileRe   = regexp.MustCompile(`(?m)(create|edit|modify|write|update)\s+['` + "`" + `"]?([^\s'` + "`" + `"\n]+\.[a-zA-Z]{1,6})['` + "`" + `"]?`)
	geminiErrorRe  = regexp.MustCompile(`(?mi)(error|failed|exception)[^\n]{0,120}`)
	geminiUserRe   = regexp.MustCompile(`(?m)^\[user\]\s*(.+)|^>\s+(.+)`)
)

func (p *GeminiParser) Detect(s string) bool {
	return geminiPromptRe.MatchString(s) ||
		strings.Contains(s, "gemini>") ||
		strings.Contains(s, "gemini-cli") ||
		strings.Contains(s, "Gemini CLI")
}

func (p *GeminiParser) Parse(scrollback string) Context {
	ctx := Context{
		Agent:       AgentGeminiCLI,
		CapturedAt:  time.Now(),
		RawSnapshot: scrollback,
	}

	matches := geminiFileRe.FindAllStringSubmatch(scrollback, -1)
	for _, m := range matches {
		if len(m) > 2 {
			op := "modified"
			if strings.Contains(m[1], "creat") || strings.Contains(m[1], "write") {
				op = "created"
			}
			ctx.FileChanges = append(ctx.FileChanges, FileChange{Path: m[2], Op: op})
		}
	}
	ctx.FileChanges = deduplicateChanges(ctx.FileChanges)

	errMatches := geminiErrorRe.FindAllString(scrollback, 10)
	seen := map[string]bool{}
	for _, e := range errMatches {
		e = strings.TrimSpace(e)
		if !seen[e] {
			ctx.ErrorsEncountered = append(ctx.ErrorsEncountered, e)
			seen[e] = true
		}
	}

	task := Task{Status: StatusInProgress}
	userMatches := geminiUserRe.FindAllStringSubmatch(scrollback, -1)
	if len(userMatches) > 0 {
		last := userMatches[len(userMatches)-1]
		if last[1] != "" {
			task.Goal = strings.TrimSpace(last[1])
		} else {
			task.Goal = strings.TrimSpace(last[2])
		}
	}
	ctx.Task = task
	ctx.ConversationExcerpt = lastNLines(scrollback, 40)
	return ctx
}
