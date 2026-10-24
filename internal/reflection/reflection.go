// Package reflection implements the machine-readable reflection journal
// (card #853): outcomes of goal conflicts (quality vs speed, follow-up
// incidents) stored in PostgreSQL — the same instance that hosts the
// tracker — with Qdrant as a derived, rebuildable index.
//
// Safety rules: the store never touches Vikunja tables — it owns only
// the dedicated schema `viktor` (its migrations ignore it). When the
// database is unreachable the tools degrade to {supported:false}
// instead of failing; the human-readable task comment written alongside
// every log keeps the record recoverable.
package reflection

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"

	"github.com/jackc/pgx/v5/pgconn"
// pgx stdlib registers the "pgx" database/sql driver used by PG.
_ "github.com/jackc/pgx/v5/stdlib" //nolint:revive // driver side-effect import
)

// Record is one reflection entry: how a goal conflict was resolved.
type Record struct {
	ID           int64              `json:"id"`
	TaskID       int64              `json:"task_id"`
	Repo         string             `json:"repo,omitempty"`
	Goals        map[string]float64 `json:"goals,omitempty"`
	GoalConflict string             `json:"goal_conflict"`
	Resolution   string             `json:"resolution"`
	Outcome      string             `json:"outcome,omitempty"`
	Confidence   float64            `json:"confidence,omitempty"`
	CreatedAt    string             `json:"created_at,omitempty"`
}

// Store is the persistence contract; *PG is the real implementation,
// fakes serve the tool tests.
type Store interface {
	Log(ctx context.Context, r Record) (int64, error)
	List(ctx context.Context, taskID int64, limit int) ([]Record, error)
	Since(ctx context.Context, afterID int64, limit int) ([]Record, error)
	Close() error
}

// Config carries the reflection layer settings; an empty PGDSN means
// "not configured" and the tools answer {supported:false}.
type Config struct {
	PGDSN string
	// Repo stamps records with the repo they belong to (card #843
	// convention); optional.
	Repo string
}

// ErrUnavailable reports the database is down; tools map it to
// {supported:false} — the same degradation contract as memory
// (ErrMemoryUnavailable) and comment_recent.
type ErrUnavailable struct{ Cause string }

func (e *ErrUnavailable) Error() string { return "reflection unavailable: " + e.Cause }

// safeCause renders err defensively: some pgx error types panic on
// Error() when partially constructed; production errors are always
// complete, tests are not.
func safeCause(err error) (s string) {
	defer func() {
		if recover() != nil {
			s = "unrenderable connection error"
		}
	}()
	return err.Error()
}

// ClassifyUnavailable maps transport-level Postgres failures to the
// degradation signal; application-level errors (bad input, constraint
// violations) pass through unchanged.
func ClassifyUnavailable(err error) error {
	if err == nil {
		return nil
	}
	var ce *pgconn.ConnectError
	if errors.As(err, &ce) {
		return &ErrUnavailable{Cause: safeCause(err)}
	}
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		switch pe.Code {
		case "57P01", "57P02", "57P03", // admin shutdown, crash shutdown, cannot connect now
			"08000", "08001", "08003", "08006": // connection_exception family
			return &ErrUnavailable{Cause: err.Error()}
		}
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return &ErrUnavailable{Cause: err.Error()}
	}
	if errors.Is(err, sql.ErrConnDone) || errors.Is(err, context.DeadlineExceeded) {
		return &ErrUnavailable{Cause: err.Error()}
	}
	return err
}

// PG is the canonical store: a lazily-connected database/sql pool over
// the pgx stdlib driver, plus the one-shot schema migration.
type PG struct {
	db     *sql.DB
	repo   string
	once   sync.Once
	schema error
}

// NewPG builds the store. sql.Open is lazy — a bad DSN or a down
// database surfaces on first use as a classifiable error.
func NewPG(cfg Config) (*PG, error) {
	if cfg.PGDSN == "" {
		return nil, errors.New("reflection: pg_dsn is required")
	}
	db, err := sql.Open("pgx", cfg.PGDSN)
	if err != nil {
		return nil, fmt.Errorf("reflection: open: %w", err)
	}
	return &PG{db: db, repo: cfg.Repo}, nil
}

