package parser

import (
	"testing"
)

// --- Detection tests ---

func TestDetectAgent_ClaudeCode(t *testing.T) {
	cases := []string{
		"Claude Code v1.0.0\n\nHuman: Add auth\n\nAssistant: I'll add auth...",
		"Human: add JWT middleware\n\nAssistant: Working on it...",
	}
	for _, s := range cases {
		p := DetectAgent(s)
		if p.AgentType() != AgentClaudeCode {
			t.Errorf("expected claude-code, got %s for input starting with %q", p.AgentType(), s[:min(40, len(s))])
		}
	}
}

func TestDetectAgent_Codex(t *testing.T) {
	s := "codex> fix the tests\napplying patch: src/auth.js\nwriting file: src/auth.js"
	p := DetectAgent(s)
	if p.AgentType() != AgentCodex {
		t.Errorf("expected codex, got %s", p.AgentType())
	}
}

func TestDetectAgent_Gemini(t *testing.T) {
	s := "gemini> create a new React component\n[user] build a form"
	p := DetectAgent(s)
	if p.AgentType() != AgentGeminiCLI {
		t.Errorf("expected gemini-cli, got %s", p.AgentType())
	}
}

func TestDetectAgent_Aider(t *testing.T) {
	s := "Aider v0.50\naider> refactor auth module\nApplied edit to src/auth.py"
	p := DetectAgent(s)
	if p.AgentType() != AgentAider {
		t.Errorf("expected aider, got %s", p.AgentType())
	}
}

func TestDetectAgent_FallbackGeneric(t *testing.T) {
	s := "user@host ~ % ls -la\ntotal 42\ndrwxr-xr-x  5 user staff 160 Jan  1 00:00 ."
	p := DetectAgent(s)
	if p.AgentType() != AgentUnknown {
		t.Errorf("expected unknown, got %s", p.AgentType())
	}
}

func TestDetectAgent_NoFalsePositiveGemini(t *testing.T) {
	s := "The Gemini constellation is visible tonight.\nStars are beautiful."
	p := DetectAgent(s)
	if p.AgentType() == AgentGeminiCLI {
		t.Error("should not detect gemini-cli from casual mention of Gemini")
	}
}

// --- Claude parser extraction tests ---

func TestClaudeParser_ExtractsFileChanges(t *testing.T) {
	scrollback := "Human: Add authentication\n\nAssistant: I'll create the auth module.\n\n" +
		"Edit(src/middleware/auth.js)\nWrite(src/config/jwt.js)\nRead(src/routes/user.js)\n"

	p := &ClaudeCodeParser{}
	ctx := p.Parse(scrollback)

	if len(ctx.FileChanges) != 3 {
		t.Fatalf("expected 3 file changes, got %d", len(ctx.FileChanges))
	}

	want := map[string]string{
		"src/middleware/auth.js": "modified",
		"src/config/jwt.js":     "created",
		"src/routes/user.js":    "read",
	}
	for _, fc := range ctx.FileChanges {
		expectedOp, ok := want[fc.Path]
		if !ok {
			t.Errorf("unexpected file change: %s", fc.Path)
			continue
		}
		if fc.Op != expectedOp {
			t.Errorf("file %s: expected op %q, got %q", fc.Path, expectedOp, fc.Op)
		}
	}
}

func TestClaudeParser_ExtractsGoal(t *testing.T) {
	scrollback := "Human: Fix the login bug\n\nAssistant: Looking at it now.\n\nHuman: Also check the JWT expiry\n\nAssistant: Sure."
	p := &ClaudeCodeParser{}
	ctx := p.Parse(scrollback)

	if ctx.Task.Goal != "Also check the JWT expiry" {
		t.Errorf("expected last Human turn as goal, got %q", ctx.Task.Goal)
	}
}

func TestClaudeParser_DetectsErrors(t *testing.T) {
	scrollback := "Human: Run the tests\n\nAssistant: Running...\n\nCannot read property 'verify' of undefined at auth.js:34\nerror: test suite failed\n"
	p := &ClaudeCodeParser{}
	ctx := p.Parse(scrollback)

	if len(ctx.ErrorsEncountered) == 0 {
		t.Fatal("expected errors to be extracted")
	}
}

func TestClaudeParser_DeduplicatesFiles(t *testing.T) {
	scrollback := "Human: Fix auth\n\nAssistant: ok\n\nRead(src/auth.js)\nEdit(src/auth.js)\n"
	p := &ClaudeCodeParser{}
	ctx := p.Parse(scrollback)

	if len(ctx.FileChanges) != 1 {
		t.Fatalf("expected 1 deduplicated file change, got %d", len(ctx.FileChanges))
	}
	if ctx.FileChanges[0].Op != "modified" {
		t.Errorf("expected read to be upgraded to modified, got %q", ctx.FileChanges[0].Op)
	}
}

// --- Codex parser tests ---

func TestCodexParser_ExtractsPatches(t *testing.T) {
	scrollback := "codex> add error handling\napplying patch: src/api.ts\ncreating file: src/errors.ts\n"
	p := &CodexParser{}
	ctx := p.Parse(scrollback)

	if len(ctx.FileChanges) != 2 {
		t.Fatalf("expected 2 file changes, got %d", len(ctx.FileChanges))
	}
}

// --- Aider parser tests ---

func TestAiderParser_ExtractsEdits(t *testing.T) {
	scrollback := "aider> refactor the database layer\nApplied edit to src/db.py\nCreated src/db_utils.py\n"
	p := &AiderParser{}
	ctx := p.Parse(scrollback)

	if len(ctx.FileChanges) != 2 {
		t.Fatalf("expected 2 file changes, got %d", len(ctx.FileChanges))
	}
	if ctx.Task.Goal != "refactor the database layer" {
		t.Errorf("unexpected goal: %q", ctx.Task.Goal)
	}
}

// --- Helper tests ---

func TestLastNLines(t *testing.T) {
	s := "a\nb\nc\nd\ne"
	result := lastNLines(s, 3)
	if result != "c\nd\ne" {
		t.Errorf("expected last 3 lines, got %q", result)
	}
}

func TestLastNLines_FewerThanN(t *testing.T) {
	s := "a\nb"
	result := lastNLines(s, 10)
	if result != s {
		t.Errorf("expected full string when fewer than N lines")
	}
}

func TestDeduplicateChanges_UpgradesReadToModified(t *testing.T) {
	changes := []FileChange{
		{Path: "a.go", Op: "read"},
		{Path: "b.go", Op: "created"},
		{Path: "a.go", Op: "modified"},
	}
	result := deduplicateChanges(changes)
	if len(result) != 2 {
		t.Fatalf("expected 2, got %d", len(result))
	}
	for _, c := range result {
		if c.Path == "a.go" && c.Op != "modified" {
			t.Errorf("expected a.go to be upgraded to modified, got %s", c.Op)
		}
	}
}
