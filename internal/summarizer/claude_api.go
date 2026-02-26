// Package summarizer uses the Claude API to compress agent session context
// into a structured handoff brief.
package summarizer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/context-bridge/bridge/internal/parser"
)

const defaultModel = "claude-haiku-4-5-20251001"
const apiURL = "https://api.anthropic.com/v1/messages"

// Summarizer calls the Claude API to produce structured context summaries.
type Summarizer struct {
	apiKey    string
	model     string
	maxTokens int
	client    *http.Client
}

// New creates a Summarizer. apiKey may be empty to use ANTHROPIC_API_KEY env var.
func New(apiKey, model string, maxTokens int) (*Summarizer, error) {
	if apiKey == "" {
		apiKey = os.Getenv("ANTHROPIC_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("Anthropic API key not set: provide via config or ANTHROPIC_API_KEY env var")
	}
	if model == "" {
		model = defaultModel
	}
	if maxTokens == 0 {
		maxTokens = 2048
	}
	return &Summarizer{
		apiKey:    apiKey,
		model:     model,
		maxTokens: maxTokens,
		client:    &http.Client{Timeout: 60 * time.Second},
	}, nil
}

// Summarize compresses a context snapshot into a handoff-ready summary string.
func (s *Summarizer) Summarize(ctx parser.Context) (string, error) {
	systemPrompt := `You are a context bridge for AI coding agents.
Your job is to compress a terminal session snapshot into a concise handoff brief.
The brief will be injected at the start of a NEW agent session so it can continue seamlessly.

Output a brief in this exact format (plain text, no markdown headers needed, be concise):

TASK: <one sentence describing the current goal>
STATUS: <in_progress | idle | blocked | complete>
BLOCKER: <if blocked, describe it; otherwise omit this line>

DONE:
- <bullet: what was completed>

FILES CHANGED:
- <path> (<created|modified|deleted>)

KEY DECISIONS:
- <important technical choices made>

ERRORS SEEN:
- <relevant errors if any>

NEXT STEP: <what the next agent should do first>

Be concise. Focus on what matters for continuity. Omit empty sections.`

	userMsg := fmt.Sprintf(`Summarize this agent session for handoff.

Agent: %s
Working directory: %s
Git branch: %s
Captured: %s

=== RAW TERMINAL SNAPSHOT (last portion) ===
%s
`,
		ctx.Agent,
		ctx.CWD,
		ctx.GitBranch,
		ctx.CapturedAt.Format(time.RFC3339),
		truncate(ctx.RawSnapshot, 6000),
	)

	return s.call(systemPrompt, userMsg)
}

// GenerateInjectionPrompt generates the opening message to inject into a new agent session.
func (s *Summarizer) GenerateInjectionPrompt(ctx parser.Context, targetAgent parser.AgentType, note string) (string, error) {
	systemPrompt := fmt.Sprintf(`You are a context bridge generating a handoff prompt for %s.
The prompt will be sent as the FIRST message to a new %s session so it continues work from another agent.
Write it as a direct instruction to the agent. Be concise, actionable, and complete.
Start with "# Context Handoff" and include all information needed to continue immediately.`, targetAgent, targetAgent)

	userMsg := fmt.Sprintf(`Generate a handoff prompt based on this session summary.
Target agent: %s
Extra note from user: %s

Session summary:
%s

File changes:
%s

Errors encountered:
%s
`,
		targetAgent,
		note,
		ctx.Summary,
		formatFileChanges(ctx.FileChanges),
		formatList(ctx.ErrorsEncountered),
	)

	return s.call(systemPrompt, userMsg)
}

func (s *Summarizer) call(system, user string) (string, error) {
	body := map[string]any{
		"model":      s.model,
		"max_tokens": s.maxTokens,
		"system":     system,
		"messages": []map[string]any{
			{"role": "user", "content": user},
		},
	}
	data, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", apiURL, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", s.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("claude API request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("claude API error %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("decode claude response: %w", err)
	}
	if len(result.Content) == 0 {
		return "", fmt.Errorf("empty claude response")
	}
	return result.Content[0].Text, nil
}

func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[len(runes)-max:])
}

func formatFileChanges(changes []parser.FileChange) string {
	if len(changes) == 0 {
		return "(none)"
	}
	var b bytes.Buffer
	for _, c := range changes {
		fmt.Fprintf(&b, "- %s (%s)\n", c.Path, c.Op)
	}
	return b.String()
}

func formatList(items []string) string {
	if len(items) == 0 {
		return "(none)"
	}
	var b bytes.Buffer
	for _, item := range items {
		fmt.Fprintf(&b, "- %s\n", item)
	}
	return b.String()
}