const schemaSQL = `
CREATE SCHEMA IF NOT EXISTS viktor;
CREATE TABLE IF NOT EXISTS viktor.reflections (
	id           bigserial PRIMARY KEY,
	task_id      bigint      NOT NULL,
	repo         text        NOT NULL DEFAULT '',
	goals        jsonb       NOT NULL DEFAULT '{}',
	goal_conflict text       NOT NULL DEFAULT '',
	resolution   text        NOT NULL DEFAULT '',
	outcome      text        NOT NULL DEFAULT '',
	confidence   real        NOT NULL DEFAULT 0,
	created_at   timestamptz NOT NULL DEFAULT now()
);`

// ensureSchema runs the auto-migration once per store instance; Vikunja
// tables are never touched (the schema `viktor` is ours alone).
func (p *PG) ensureSchema(ctx context.Context) error {
	p.once.Do(func() {
		if _, err := p.db.ExecContext(ctx, schemaSQL); err != nil {
			p.schema = fmt.Errorf("reflection: migrate: %w", err)
		}
	})
	return p.schema
}

func (p *PG) Log(ctx context.Context, r Record) (int64, error) {
	if err := p.ensureSchema(ctx); err != nil {
		return 0, ClassifyUnavailable(err)
	}
	if r.Confidence < 0 {
		r.Confidence = 0
	}
	if r.Confidence > 1 {
		r.Confidence = 1
	}
	if r.Repo == "" {
		r.Repo = p.repo
	}
	goals, err := json.Marshal(r.Goals)
	if err != nil {
		return 0, fmt.Errorf("reflection: goals: %w", err)
	}
	var id int64
	err = p.db.QueryRowContext(ctx, `
		INSERT INTO viktor.reflections (task_id, repo, goals, goal_conflict, resolution, outcome, confidence)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id`,
		r.TaskID, r.Repo, goals, r.GoalConflict, r.Resolution, r.Outcome, r.Confidence).Scan(&id)
	if err != nil {
		return 0, ClassifyUnavailable(fmt.Errorf("reflection: insert: %w", err))
	}
	return id, nil
}

func (p *PG) List(ctx context.Context, taskID int64, limit int) ([]Record, error) {
	if err := p.ensureSchema(ctx); err != nil {
		return nil, ClassifyUnavailable(err)
	}
	rows, err := p.query(ctx, "task_id = $1 ORDER BY id DESC LIMIT $2", args(taskID, limit))
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func args(taskID int64, limit int) []any {
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	return []any{taskID, limit}
}

func (p *PG) query(ctx context.Context, where string, args []any) ([]Record, error) {
	// `where` is built internally from a fixed set of clauses and numeric
	// limits (no external input).
	q := "SELECT id, task_id, repo, goals, goal_conflict, resolution, outcome, confidence, created_at FROM viktor.reflections WHERE " + where // #nosec G202 -- fixed internal clauses
	rows, err := p.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, ClassifyUnavailable(fmt.Errorf("reflection: select: %w", err))
	}
	defer func() { _ = rows.Close() }()
	var out []Record
	for rows.Next() {
		var r Record
		var goals []byte
		if err := rows.Scan(&r.ID, &r.TaskID, &r.Repo, &goals, &r.GoalConflict, &r.Resolution, &r.Outcome, &r.Confidence, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("reflection: scan: %w", err)
		}
		if len(goals) > 0 {
			_ = json.Unmarshal(goals, &r.Goals) // jsonb is always valid JSON
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, ClassifyUnavailable(fmt.Errorf("reflection: rows: %w", err))
	}
	return out, nil
}

// Since reads records with id > afterID in ascending order — the
// indexer feed (card #853: cursor by id, in the spirit of sync.json).
func (p *PG) Since(ctx context.Context, afterID int64, limit int) ([]Record, error) {
	if err := p.ensureSchema(ctx); err != nil {
		return nil, ClassifyUnavailable(err)
	}
	if limit <= 0 {
		limit = 200
	}
	if limit > 1000 {
		limit = 1000
	}
	return p.query(ctx, fmt.Sprintf("id > %d ORDER BY id ASC LIMIT %d", afterID, limit), nil)
}

func (p *PG) Close() error { return p.db.Close() }
