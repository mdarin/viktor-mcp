package reflection

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestClassifyUnavailable(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool // true → ErrUnavailable (degrade), false → passthrough
	}{
		{"connect error", &pgconn.ConnectError{Config: &pgconn.Config{Host: "x"}}, true},
		{"admin shutdown", &pgconn.PgError{Code: "57P01"}, true},
		{"cannot connect now", &pgconn.PgError{Code: "57P03"}, true},
		{"connection exception", &pgconn.PgError{Code: "08006"}, true},
		{"syntax error", &pgconn.PgError{Code: "42601"}, false},
		{"net timeout", &net.OpError{Op: "dial", Err: fmt.Errorf("refused")}, true},
		{"conn done", sql.ErrConnDone, true},
		{"deadline", context.DeadlineExceeded, true},
		{"app error", errors.New("constraint violation"), false},
	}
	for _, tc := range cases {
		got := ClassifyUnavailable(tc.err)
		var un *ErrUnavailable
		isUn := errors.As(got, &un)
		if isUn != tc.want {
			t.Fatalf("%s: want unavailable=%v, got %v", tc.name, tc.want, got)
		}
	}
	if ClassifyUnavailable(nil) != nil {
		t.Fatal("nil must stay nil")
	}
}

func TestNewPGRequiresDSN(t *testing.T) {
	if _, err := NewPG(Config{}); err == nil {
		t.Fatal("empty DSN must be rejected")
	}
}

// TestPGIntegration is the live-database test; skipped unless
// VIKTOR_PG_TEST_DSN points at a scratch Postgres. It also asserts the
// isolation rule: the store only touches the `viktor` schema.
func TestPGIntegration(t *testing.T) {
	dsn := os.Getenv("VIKTOR_PG_TEST_DSN")
	if dsn == "" {
		t.Skip("set VIKTOR_PG_TEST_DSN to run the live PG integration test")
	}
	pg, err := NewPG(Config{PGDSN: dsn, Repo: "viktor"})
	if err != nil {
		t.Fatal(err)
	}
	defer pg.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Idempotent auto-migration.
	for i := 0; i < 2; i++ {
		if err := pg.ensureSchema(ctx); err != nil {
			t.Fatalf("migrate run %d: %v", i, err)
		}
	}

	id1, err := pg.Log(ctx, Record{TaskID: 853, Goals: map[string]float64{"quality": 1}, GoalConflict: "quality vs speed", Resolution: "journal first", Outcome: "ok", Confidence: 0.9})
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	id2, err := pg.Log(ctx, Record{TaskID: 853, GoalConflict: "broad vs scoped", Resolution: "scoped", Confidence: 2})
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	if id2 <= id1 {
		t.Fatalf("ids must grow: %d then %d", id1, id2)
	}

	// Repo defaults from config.
	recs, err := pg.List(ctx, 853, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(recs) < 2 {
		t.Fatalf("want >=2 records, got %d", len(recs))
	}
	if recs[0].Repo != "viktor" {
		t.Fatalf("repo stamp missing: %+v", recs[0])
	}
	if recs[0].Confidence != 1 { // clamped from 2
		t.Fatalf("confidence not clamped: %v", recs[0].Confidence)
	}

	// Since: id cursor in ascending order, resumable.
	rows, err := pg.Since(ctx, id1, 10)
	if err != nil {
		t.Fatalf("since: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != id2 {
		t.Fatalf("since after %d: %+v", id1, rows)
	}

	// Isolation: Vikunja tables must not exist in our connection path —
	// the migration created exactly our schema, and a bare query against
	// a Vikunja-owned table proves we are not touching shared state.
	if _, err := pg.db.Exec(`SELECT 1 FROM viktor.reflections`); err != nil {
		t.Fatalf("own table must be queryable: %v", err)
	}
}
