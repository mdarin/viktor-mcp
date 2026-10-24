package tools

import (
	"context"
	"errors"
	"strings"
	"testing"

	"viktor/internal/history"
)

// fakeHistStore is the in-memory Store for tool tests.
type fakeHistStore struct {
	docs  []history.Doc
	down  error
	lastQ history.Query
}

func (f *fakeHistStore) ReplaceTask(_ context.Context, docs []history.Doc) error {
	if f.down != nil {
		return f.down
	}
	if len(docs) == 0 {
		return errors.New("no docs")
	}
	kept := f.docs[:0:0]
	for _, d := range f.docs {
		if !(d.Instance == docs[0].Instance && d.TaskID == docs[0].TaskID) {
			kept = append(kept, d)
		}
	}
	f.docs = append(kept, docs...)
	return nil
}

func (f *fakeHistStore) DeleteNotIn(_ context.Context, instance string, taskIDs []int64) (int64, error) {
	if f.down != nil {
		return 0, f.down
	}
	live := map[int64]bool{}
	for _, id := range taskIDs {
		live[id] = true
	}
	var removed int64
	kept := f.docs[:0:0]
	for _, d := range f.docs {
		if d.Instance == instance && !live[d.TaskID] {
			removed++
			continue
		}
		kept = append(kept, d)
	}
	f.docs = kept
	return removed, nil
}

func (f *fakeHistStore) Search(_ context.Context, q history.Query) ([]history.Hit, string, error) {
	if f.down != nil {
		return nil, "", f.down
	}
	f.lastQ = q
	var out []history.Hit
	for _, d := range f.docs {
		if !strings.Contains(strings.ToLower(d.Content), strings.ToLower(strings.TrimSpace(q.Text))) {
			continue
		}
		if q.Instance != "" && d.Instance != q.Instance {
			continue
		}
		if q.ProjectID > 0 && d.ProjectID != q.ProjectID {
			continue
		}
		if q.TaskID > 0 && d.TaskID != q.TaskID {
			continue
		}
		if q.Kind != "" && d.Kind != q.Kind {
			continue
		}
		out = append(out, history.Hit{
			TaskID: d.TaskID, ProjectID: d.ProjectID, Instance: d.Instance,
			Kind: d.Kind, Content: d.Content, Score: 1,
		})
	}
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, "bm25", nil
}

func (f *fakeHistStore) Count(_ context.Context, instance string) (int64, error) {
	if f.down != nil {
		return 0, f.down
	}
	var n int64
	for _, d := range f.docs {
		if instance == "" || d.Instance == instance {
			n++
		}
	}
	return n, nil
}

func (f *fakeHistStore) Close() error { return nil }

func TestSearchHistoryNotConfigured(t *testing.T) {
	_, session := newCRUDFixture(t)
	out, err := callCRUD(t, session, "search_history", map[string]any{"query": "panic"})
	if err != nil {
		t.Fatal(err)
	}
	if out["supported"] != false {
		t.Fatalf("want degradation, got %v", out)
	}
}

func TestSearchHistoryValidation(t *testing.T) {
	_, session := newCRUDFixture(t)
	if _, err := callCRUD(t, session, "search_history", map[string]any{}); err == nil {
		t.Fatal("missing query must fail")
	}
	if _, err := callCRUD(t, session, "search_history", map[string]any{"query": "  "}); err == nil {
		t.Fatal("blank query must fail")
	}
	if _, err := callCRUD(t, session, "search_history", map[string]any{"query": "x", "kind": "bogus"}); err == nil {
		t.Fatal("bogus kind must fail")
	}
}

func TestSearchHistoryFiltersAndLimit(t *testing.T) {
	f, session := newCRUDFixture(t)
	store := &fakeHistStore{}
	f.registry.SetHistoryStore("default", store)
	if err := store.ReplaceTask(context.Background(), []history.Doc{
		{TaskID: 1, ProjectID: 8, Instance: "default", Kind: history.KindTask, Content: "Wrap reflection comment body"},
		{TaskID: 1, ProjectID: 8, Instance: "default", Kind: history.KindComment, Seq: 11, Content: "panic: nil map write in reflection"},
		{TaskID: 2, ProjectID: 9, Instance: "default", Kind: history.KindTask, Content: "panic recovery in watchdog"},
	}); err != nil {
		t.Fatal(err)
	}

	out, err := callCRUD(t, session, "search_history", map[string]any{"query": "panic", "kind": "comment"})
	if err != nil {
		t.Fatal(err)
	}
	if out["supported"] != true || out["count"] != float64(1) {
		t.Fatalf("kind filter out = %v", out)
	}

	out, err = callCRUD(t, session, "search_history", map[string]any{"query": "panic", "project_id": 8, "limit": 1})
	if err != nil {
		t.Fatal(err)
	}
	if out["count"] != float64(1) {
		t.Fatalf("project+limit out = %v", out)
	}
	if store.lastQ.Limit != 1 {
		t.Fatalf("limit not forwarded: %+v", store.lastQ)
	}

	out, err = callCRUD(t, session, "search_history", map[string]any{"query": "wrap reflection"})
	if err != nil {
		t.Fatal(err)
	}
	hits := out["hits"].([]any)
	if len(hits) != 1 || hits[0].(map[string]any)["task_id"] != float64(1) {
		t.Fatalf("task hit out = %v", out)
	}
}

func TestSearchHistoryDegradeOnDown(t *testing.T) {
	f, session := newCRUDFixture(t)
	store := &fakeHistStore{down: &history.ErrUnavailable{Cause: "db down"}}
	f.registry.SetHistoryStore("default", store)
	out, err := callCRUD(t, session, "search_history", map[string]any{"query": "x"})
	if err != nil {
		t.Fatal(err)
	}
	if out["supported"] != false {
		t.Fatalf("want degrade, got %v", out)
	}
}

func TestSearchHistoryAppErrorNotDegraded(t *testing.T) {
	f, session := newCRUDFixture(t)
	store := &fakeHistStore{down: errors.New("constraint violation")}
	f.registry.SetHistoryStore("default", store)
	if _, err := callCRUD(t, session, "search_history", map[string]any{"query": "x"}); err == nil {
		t.Fatal("app error must fail, not degrade")
	}
}
