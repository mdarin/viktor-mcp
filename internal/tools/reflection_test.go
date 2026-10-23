package tools

import (
	"context"
	"errors"
	"testing"

	"viktor/internal/reflection"
)

// fakeReflectStore is the in-memory Store for tool tests.
type fakeReflectStore struct {
	records []reflection.Record
	nextID  int64
	down    error
}

func (f *fakeReflectStore) Log(_ context.Context, r reflection.Record) (int64, error) {
	if f.down != nil {
		return 0, f.down
	}
	f.nextID++
	r.ID = f.nextID
	f.records = append(f.records, r)
	return r.ID, nil
}

func (f *fakeReflectStore) List(_ context.Context, taskID int64, limit int) ([]reflection.Record, error) {
	if f.down != nil {
		return nil, f.down
	}
	var out []reflection.Record
	for i := len(f.records) - 1; i >= 0; i-- {
		if f.records[i].TaskID == taskID {
			out = append(out, f.records[i])
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (f *fakeReflectStore) Since(_ context.Context, afterID int64, limit int) ([]reflection.Record, error) {
	if f.down != nil {
		return nil, f.down
	}
	var out []reflection.Record
	for _, r := range f.records {
		if r.ID > afterID {
			out = append(out, r)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (f *fakeReflectStore) Close() error { return nil }

func TestReflectionNotConfigured(t *testing.T) {
	_, session := newCRUDFixture(t)
	for _, name := range []string{"reflection_log", "reflection_list"} {
		args := map[string]any{"task_id": 301}
		if name == "reflection_log" {
			args["goal_conflict"], args["resolution"] = "q", "r"
		}
		out, err := callCRUD(t, session, name, args)
		if err != nil {
			t.Fatal(err)
		}
		if out["supported"] != false {
			t.Fatalf("%s: want degradation, got %v", name, out)
		}
	}
}

func TestReflectionLogValidation(t *testing.T) {
	_, session := newCRUDFixture(t)
	if _, err := callCRUD(t, session, "reflection_log", map[string]any{"goal_conflict": "q", "resolution": "r"}); err == nil {
		t.Fatal("missing task_id must fail")
	}
	if _, err := callCRUD(t, session, "reflection_log", map[string]any{"task_id": 301, "resolution": "r"}); err == nil {
		t.Fatal("missing goal_conflict must fail")
	}
	if _, err := callCRUD(t, session, "reflection_log", map[string]any{"task_id": 301, "goal_conflict": "q"}); err == nil {
		t.Fatal("missing resolution must fail")
	}
	if _, err := callCRUD(t, session, "reflection_list", map[string]any{}); err == nil {
		t.Fatal("missing task_id must fail")
	}
}

func TestReflectionLogAndList(t *testing.T) {
	f, session := newCRUDFixture(t)
	store := &fakeReflectStore{}
	f.registry.SetReflectionStore("default", store)

	out, err := callCRUD(t, session, "reflection_log", map[string]any{
		"task_id": 301, "goal_conflict": "quality vs speed", "resolution": "scoped patch",
		"outcome": "merged", "confidence": 1.7, "goals": map[string]any{"quality": 1.0, "speed": 0.6},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out["supported"] != true || out["id"] == nil {
		t.Fatalf("log out = %v", out)
	}
	if store.records[0].Confidence != 1 {
		t.Fatalf("confidence not clamped: %v", store.records[0].Confidence)
	}

	out, err = callCRUD(t, session, "reflection_list", map[string]any{"task_id": 301})
	if err != nil {
		t.Fatal(err)
	}
	if out["count"] != float64(1) {
		t.Fatalf("list out = %v", out)
	}
	rec := out["records"].([]any)[0].(map[string]any)
	if rec["goal_conflict"] != "quality vs speed" || rec["resolution"] != "scoped patch" {
		t.Fatalf("round-trip mismatch: %v", rec)
	}
}

func TestReflectionDegradeOnDown(t *testing.T) {
	f, session := newCRUDFixture(t)
	store := &fakeReflectStore{down: &reflection.ErrUnavailable{Cause: "db down"}}
	f.registry.SetReflectionStore("default", store)
	out, err := callCRUD(t, session, "reflection_log", map[string]any{"task_id": 301, "goal_conflict": "q", "resolution": "r"})
	if err != nil {
		t.Fatal(err)
	}
	if out["supported"] != false {
		t.Fatalf("want degrade, got %v", out)
	}
	out, err = callCRUD(t, session, "reflection_list", map[string]any{"task_id": 301})
	if err != nil {
		t.Fatal(err)
	}
	if out["supported"] != false {
		t.Fatalf("list: want degrade, got %v", out)
	}
}

func TestReflectionAppErrorNotDegraded(t *testing.T) {
	// An application-level error (not classified as unavailable) must
	// surface as a tool error, not a {supported:false} row.
	f, session := newCRUDFixture(t)
	store := &fakeReflectStore{down: errors.New("constraint violation")}
	f.registry.SetReflectionStore("default", store)
	if _, err := callCRUD(t, session, "reflection_log", map[string]any{"task_id": 301, "goal_conflict": "q", "resolution": "r"}); err == nil {
		t.Fatal("app error must fail, not degrade")
	}
}
