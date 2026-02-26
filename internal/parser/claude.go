package parser

import (
	"regexp"
	"strings"
	"time"
)

// ClaudeCodeParser parses Claude Code terminal sessions.
type ClaudeCodeParser struct{}

func (p *ClaudeCodeParser) AgentType() AgentType { return AgentClaudeCode }

var (
	claudePromptRe  = regexp.MustCompile(`(?m)^>\s+`)
	claudeToolRe    = regexp.MustCompile(`(?m)^(●|◉|✓|⎿|│)\s+`)
	claudeEditRe    = regexp.MustCompile(`(?m)Edit\(([^)]+)\)`)
	claudeReadRe    = regexp.MustCompile(`(?m)Read\(([^)]+)\)`)
	claudeWriteRe   = regexp.MustCompile(`(?m)Write\(([^)]+)\)`)
	claudeBashRe    = regexp.MustCompile(`(?m)Bash\(([^)]+)\)`)
	claudeErrorRe   = regexp.MustCompile(`(?mi)(error|exception|failed|cannot|unable|undefined)[^\n]{0,120}`)
	claudeHumanRe   = regexp.MustCompile(`(?m)^Human:\s*(.+)`)
	claudeAssistRe  = regexp.MustCompile(`(?m)^Assistant:\s*(.+)`)
)

func (p *ClaudeCodeParser) Detect(s string) bool {
	return strings.Contains(s, "Claude Code") ||
		strings.Contains(s, "claude-code") ||
		claudePromptRe.MatchString(s) ||
		(strings.Contains(s, "Human:") && strings.Contains(s, "Assistant:"))
}

func (p *ClaudeCodeParser) Parse(scrollback string) Context {
	ctx := Context{
		Agent:       AgentClaudeCode,
		CapturedAt:  time.Now(),
		RawSnapshot: scrollback,
	}

	// Extract file changes.
	ctx.FileChanges = append(ctx.FileChanges, extractMatches(claudeEditRe, scrollback, "modified")...)
	ctx.FileChanges = append(ctx.FileChanges, extractMatches(claudeWriteRe, scrollback, "created")...)
	ctx.FileChanges = append(ctx.FileChanges, extractMatches(claudeReadRe, scrollback, "read")...)
	ctx.FileChanges = deduplicateChanges(ctx.FileChanges)

	// Extract errors.
	errMatches := claudeErrorRe.FindAllString(scrollback, 20)
	seen := map[string]bool{}
	for _, e := range errMatches {
		e = strings.TrimSpace(e)
		if !seen[e] && len(e) > 10 {
			ctx.ErrorsEncountered = append(ctx.ErrorsEncountered, e)
			seen[e] = true
		}
	}

	// Extract task goal: last Human: turn or last > prompt line.
	ctx.Task = extractClaudeTask(scrollback)

	// Conversation excerpt: last 40 lines.
	ctx.ConversationExcerpt = lastNLines(scrollback, 40)

	return ctx
}

func extractClaudeTask(s string) Task {
	task := Task{Status: StatusInProgress}

	// Try Human:/Assistant: format first.
	humanMatches := claudeHumanRe.FindAllStringSubmatch(s, -1)
	if len(humanMatches) > 0 {
		last := humanMatches[len(humanMatches)-1]
		task.Goal = strings.TrimSpace(last[1])
	}

	// Detect if agent is idle (waiting for input).
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > 0 {
		lastLine := strings.TrimSpace(lines[len(lines)-1])
		if lastLine == ">" || lastLine == "> " || strings.HasSuffix(lastLine, "$ ") {
			task.Status = StatusIdle
		}
	}

	// Detect blockers from error patterns in recent lines.
	recent := lastNLines(s, 20)
	if claudeErrorRe.MatchString(recent) && task.Status == StatusIdle {
		task.Status = StatusBlocked
		errs := claudeErrorRe.FindAllString(recent, 3)
		if len(errs) > 0 {
			task.CurrentBlocker = strings.TrimSpace(errs[len(errs)-1])
		}
	}

	return task
}

func extractMatches(re *regexp.Regexp, s, op string) []FileChange {
	var changes []FileChange
	matches := re.FindAllStringSubmatch(s, -1)
	for _, m := range matches {
		if len(m) > 1 {
			path := strings.TrimSpace(m[1])
			// Strip quotes and trailing content after comma.
			path = strings.Trim(path, `"'`)
			if idx := strings.Index(path, ","); idx > 0 {
				path = strings.TrimSpace(path[:idx])
			}
			if path != "" {
				changes = append(changes, FileChange{Path: path, Op: op})
			}
		}
	}
	return changes
}

func deduplicateChanges(changes []FileChange) []FileChange {
	seen := map[string]string{}
	var out []FileChange
	for _, c := range changes {
		prev, exists := seen[c.Path]
		if !exists {
			seen[c.Path] = c.Op
			out = append(out, c)
		} else if prev == "read" && c.Op != "read" {
			// Upgrade read -> modified/created.
			seen[c.Path] = c.Op
			for i, o := range out {
				if o.Path == c.Path {
					out[i].Op = c.Op
					break
				}
			}
		}
	}
	return out
}

func lastNLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}
