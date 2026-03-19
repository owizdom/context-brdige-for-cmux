package parser

import (
	"regexp"
	"strings"
	"time"
)

// CodexParser parses OpenAI Codex CLI terminal sessions.
type CodexParser struct{}

func (p *CodexParser) AgentType() AgentType { return AgentCodex }

var (
	codexPromptRe      = regexp.MustCompile(`(?m)^codex>\s*`)
	codexPromptInputRe = regexp.MustCompile(`(?m)^›\s*(.+)`)
	codexApplyRe       = regexp.MustCompile(`(?m)(applying patch|writing file|creating file)[^\n]*:\s*([^\n]+)`)
	codexUserRe        = regexp.MustCompile(`(?m)^(user|you):\s*(.+)`)
	codexAssistRe      = regexp.MustCompile(`(?m)^(assistant|codex):\s*(.+)`)
	codexErrorRe       = regexp.MustCompile(`(?mi)^\s*(error|exception)\b[^\n]{0,120}`)
)

func (p *CodexParser) Detect(s string) bool {
	return codexPromptRe.MatchString(s) ||
		strings.Contains(s, "codex>") ||
		strings.Contains(s, "Codex CLI") ||
		(strings.Contains(s, "applying patch") && strings.Contains(s, "writing file"))
}

func (p *CodexParser) Parse(scrollback string) Context {
	ctx := Context{
		Agent:       AgentCodex,
		CapturedAt:  time.Now(),
		RawSnapshot: scrollback,
		Task:        Task{Status: StatusInProgress},
	}

	matches := codexApplyRe.FindAllStringSubmatch(scrollback, -1)
	for _, m := range matches {
		if len(m) > 2 {
			op := "modified"
			if strings.Contains(m[1], "creat") {
				op = "created"
			}
			ctx.FileChanges = append(ctx.FileChanges, FileChange{
				Path: strings.TrimSpace(m[2]),
				Op:   op,
			})
		}
	}
	ctx.FileChanges = deduplicateChanges(ctx.FileChanges)

	errMatches := codexErrorRe.FindAllString(scrollback, 10)
	seen := map[string]bool{}
	for _, e := range errMatches {
		e = strings.TrimSpace(e)
		if !seen[e] {
			ctx.ErrorsEncountered = append(ctx.ErrorsEncountered, e)
			seen[e] = true
		}
	}

	userMatches := codexUserRe.FindAllStringSubmatch(scrollback, -1)
	if len(userMatches) > 0 {
		ctx.Task.Goal = strings.TrimSpace(userMatches[len(userMatches)-1][2])
	}
	if ctx.Task.Goal == "" {
		promptMatches := codexPromptInputRe.FindAllStringSubmatch(scrollback, -1)
		if len(promptMatches) > 0 {
			ctx.Task.Goal = strings.TrimSpace(promptMatches[len(promptMatches)-1][1])
		}
	}

	lines := strings.Split(strings.TrimSpace(scrollback), "\n")
	if len(lines) > 0 {
		last := strings.TrimSpace(lines[len(lines)-1])
		if strings.HasSuffix(last, "> ") || last == "codex>" {
			ctx.Task.Status = StatusIdle
		}
	}
	ctx.ConversationExcerpt = lastNLines(scrollback, 40)
	return ctx
}
