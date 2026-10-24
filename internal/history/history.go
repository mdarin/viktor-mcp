// Package history implements the BM25 lexical search index over tracker
// history (card #867, Phase 2 of the "Очереди 2" concept, ADR 0004):
// task titles/descriptions and comments stored in the ParadeDB
// `search_history` table (deploy/paradedb-init/01_search_history.sql).
//
// Division of labour with the semantic memory (ADR 0003): semantic
// search answers "what is this about", lexical search answers "where is
// this exact error code / identifier / task id". The corpus is the same
// (tasks + comments), the retrieval is BM25 instead of embeddings.
//
// Safety rules (same as reflection, card #853): the store never touches
// Vikunja tables — it owns only the `search_history` table; when the
// database is unreachable the tool degrades to {supported:false}
// instead of failing. If the pg_search extension is missing or its
// function set drifted, Search falls back to ILIKE and reports mode
// "ilike" — the index stays usable without the extension.
package history

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib" //nolint:revive // driver side-effect import
)

// Kinds stored in the `kind` column. Free-form by design, but the sync
// CLI (cmd/viktor history) writes exactly these two.
const (
	KindTask    = "task"
	KindComment = "comment"
)

// Doc is one indexable unit: the whole task (kind "task", seq 0) or a
// single comment (kind "comment", seq = comment id). One task's docs go
// into the index atomically via ReplaceTask.
type Doc struct {
	TaskID    int64
	ProjectID int64
	Instance  string
	Kind      string
	Seq       int64
	Content   string
}

// Hit is one search result.
type Hit struct {
	TaskID    int64   `json:"task_id"`
	ProjectID int64   `json:"project_id,omitempty"`
	Instance  string  `json:"instance,omitempty"`
	Kind      string  `json:"kind"`
	Content   string  `json:"content"`
	Score     float64 `json:"score,omitempty"`
	CreatedAt string  `json:"created_at,omitempty"`
}

// Query is a search request; all filters are optional.
type Query struct {
	Text      string
	Instance  string
	ProjectID int64
	TaskID    int64
	Kind      string
	Limit     int
}

// Store is the persistence contract; *PG is the real implementation,
// fakes serve the tool tests.
type Store interface {
	// ReplaceTask atomically rewrites all docs of one task: the
	// previous rows are deleted, the new ones inserted (one
	// transaction). Re-running the sync over unchanged data therefore
	// never duplicates rows.
	ReplaceTask(ctx context.Context, docs []Doc) error
	// DeleteNotIn removes every doc of the instance whose task is not
	// in taskIDs — ghost cleanup after a complete scan (card #821
	// semantics). Returns the number of rows removed.
	DeleteNotIn(ctx context.Context, instance string, taskIDs []int64) (int64, error)
	// Search returns up to Limit hits plus the retrieval mode
	// ("bm25" or "ilike").
	Search(ctx context.Context, q Query) (hits []Hit, mode string, err error)
	// Count returns the number of indexed docs (optionally filtered by
	// instance when instance != "").
	Count(ctx context.Context, instance string) (int64, error)
	Close() error
}

// Config carries the history layer settings; an empty PGDSN means "not
// configured" and the tool answers {supported:false}.
type Config struct {
	PGDSN string
}

// ErrUnavailable reports the database is down; the tool maps it to
// {supported:false} — the same degradation contract as memory and
// reflection (card #853).
type ErrUnavailable struct{ Cause string }

func (e *ErrUnavailable) Error() string { return "history unavailable: " + e.Cause }

// safeCause renders err defensively: some pgx error types panic on
// Error() when partially constructed (card #853 lesson).
func safeCause(err error) (s string) {
	defer func() {
		if recover() != nil {
			s = "unrenderable connection error"
		}
	}()
	return err.Error()
}

// ClassifyUnavailable maps transport-level Postgres failures to the
// degradation signal; application-level errors pass through unchanged.
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
// the pgx stdlib driver, plus the one-shot schema migration and the
// one-shot pg_search capability probe.
type PG struct {
	db     *sql.DB
	once   sync.Once
	schema error
	// bm25 reports whether the pg_search extension answered the probe;
	// false → Search uses the ILIKE fallback.
	bm25 bool
}

// NewPG builds the store. sql.Open is lazy — a bad DSN or a down
// database surfaces on first use as a classifiable error.
func NewPG(cfg Config) (*PG, error) {
	if cfg.PGDSN == "" {
		return nil, errors.New("history: pg_dsn is required")
	}
	db, err := sql.Open("pgx", cfg.PGDSN)
	if err != nil {
		return nil, fmt.Errorf("history: open: %w", err)
	}
	return &PG{db: db}, nil
}

