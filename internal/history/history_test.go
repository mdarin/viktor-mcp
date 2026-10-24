package history

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestNewPGRejectsEmptyDSN(t *testing.T) {
	if _, err := NewPG(Config{}); err == nil {
		t.Fatal("empty DSN must be rejected")
	}
}

// TestPGIntegration is the live-database test; skipped unless
// VIKTOR_HISTORY_TEST_DSN points at a scratch ParadeDB/Postgres. It
// asserts the idempotency rule (a repeated ReplaceTask never
// duplicates rows) and both retrieval modes.
func TestPGIntegration(t *testing.T) {
	dsn := os.Getenv("VIKTOR_HISTORY_TEST_DSN")
	if dsn == "" {
		t.Skip("set VIKTOR_HISTORY_TEST_DSN to run the live PG integration test")
	}
	pg, err := NewPG(Config{PGDSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pg.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Idempotent auto-migration.
	for i := 0; i < 2; i++ {
		if err := pg.ensureSchema(ctx); err != nil {
			t.Fatalf("migrate run %d: %v", i, err)
		}
	}

	instance := fmt.Sprintf("test-%d", time.Now().UnixNano())
	docs := func() []Doc {
		return []Doc{
			{TaskID: 4242, ProjectID: 1, Instance: instance, Kind: KindTask,
				Content: "search_history: BM25 lexical history search (card #867)"},
			{TaskID: 4242, ProjectID: 1, Instance: instance, Kind: KindComment, Seq: 1,
				Content: "panic: undefined function paradedb.match in migration probe"},
			{TaskID: 4242, ProjectID: 1, Instance: instance, Kind: KindComment, Seq: 2,
				Content: "fixed by ILIKE fallback with escaped percent signs"},
		}
	}
	for i := 0; i < 3; i++ {
		if err := pg.ReplaceTask(ctx, docs()); err != nil {
			t.Fatalf("replace run %d: %v", i, err)
		}
	}
	n, err := pg.Count(ctx, instance)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("repeated sync duplicated rows: want 3 docs, got %d", n)
	}

	hits, mode, err := pg.Search(ctx, Query{Text: "paradedb.match", Instance: instance})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("retrieval mode: %s, hits: %d", mode, len(hits))
	if len(hits) == 0 {
		t.Fatal("want >=1 hit")
	}
	if hits[0].Kind != KindComment {
		t.Fatalf("comment hit must rank first, got %+v", hits[0])
	}

	// Ghost cleanup: only the live task id survives.
	removed, err := pg.DeleteNotIn(ctx, instance, []int64{4242})
	if err != nil {
		t.Fatal(err)
	}
	if removed != 0 {
		t.Fatalf("no ghosts expected, removed %d", removed)
	}
	if err := pg.ReplaceTask(ctx, []Doc{{TaskID: 4243, ProjectID: 1, Instance: instance, Kind: KindTask, Content: "ghost"}}); err != nil {
		t.Fatal(err)
	}
	removed, err = pg.DeleteNotIn(ctx, instance, []int64{4242})
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("want 1 ghost removed, got %d", removed)
	}
}
