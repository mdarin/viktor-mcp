package fsmstate

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	// Pure-Go SQLite driver, registered as "sqlite" (ADR 0004).
	_ "modernc.org/sqlite" //nolint:revive // driver side-effect registration
)

// Store is the persistent FSM store: one SQLite file per instance
// (<XDG_STATE_HOME>/viktor/fsm/<instance>.db, ADR 0004).
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the store at path.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, fmt.Errorf("fsmstate: open %s: %w", path, err)
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the store.
func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate() error {
	const schema = `
CREATE TABLE IF NOT EXISTS claims (
    instance   TEXT NOT NULL,
    task_id    INTEGER NOT NULL,
    state      TEXT NOT NULL,
    request_id TEXT NOT NULL,
    actor      TEXT NOT NULL DEFAULT '',
    claimed_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (instance, task_id)
);
CREATE TABLE IF NOT EXISTS idempotency (
    instance    TEXT NOT NULL,
    request_id  TEXT NOT NULL,
    tool        TEXT NOT NULL,
    result_json TEXT,
    created_at  TEXT NOT NULL,
    PRIMARY KEY (instance, request_id)
);
CREATE TABLE IF NOT EXISTS transitions (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    instance   TEXT NOT NULL,
    task_id    INTEGER NOT NULL,
    from_state TEXT NOT NULL,
    to_state   TEXT NOT NULL,
    request_id TEXT NOT NULL DEFAULT '',
    actor      TEXT NOT NULL DEFAULT '',
    detail     TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);`
	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("fsmstate: migrate: %w", err)
	}
	return nil
}

// Claim is the current claim of a task.
type Claim struct {
	TaskID    int    `json:"task_id"`
	State     State  `json:"state"`
	RequestID string `json:"request_id"`
	Actor     string `json:"actor,omitempty"`
	ClaimedAt string `json:"claimed_at"`
	UpdatedAt string `json:"updated_at"`
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }

