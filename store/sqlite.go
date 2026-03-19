// Package store persists Universal Context Format (UCF) snapshots in SQLite.
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/context-bridge/bridge/parser"
)

// Store persists and retrieves context snapshots.
type Store struct {
	db *sql.DB
}

// Open opens (or creates) the SQLite database at dbPath.
func Open(dbPath string) (*Store, error) {
	if strings.HasPrefix(dbPath, "~/") {
		home, _ := os.UserHomeDir()
		dbPath = filepath.Join(home, dbPath[2:])
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
	CREATE TABLE IF NOT EXISTS sessions (
		session_id    TEXT PRIMARY KEY,
		surface_id    TEXT NOT NULL,
		workspace_id  TEXT NOT NULL,
		agent         TEXT NOT NULL,
		workspace     TEXT,
		cwd           TEXT,
		git_branch    TEXT,
		captured_at   DATETIME NOT NULL,
		task_goal     TEXT,
		task_status   TEXT,
		task_blocker  TEXT,
		file_changes  TEXT,
		key_decisions TEXT,
		errors        TEXT,
		excerpt       TEXT,
		summary       TEXT,
		raw_snapshot  TEXT,
		updated_at    DATETIME NOT NULL
	);

	CREATE TABLE IF NOT EXISTS named_snapshots (
		name        TEXT PRIMARY KEY,
		session_id  TEXT NOT NULL,
		context_json TEXT NOT NULL,
		created_at  DATETIME NOT NULL
	);
	`)
	return err
}

// Upsert saves or updates a context snapshot.
func (s *Store) Upsert(ctx parser.Context) error {
	fileChangesJSON, _ := json.Marshal(ctx.FileChanges)
	decisionsJSON, _ := json.Marshal(ctx.KeyDecisions)
	errorsJSON, _ := json.Marshal(ctx.ErrorsEncountered)

	_, err := s.db.Exec(`
	INSERT INTO sessions
		(session_id, surface_id, workspace_id, agent, workspace, cwd, git_branch,
		 captured_at, task_goal, task_status, task_blocker,
		 file_changes, key_decisions, errors, excerpt, summary, raw_snapshot, updated_at)
	VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
	ON CONFLICT(session_id) DO UPDATE SET
		surface_id   = excluded.surface_id,
		agent        = excluded.agent,
		workspace    = excluded.workspace,
		cwd          = excluded.cwd,
		git_branch   = excluded.git_branch,
		captured_at  = excluded.captured_at,
		task_goal    = excluded.task_goal,
		task_status  = excluded.task_status,
		task_blocker = excluded.task_blocker,
		file_changes = excluded.file_changes,
		key_decisions= excluded.key_decisions,
		errors       = excluded.errors,
		excerpt      = excluded.excerpt,
		summary      = excluded.summary,
		raw_snapshot = excluded.raw_snapshot,
		updated_at   = excluded.updated_at
	`,
		ctx.SessionID, ctx.SurfaceID, ctx.WorkspaceID,
		string(ctx.Agent), ctx.Workspace, ctx.CWD, ctx.GitBranch,
		ctx.CapturedAt.UTC().Format(time.RFC3339),
		ctx.Task.Goal, string(ctx.Task.Status), ctx.Task.CurrentBlocker,
		string(fileChangesJSON), string(decisionsJSON), string(errorsJSON),
		ctx.ConversationExcerpt, ctx.Summary, ctx.RawSnapshot,
		time.Now().UTC().Format(time.RFC3339),
	)
	return err
}

// Get retrieves a context snapshot by session ID.
func (s *Store) Get(sessionID string) (*parser.Context, error) {
	row := s.db.QueryRow(`SELECT * FROM sessions WHERE session_id = ?`, sessionID)
	return scanContext(row)
}

// GetBySurface retrieves the latest snapshot for a surface.
func (s *Store) GetBySurface(surfaceID string) (*parser.Context, error) {
	row := s.db.QueryRow(
		`SELECT * FROM sessions WHERE surface_id = ? ORDER BY updated_at DESC LIMIT 1`,
		surfaceID,
	)
	return scanContext(row)
}

// ListAll returns all active sessions ordered by most recently updated.
func (s *Store) ListAll() ([]parser.Context, error) {
	rows, err := s.db.Query(`SELECT * FROM sessions ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []parser.Context
	for rows.Next() {
		ctx, err := scanContext(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *ctx)
	}
	return out, rows.Err()
}

// UpdateSummary sets the LLM-generated summary for a session.
func (s *Store) UpdateSummary(sessionID, summary string) error {
	_, err := s.db.Exec(
		`UPDATE sessions SET summary = ?, updated_at = ? WHERE session_id = ?`,
		summary, time.Now().UTC().Format(time.RFC3339), sessionID,
	)
	return err
}

// SaveSnapshot saves a named snapshot for later restoration.
func (s *Store) SaveSnapshot(name, sessionID string, ctx parser.Context) error {
	data, err := json.Marshal(ctx)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`
	INSERT INTO named_snapshots (name, session_id, context_json, created_at)
	VALUES (?,?,?,?)
	ON CONFLICT(name) DO UPDATE SET
		session_id   = excluded.session_id,
		context_json = excluded.context_json,
		created_at   = excluded.created_at
	`, name, sessionID, string(data), time.Now().UTC().Format(time.RFC3339))
	return err
}

// LoadSnapshot retrieves a named snapshot.
func (s *Store) LoadSnapshot(name string) (*parser.Context, error) {
	var raw string
	err := s.db.QueryRow(
		`SELECT context_json FROM named_snapshots WHERE name = ?`, name,
	).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("snapshot %q not found", name)
	}
	if err != nil {
		return nil, err
	}
	var ctx parser.Context
	if err := json.Unmarshal([]byte(raw), &ctx); err != nil {
		return nil, err
	}
	return &ctx, nil
}

// Delete removes a session record.
func (s *Store) Delete(sessionID string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE session_id = ?`, sessionID)
	return err
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

type scanner interface {
	Scan(dest ...any) error
}

func scanContext(row scanner) (*parser.Context, error) {
	var (
		ctx                                        parser.Context
		capturedAt, updatedAt                      string
		agentStr                                   string
		fileChangesJSON, decisionsJSON, errorsJSON string
		taskGoal, taskStatus, taskBlocker          string
	)
	err := row.Scan(
		&ctx.SessionID, &ctx.SurfaceID, &ctx.WorkspaceID,
		&agentStr, &ctx.Workspace, &ctx.CWD, &ctx.GitBranch,
		&capturedAt, &taskGoal, &taskStatus, &taskBlocker,
		&fileChangesJSON, &decisionsJSON, &errorsJSON,
		&ctx.ConversationExcerpt, &ctx.Summary, &ctx.RawSnapshot,
		&updatedAt,
	)
	if err != nil {
		return nil, err
	}
	ctx.Agent = parser.AgentType(agentStr)
	ctx.Task.Goal = taskGoal
	ctx.Task.Status = parser.TaskStatus(taskStatus)
	ctx.Task.CurrentBlocker = taskBlocker
	ctx.CapturedAt, _ = time.Parse(time.RFC3339, capturedAt)

	_ = json.Unmarshal([]byte(fileChangesJSON), &ctx.FileChanges)
	_ = json.Unmarshal([]byte(decisionsJSON), &ctx.KeyDecisions)
	_ = json.Unmarshal([]byte(errorsJSON), &ctx.ErrorsEncountered)

	return &ctx, nil
}