// schemaSQL is idempotent and superset of the init-script table
// (deploy/paradedb-init/01_search_history.sql, card #52): it adds the
// instance/history_key columns the deployment script predates.
const schemaSQL = `
CREATE TABLE IF NOT EXISTS search_history (
	id          bigserial PRIMARY KEY,
	task_id     bigint      NOT NULL,
	project_id  bigint      NOT NULL DEFAULT 0,
	instance    text        NOT NULL DEFAULT '',
	kind        text        NOT NULL DEFAULT 'note',
	content     text        NOT NULL,
	history_key text        NOT NULL DEFAULT '',
	created_at  timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE search_history ADD COLUMN IF NOT EXISTS project_id bigint NOT NULL DEFAULT 0;
ALTER TABLE search_history ADD COLUMN IF NOT EXISTS instance text NOT NULL DEFAULT '';
ALTER TABLE search_history ADD COLUMN IF NOT EXISTS history_key text NOT NULL DEFAULT '';`

// bm25SQL builds the ParadeDB index; failure is non-fatal (the probe
// below decides the retrieval mode).
const bm25SQL = `CREATE INDEX IF NOT EXISTS search_history_bm25 ON search_history USING bm25 (id, content);`

// probeSQL is the capability check: it only succeeds when the pg_search
// extension is installed and paradedb.match/paradedb.score exist with
// the expected signatures. On an empty table it returns no rows.
const probeSQL = `SELECT paradedb.score(id) FROM search_history WHERE content @@@ paradedb.match('content', 'probe') LIMIT 1;`

// ensureSchema runs the auto-migration and the capability probe once
// per store instance; Vikunja tables are never touched.
func (p *PG) ensureSchema(ctx context.Context) error {
	p.once.Do(func() {
		if _, err := p.db.ExecContext(ctx, schemaSQL); err != nil {
			p.schema = fmt.Errorf("history: migrate: %w", err)
			return
		}
		if _, err := p.db.ExecContext(ctx, bm25SQL); err == nil {
			var probe any
			if err := p.db.QueryRowContext(ctx, probeSQL).Scan(&probe); err == nil || errors.Is(err, sql.ErrNoRows) {
				p.bm25 = true
			}
		}
	})
	return p.schema
}

// historyKey builds the stable dedup key of a doc:
// {instance}:{task_id}:{kind}:{seq}.
func historyKey(d Doc) string {
	return fmt.Sprintf("%s:%d:%s:%d", d.Instance, d.TaskID, d.Kind, d.Seq)
}

