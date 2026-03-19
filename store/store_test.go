package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/context-bridge/bridge/parser"
)

func tempStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestStore_UpsertAndGet(t *testing.T) {
	s := tempStore(t)

	ctx := parser.Context{
		SessionID:   "sess-001",
		SurfaceID:   "surf-abc",
		WorkspaceID: "ws-001",
		Agent:       parser.AgentClaudeCode,
		CWD:         "/tmp/project",
		GitBranch:   "main",
		CapturedAt:  time.Now(),
		Task: parser.Task{
			Goal:   "Add JWT auth",
			Status: parser.StatusInProgress,
		},
		FileChanges: []parser.FileChange{
			{Path: "auth.js", Op: "created"},
		},
		ErrorsEncountered:   []string{"cannot read property"},
		ConversationExcerpt: "last 40 lines...",
		RawSnapshot:         "full scrollback...",
	}

	if err := s.Upsert(ctx); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := s.Get("sess-001")
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if got.Agent != parser.AgentClaudeCode {
		t.Errorf("agent: got %s, want claude-code", got.Agent)
	}
	if got.Task.Goal != "Add JWT auth" {
		t.Errorf("goal: got %q, want %q", got.Task.Goal, "Add JWT auth")
	}
	if len(got.FileChanges) != 1 {
		t.Errorf("file changes: got %d, want 1", len(got.FileChanges))
	}
	if len(got.ErrorsEncountered) != 1 {
		t.Errorf("errors: got %d, want 1", len(got.ErrorsEncountered))
	}
}

func TestStore_UpsertUpdatesExisting(t *testing.T) {
	s := tempStore(t)

	ctx := parser.Context{
		SessionID:   "sess-001",
		SurfaceID:   "surf-abc",
		WorkspaceID: "ws-001",
		Agent:       parser.AgentClaudeCode,
		CapturedAt:  time.Now(),
		Task:        parser.Task{Goal: "original goal", Status: parser.StatusInProgress},
	}
	s.Upsert(ctx)

	ctx.Task.Goal = "updated goal"
	ctx.Task.Status = parser.StatusComplete
	s.Upsert(ctx)

	got, _ := s.Get("sess-001")
	if got.Task.Goal != "updated goal" {
		t.Errorf("goal not updated: got %q", got.Task.Goal)
	}
	if got.Task.Status != parser.StatusComplete {
		t.Errorf("status not updated: got %s", got.Task.Status)
	}
}

func TestStore_ListAll_OrderedByRecent(t *testing.T) {
	s := tempStore(t)

	for i, id := range []string{"sess-old", "sess-new"} {
		ctx := parser.Context{
			SessionID:   id,
			SurfaceID:   "surf-" + id,
			WorkspaceID: "ws-001",
			Agent:       parser.AgentClaudeCode,
			CapturedAt:  time.Now().Add(time.Duration(i) * time.Second),
			Task:        parser.Task{Goal: id, Status: parser.StatusInProgress},
		}
		s.Upsert(ctx)
		time.Sleep(1100 * time.Millisecond)
	}

	all, err := s.ListAll()
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(all))
	}
	if all[0].SessionID != "sess-new" {
		t.Errorf("expected most recent first, got %s", all[0].SessionID)
	}
}

func TestStore_Snapshots(t *testing.T) {
	s := tempStore(t)

	ctx := parser.Context{
		SessionID:   "sess-001",
		SurfaceID:   "surf-abc",
		WorkspaceID: "ws-001",
		Agent:       parser.AgentCodex,
		CapturedAt:  time.Now(),
		Task:        parser.Task{Goal: "fix auth", Status: parser.StatusBlocked},
	}

	if err := s.SaveSnapshot("checkpoint-1", "sess-001", ctx); err != nil {
		t.Fatalf("save snapshot: %v", err)
	}

	loaded, err := s.LoadSnapshot("checkpoint-1")
	if err != nil {
		t.Fatalf("load snapshot: %v", err)
	}
	if loaded.Task.Goal != "fix auth" {
		t.Errorf("snapshot goal: got %q", loaded.Task.Goal)
	}
	if loaded.Agent != parser.AgentCodex {
		t.Errorf("snapshot agent: got %s", loaded.Agent)
	}
}

func TestStore_LoadSnapshot_NotFound(t *testing.T) {
	s := tempStore(t)
	_, err := s.LoadSnapshot("nonexistent")
	if err == nil {
		t.Error("expected error for missing snapshot")
	}
}

func TestStore_Delete(t *testing.T) {
	s := tempStore(t)

	ctx := parser.Context{
		SessionID:   "sess-del",
		SurfaceID:   "surf-del",
		WorkspaceID: "ws-001",
		Agent:       parser.AgentAider,
		CapturedAt:  time.Now(),
		Task:        parser.Task{Goal: "delete me", Status: parser.StatusIdle},
	}
	s.Upsert(ctx)
	s.Delete("sess-del")

	_, err := s.Get("sess-del")
	if err == nil {
		t.Error("expected error after delete")
	}
}

func TestStore_UpdateSummary(t *testing.T) {
	s := tempStore(t)

	ctx := parser.Context{
		SessionID:   "sess-sum",
		SurfaceID:   "surf-sum",
		WorkspaceID: "ws-001",
		Agent:       parser.AgentClaudeCode,
		CapturedAt:  time.Now(),
		Task:        parser.Task{Goal: "test", Status: parser.StatusInProgress},
	}
	s.Upsert(ctx)

	s.UpdateSummary("sess-sum", "TASK: test\nSTATUS: in_progress")

	got, _ := s.Get("sess-sum")
	if got.Summary != "TASK: test\nSTATUS: in_progress" {
		t.Errorf("summary not updated: got %q", got.Summary)
	}
}

func TestStore_ExpandsHomePath(t *testing.T) {
	home, _ := os.UserHomeDir()
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "sub", "test.db"))
	if err != nil {
		t.Fatalf("open with nested path: %v", err)
	}
	s.Close()
	_ = home
}
