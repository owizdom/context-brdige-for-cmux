package parser

import (
	"regexp"
	"strings"
	"time"
)

// AiderParser parses Aider terminal sessions.
type AiderParser struct{}

func (p *AiderParser) AgentType() AgentType { return AgentAider }

var (
	aiderPromptRe  = regexp.MustCompile(`(?m)^aider>\s*`)
	aiderAppliedRe = regexp.MustCompile(`(?m)Applied edit to ([^\n]+)`)
	aiderCreatedRe = regexp.MustCompile(`(?m)Created ([^\n]+)`)
	aiderErrorRe   = regexp.MustCompile(`(?mi)(error|failed|traceback)[^\n]{0,120}`)
)

func (p *AiderParser) Detect(s string) bool {
	return aiderPromptRe.MatchString(s) ||
		strings.Contains(s, "aider>") ||
		strings.Contains(s, "Aider v") ||
		strings.Contains(s, "Applied edit to")
}

func (p *AiderParser) Parse(scrollback string) Context {
	ctx := Context{
		Agent:       AgentAider,
		CapturedAt:  time.Now(),
		RawSnapshot: scrollback,
	}

	for _, m := range aiderAppliedRe.FindAllStringSubmatch(scrollback, -1) {
		if len(m) > 1 {
			ctx.FileChanges = append(ctx.FileChanges, FileChange{Path: strings.TrimSpace(m[1]), Op: "modified"})
		}
	}
	for _, m := range aiderCreatedRe.FindAllStringSubmatch(scrollback, -1) {
		if len(m) > 1 {
			ctx.FileChanges = append(ctx.FileChanges, FileChange{Path: strings.TrimSpace(m[1]), Op: "created"})
		}
	}
	ctx.FileChanges = deduplicateChanges(ctx.FileChanges)

	seen := map[string]bool{}
	for _, e := range aiderErrorRe.FindAllString(scrollback, 10) {
		e = strings.TrimSpace(e)
		if !seen[e] {
			ctx.ErrorsEncountered = append(ctx.ErrorsEncountered, e)
			seen[e] = true
		}
	}

	task := Task{Status: StatusInProgress}
	lines := strings.Split(scrollback, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.HasPrefix(lines[i], "aider> ") {
			task.Goal = strings.TrimPrefix(lines[i], "aider> ")
			break
		}
	}
	ctx.Task = task
	ctx.ConversationExcerpt = lastNLines(scrollback, 40)
	return ctx
}