// GetClaim returns the current claim of a task, ok=false when the task is
// not tracked by the FSM.
func (s *Store) GetClaim(ctx context.Context, instance string, taskID int) (Claim, bool, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT task_id, state, request_id, actor, claimed_at, updated_at FROM claims WHERE instance = ? AND task_id = ?`,
		instance, taskID)
	var c Claim
	err := row.Scan(&c.TaskID, &c.State, &c.RequestID, &c.Actor, &c.ClaimedAt, &c.UpdatedAt)
	if err == sql.ErrNoRows {
		return Claim{}, false, nil
	}
	if err != nil {
		return Claim{}, false, fmt.Errorf("fsmstate: get claim: %w", err)
	}
	return c, true, nil
}

// Claim atomically claims a task for in_progress work. The (instance,
// task_id) primary key makes a concurrent second claim fail; a repeat with
// the same request_id returns the stored result instead (idempotency).
func (s *Store) Claim(ctx context.Context, instance string, taskID int, requestID, actor string) (Claim, bool, error) {
	if res, ok, err := s.LookupIdempotent(ctx, instance, requestID); err != nil || ok {
		var c Claim
		if ok && res != nil {
			_ = json.Unmarshal(res, &c)
		}
		return c, ok, err
	}
	ts := now()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO claims (instance, task_id, state, request_id, actor, claimed_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		instance, taskID, string(StateInProgress), requestID, actor, ts, ts)
	if err != nil {
		return Claim{}, false, fmt.Errorf("fsmstate: claim task %d: %w (already claimed?)", taskID, err)
	}
	c := Claim{TaskID: taskID, State: StateInProgress, RequestID: requestID, Actor: actor, ClaimedAt: ts, UpdatedAt: ts}
	if err := s.record(ctx, instance, taskID, "", StateInProgress, requestID, actor, "claim"); err != nil {
		return Claim{}, false, err
	}
	if err := s.StoreIdempotent(ctx, instance, requestID, "claim_task", c); err != nil {
		return Claim{}, false, err
	}
	return c, false, nil
}

// Transition moves a tracked task from → to after validating the matrix.
// When the task is not tracked the transition is allowed only from an
// explicit expected-from state, enforced by the caller.
func (s *Store) Transition(ctx context.Context, instance string, taskID int, to State, requestID, actor, detail string) (Claim, error) {
	cur, tracked, err := s.GetClaim(ctx, instance, taskID)
	if err != nil {
		return Claim{}, err
	}
	from := StateBacklog
	if tracked {
		from = cur.State
	}
	if from == to {
		return cur, nil
	}
	if !CanTransition(from, to) {
		return Claim{}, fmt.Errorf("fsmstate: transition %s → %s for task %d is not allowed (allowed from %s: %v)", from, to, taskID, from, AllowedFrom(from))
	}
	ts := now()
	if tracked {
		if _, err := s.db.ExecContext(ctx,
			`UPDATE claims SET state = ?, request_id = ?, updated_at = ? WHERE instance = ? AND task_id = ?`,
			string(to), requestID, ts, instance, taskID); err != nil {
			return Claim{}, fmt.Errorf("fsmstate: update claim: %w", err)
		}
	} else {
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO claims (instance, task_id, state, request_id, actor, claimed_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			instance, taskID, string(to), requestID, actor, ts, ts); err != nil {
			return Claim{}, fmt.Errorf("fsmstate: insert claim: %w", err)
		}
	}
	if err := s.record(ctx, instance, taskID, from, to, requestID, actor, detail); err != nil {
		return Claim{}, err
	}
	return s.mustClaim(ctx, instance, taskID)
}

func (s *Store) mustClaim(ctx context.Context, instance string, taskID int) (Claim, error) {
	c, ok, err := s.GetClaim(ctx, instance, taskID)
	if err != nil {
		return Claim{}, err
	}
	if !ok {
		return Claim{}, fmt.Errorf("fsmstate: claim for task %d vanished", taskID)
	}
	return c, nil
}

func (s *Store) record(ctx context.Context, instance string, taskID int, from, to State, requestID, actor, detail string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO transitions (instance, task_id, from_state, to_state, request_id, actor, detail, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		instance, taskID, string(from), string(to), requestID, actor, detail, now())
	if err != nil {
		return fmt.Errorf("fsmstate: record transition: %w", err)
	}
	return nil
}

// LookupIdempotent returns the stored result for request_id, ok=false on
// first sight.
func (s *Store) LookupIdempotent(ctx context.Context, instance, requestID string) (json.RawMessage, bool, error) {
	if requestID == "" {
		return nil, false, nil
	}
	var raw sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT result_json FROM idempotency WHERE instance = ? AND request_id = ?`,
		instance, requestID).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("fsmstate: idempotency lookup: %w", err)
	}
	if !raw.Valid {
		return nil, true, nil
	}
	return json.RawMessage(raw.String), true, nil
}

// StoreIdempotent persists the result of a request_id. The INSERT is a
// no-op when the request_id is already known: the first result wins.
func (s *Store) StoreIdempotent(ctx context.Context, instance, requestID, tool string, result any) error {
	if requestID == "" {
		return nil
	}
	data, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("fsmstate: marshal result: %w", err)
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO idempotency (instance, request_id, tool, result_json, created_at) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT (instance, request_id) DO NOTHING`,
		instance, requestID, tool, string(data), now())
	if err != nil {
		return fmt.Errorf("fsmstate: idempotency store: %w", err)
	}
	return nil
}

// History lists the transition audit trail of a task, newest last.
func (s *Store) History(ctx context.Context, instance string, taskID int) ([]map[string]any, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT from_state, to_state, request_id, actor, detail, created_at FROM transitions WHERE instance = ? AND task_id = ? ORDER BY id`,
		instance, taskID)
	if err != nil {
		return nil, fmt.Errorf("fsmstate: history: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []map[string]any
	for rows.Next() {
		var from, to, req, actor, detail, created string
		if err := rows.Scan(&from, &to, &req, &actor, &detail, &created); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"from": from, "to": to, "request_id": req,
			"actor": actor, "detail": detail, "at": created,
		})
	}
	return out, rows.Err()
}
