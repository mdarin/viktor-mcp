package memory

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

// --- reflection journal (card #853) ---

func TestChunkReflection(t *testing.T) {
	ch := ChunkReflection(ReflectionRow{
		ID: 12, TaskID: 853, Repo: "viktor",
		Goals:        map[string]float64{"quality": 1, "speed": 0.6},
		GoalConflict: "quality vs speed",
		Resolution:   "journal first, embed later",
		Outcome:      "shipped",
		Confidence:   0.85,
		CreatedAt:    "2026-10-23T10:00:00Z",
	})
	if ch.Type != ChunkTypeReflection || ch.TaskID != 853 {
		t.Fatalf("chunk meta: %+v", ch)
	}
	if ch.Key != "reflection-12" {
		t.Fatalf("key = %q", ch.Key)
	}
	if ch.Payload["chunk_type"] != "reflection" || ch.Payload["reflection_id"] != int64(12) {
		t.Fatalf("payload: %v", ch.Payload)
	}
	for _, want := range []string{"goal_conflict: quality vs speed", "resolution: journal first", "outcome: shipped", "confidence: 0.85", "goals: quality=1.00 speed=0.60"} {
		if !strings.Contains(ch.Text, want) {
			t.Fatalf("text missing %q:\n%s", want, ch.Text)
		}
	}
}

type fakeReflectionSource struct {
	rows  []ReflectionRow
	err   error
	calls int
}

func (f *fakeReflectionSource) ReflectionsSince(_ context.Context, afterID int64, limit int) ([]ReflectionRow, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	var out []ReflectionRow
	for _, r := range f.rows {
		if r.ID > afterID {
			out = append(out, r)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func readReflectionCursor(t *testing.T, sp string) int64 {
	t.Helper()
	data, err := os.ReadFile(sp)
	if err != nil {
		t.Fatal(err)
	}
	var st struct {
		ReflectionCursor int64 `json:"reflection_cursor"`
	}
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatal(err)
	}
	return st.ReflectionCursor
}

func TestSyncIndexesReflections(t *testing.T) {
	src := &fakeSource{tasks: []Task{{ID: 853, Title: "t", Updated: "2026-10-23T10:00:00Z"}}}
	emb := &fakeEmbedder{dim: 3}
	st := newFakeStore()
	ix, _ := newTestIndexer(t, src, emb, st, nil)
	journal := &fakeReflectionSource{rows: []ReflectionRow{
		{ID: 1, TaskID: 853, GoalConflict: "c1", Resolution: "r1"},
		{ID: 2, TaskID: 853, GoalConflict: "c2", Resolution: "r2"},
		{ID: 3, TaskID: 999, GoalConflict: "c3", Resolution: "r3"},
	}}
	ix.SetReflectionSource(journal)

	rep, err := ix.Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Reflections != 3 {
		t.Fatalf("reflections = %d, report: %+v", rep.Reflections, rep)
	}
	if st.upserts < 3 {
		t.Fatalf("upserts = %d", st.upserts)
	}

	// Repeat sync: hash-compare makes reflection re-indexing free.
	rep2, err := ix.Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rep2.Reflections != 0 {
		t.Fatalf("second sync re-indexed %d reflections", rep2.Reflections)
	}

	// New journal rows only.
	journal.rows = append(journal.rows, ReflectionRow{ID: 4, TaskID: 853, GoalConflict: "c4", Resolution: "r4"})
	rep3, err := ix.Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rep3.Reflections != 1 {
		t.Fatalf("third sync reflections = %d", rep3.Reflections)
	}

	// Reflection chunks must not participate in the scan's ghost
	// cleanup: the complete scan deletes nothing even though task 999
	// (owner of reflection 3) is not on the board — journal-owned
	// lifecycle, not scan-owned.
	if rep.Deleted != 0 {
		t.Fatalf("complete scan deleted %d points (reflections must not ghost)", rep.Deleted)
	}
}

func TestSyncReflectionJournalDown(t *testing.T) {
	src := &fakeSource{tasks: []Task{{ID: 1, Title: "t", Updated: "2026-10-23T10:00:00Z"}}}
	emb := &fakeEmbedder{dim: 3}
	st := newFakeStore()
	ix, _ := newTestIndexer(t, src, emb, st, nil)
	ix.SetReflectionSource(&fakeReflectionSource{err: errors.New("db down")})

	rep, err := ix.Sync(context.Background())
	if err != nil {
		t.Fatalf("journal failure must degrade, not fail: %v", err)
	}
	if rep.ReflectionsError == "" {
		t.Fatalf("want reflections_error, got %+v", rep)
	}
	if st.upserts == 0 {
		t.Fatal("board projection must continue")
	}
}

func TestSyncReflectionCursorSemantics(t *testing.T) {
	src := &fakeSource{tasks: []Task{{ID: 1, Title: "t", Updated: "2026-10-23T10:00:00Z"}}}
	emb := &fakeEmbedder{dim: 3}
	st := newFakeStore()
	ix, sp := newTestIndexer(t, src, emb, st, nil)
	journal := &fakeReflectionSource{rows: []ReflectionRow{
		{ID: 7, TaskID: 1, GoalConflict: "c", Resolution: "r"},
		{ID: 8, TaskID: 1, GoalConflict: "c2", Resolution: "r2"},
	}}
	ix.SetReflectionSource(journal)
	if _, err := ix.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := readReflectionCursor(t, sp); got != 8 {
		t.Fatalf("cursor after success = %d, want 8", got)
	}

	// Failed pass: the cursor must stay so nothing is skipped.
	journal.err = errors.New("gone")
	if _, err := ix.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := readReflectionCursor(t, sp); got != 8 {
		t.Fatalf("cursor advanced on failure: %d", got)
	}
}
