package parser

import (
	"regexp"
	"strings"
	"time"
)

// GenericParser is the fallback parser for unknown agents.
type GenericParser struct{}

func (p *GenericParser) AgentType() AgentType { return AgentUnknown }

var genericErrorRe = regexp.MustCompile(`(?mi)(error|exception|failed|traceback)[^\n]{0,120}`)

func (p *GenericParser) Detect(_ string) bool { return true }

func (p *GenericParser) Parse(scrollback string) Context {
	ctx := Context{
		Agent:               AgentUnknown,
		CapturedAt:          time.Now(),
		RawSnapshot:         scrollback,
		ConversationExcerpt: lastNLines(scrollback, 50),
	}

	seen := map[string]bool{}
	for _, e := range genericErrorRe.FindAllString(scrollback, 10) {
		e = strings.TrimSpace(e)
		if !seen[e] {
			ctx.ErrorsEncountered = append(ctx.ErrorsEncountered, e)
			seen[e] = true
		}
	}

	// Best-effort goal: last non-empty, non-prompt line.
	lines := strings.Split(strings.TrimSpace(scrollback), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line != "" && !strings.HasSuffix(line, "$") && !strings.HasSuffix(line, ">") {
			ctx.Task = Task{Goal: line, Status: StatusUnknown()}
			break
		}
	}

	return ctx
}

func StatusUnknown() TaskStatus { return StatusIdle }
