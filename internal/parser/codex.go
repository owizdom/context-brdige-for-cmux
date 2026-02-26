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
	codexPromptRe  = regexp.MustCompile(`(?m)^codex>\s*`)
	codexApplyRe   = regexp.MustCompile(`(?m)(applying patch|writing file|creating file)[^\n]*:\s*([^\n]+)`)
	codexUserRe    = regexp.MustCompile(`(?m)^(user|you):\s*(.+)`)
	codexAssistRe  = regexp.MustCompile(`(?m)^(assistant|codex):\s*(.+)`)
	codexErrorRe   = regexp.MustCompile(`(?mi)(error|failed|exception)[^\n]{0,120}`)
)

func (p *CodexParser) Detect(s string) bool {
	return strings.Contains(s, "codex") ||
		codexPromptRe.MatchString(s) ||
		(strings.Contains(s, "applying patch") && strings.Contains(s, "writing file"))
}

func (p *CodexParser) Parse(scrollback string) Context {
	ctx := Context{
		Agent:       AgentCodex,
		CapturedAt:  time.Now(),
		RawSnapshot: scrollback,
	}

	// Extract file changes from "applying patch: path" lines.
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

	// Errors.
	errMatches := codexErrorRe.FindAllString(scrollback, 10)
	seen := map[string]bool{}
	for _, e := range errMatches {
		e = strings.TrimSpace(e)
		if !seen[e] {
			ctx.ErrorsEncountered = append(ctx.ErrorsEncountered, e)
			seen[e] = true
		}
	}

	// Task goal from last user: turn.
	userMatches := codexUserRe.FindAllStringSubmatch(scrollback, -1)
	task := Task{Status: StatusInProgress}
	if len(userMatches) > 0 {
		task.Goal = strings.TrimSpace(userMatches[len(userMatches)-1][2])
	}

	// Idle detection.
	lines := strings.Split(strings.TrimSpace(scrollback), "\n")
	if len(lines) > 0 {
		last := strings.TrimSpace(lines[len(lines)-1])
		if strings.HasSuffix(last, "> ") || last == "codex>" {
			task.Status = StatusIdle
		}
	}
	ctx.Task = task
	ctx.ConversationExcerpt = lastNLines(scrollback, 40)
	return ctx
}