// ReplaceTask implements Store.
func (p *PG) ReplaceTask(ctx context.Context, docs []Doc) error {
	if err := p.ensureSchema(ctx); err != nil {
		return ClassifyUnavailable(err)
	}
	if len(docs) == 0 {
		return errors.New("history: replace: no docs")
	}
	d0 := docs[0]
	if d0.Instance == "" || d0.TaskID <= 0 {
		return errors.New("history: doc needs instance and task_id")
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return ClassifyUnavailable(fmt.Errorf("history: begin: %w", err))
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM search_history WHERE instance = $1 AND task_id = $2`,
		d0.Instance, d0.TaskID); err != nil {
		return ClassifyUnavailable(fmt.Errorf("history: delete: %w", err))
	}
	for _, d := range docs {
		if strings.TrimSpace(d.Content) == "" {
			continue
		}
		if d.Instance == "" || d.TaskID <= 0 {
			return errors.New("history: doc needs instance and task_id")
		}
		if d.Kind == "" {
			d.Kind = KindTask
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO search_history (task_id, project_id, instance, kind, content, history_key)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			d.TaskID, d.ProjectID, d.Instance, d.Kind, d.Content, historyKey(d)); err != nil {
			return ClassifyUnavailable(fmt.Errorf("history: insert: %w", err))
		}
	}
	if err := tx.Commit(); err != nil {
		return ClassifyUnavailable(fmt.Errorf("history: commit: %w", err))
	}
	return nil
}

// DeleteNotIn implements Store.
func (p *PG) DeleteNotIn(ctx context.Context, instance string, taskIDs []int64) (int64, error) {
	if err := p.ensureSchema(ctx); err != nil {
		return 0, ClassifyUnavailable(err)
	}
	if instance == "" {
		return 0, errors.New("history: delete not-in: instance is required")
	}
	ids := make([]int64, 0, len(taskIDs))
	for _, id := range taskIDs {
		if id > 0 {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		// Nothing live: drop everything of the instance.
		res, err := p.db.ExecContext(ctx, `DELETE FROM search_history WHERE instance = $1`, instance)
		if err != nil {
			return 0, ClassifyUnavailable(fmt.Errorf("history: delete not-in: %w", err))
		}
		return res.RowsAffected()
	}
	res, err := p.db.ExecContext(ctx,
		`DELETE FROM search_history WHERE instance = $1 AND NOT (task_id = ANY($2::bigint[]))`,
		instance, ids)
	if err != nil {
		return 0, ClassifyUnavailable(fmt.Errorf("history: delete not-in: %w", err))
	}
	return res.RowsAffected()
}

// filterWhere appends the non-empty Query filters; it returns the
// clauses and the grown args so placeholders stay correctly numbered.
func filterWhere(q Query, args []any) ([]string, []any) {
	var where []string
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, fmt.Sprintf(cond, len(args)))
	}
	if q.Instance != "" {
		add("instance = $%d", q.Instance)
	}
	if q.ProjectID > 0 {
		add("project_id = $%d", q.ProjectID)
	}
	if q.TaskID > 0 {
		add("task_id = $%d", q.TaskID)
	}
	if q.Kind != "" {
		add("kind = $%d", q.Kind)
	}
	return where, args
}

// Search implements Store: BM25 via pg_search when the probe passed,
// ILIKE fallback otherwise (also when the BM25 query fails with an
// undefined-function/undefined-object error — extension version drift).
func (p *PG) Search(ctx context.Context, q Query) ([]Hit, string, error) {
	if err := p.ensureSchema(ctx); err != nil {
		return nil, "", ClassifyUnavailable(err)
	}
	text := strings.TrimSpace(q.Text)
	if text == "" {
		return nil, "", errors.New("history: query text is required")
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	if p.bm25 {
		hits, err := p.searchBM25(ctx, text, q, limit)
		if err == nil {
			return hits, "bm25", nil
		}
		var pe *pgconn.PgError
		if !(errors.As(err, &pe) && (pe.Code == "42883" || pe.Code == "42704")) { // undefined_function / undefined_object
			return nil, "", ClassifyUnavailable(fmt.Errorf("history: bm25 search: %w", err))
		}
	}
	hits, err := p.searchLike(ctx, text, q, limit)
	if err != nil {
		return nil, "", err
	}
	return hits, "ilike", nil
}

func (p *PG) searchBM25(ctx context.Context, text string, q Query, limit int) ([]Hit, error) {
	args := []any{text}
	where := []string{"content @@@ paradedb.match('content', $1)"}
	w, args := filterWhere(q, args)
	where = append(where, w...)
	args = append(args, limit)
	query := "SELECT task_id, project_id, instance, kind, content, created_at, paradedb.score(id) AS score" +
		" FROM search_history WHERE " + strings.Join(where, " AND ") +
		fmt.Sprintf(" ORDER BY score DESC LIMIT $%d", len(args)) // #nosec G202 -- fixed internal clauses
	return p.scan(ctx, query, args)
}

func (p *PG) searchLike(ctx context.Context, text string, q Query, limit int) ([]Hit, error) {
	pattern := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(text) + "%"
	args := []any{pattern}
	where := []string{"content ILIKE '%' || $1 || '%'"}
	w, args := filterWhere(q, args)
	where = append(where, w...)
	args = append(args, limit)
	query := "SELECT task_id, project_id, instance, kind, content, created_at, 0::real AS score" +
		" FROM search_history WHERE " + strings.Join(where, " AND ") +
		fmt.Sprintf(" ORDER BY id DESC LIMIT $%d", len(args)) // #nosec G202 -- fixed internal clauses
	return p.scan(ctx, query, args)
}

func (p *PG) scan(ctx context.Context, query string, args []any) ([]Hit, error) {
	rows, err := p.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, ClassifyUnavailable(fmt.Errorf("history: select: %w", err))
	}
	defer func() { _ = rows.Close() }()
	var out []Hit
	for rows.Next() {
		var h Hit
		var score sql.NullFloat64
		if err := rows.Scan(&h.TaskID, &h.ProjectID, &h.Instance, &h.Kind, &h.Content, &h.CreatedAt, &score); err != nil {
			return nil, fmt.Errorf("history: scan: %w", err)
		}
		h.Score = score.Float64
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, ClassifyUnavailable(fmt.Errorf("history: rows: %w", err))
	}
	return out, nil
}

// Count implements Store.
func (p *PG) Count(ctx context.Context, instance string) (int64, error) {
	if err := p.ensureSchema(ctx); err != nil {
		return 0, ClassifyUnavailable(err)
	}
	var n int64
	var err error
	if instance == "" {
		err = p.db.QueryRowContext(ctx, `SELECT count(*) FROM search_history`).Scan(&n)
	} else {
		err = p.db.QueryRowContext(ctx, `SELECT count(*) FROM search_history WHERE instance = $1`, instance).Scan(&n)
	}
	if err != nil {
		return 0, ClassifyUnavailable(fmt.Errorf("history: count: %w", err))
	}
	return n, nil
}

func (p *PG) Close() error { return p.db.Close() }
