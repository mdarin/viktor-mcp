package memory

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// repoFilterStore emulates the Qdrant must-match filter so the test can
// show repo= actually excludes foreign-repo points (card #843).
type repoFilterStore struct {
	points []SearchHit
	got    *SearchFilter
}

func (s *repoFilterStore) SearchPoints(_ context.Context, _ string, _ []float32, k int, f *SearchFilter) ([]SearchHit, error) {
	s.got = f
	out := []SearchHit{}
	for _, p := range s.points {
		if filterMatches(f, p.Payload) {
			out = append(out, p)
		}
		if len(out) == k {
			break
		}
	}
	return out, nil
}

func filterMatches(f *SearchFilter, payload map[string]any) bool {
	if f == nil {
		return true
	}
	for _, m := range f.Must {
		key, _ := m["key"].(string)
		match, _ := m["match"].(map[string]any)
		val, ok := match["value"]
		if ok {
			if pv, _ := payload[key].(string); pv != val {
				return false
			}
		}
	}
	return true
}

func TestSearchRepoFilterExcludesForeignRepos(t *testing.T) {
	st := &repoFilterStore{points: []SearchHit{
		{Score: 0.9, Payload: map[string]any{"task_id": float64(1), "chunk_type": "task", "title": "viktor task", "repo": "viktor"}},
		{Score: 0.8, Payload: map[string]any{"task_id": float64(2), "chunk_type": "task", "title": "wirdia task", "repo": "wirdia"}},
	}}
	s, err := NewSearcher(Config{QdrantURL: "http://x", Collection: "c", EmbedModel: "m"}, &fakeEmbedder{dim: 4}, st)
	if err != nil {
		t.Fatal(err)
	}
	hits, err := s.Search(context.Background(), "task", SearchOpts{Repo: "viktor", K: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].TaskID != 1 || hits[0].Repo != "viktor" {
		t.Fatalf("repo filter must keep only viktor hits, got %+v", hits)
	}
	if st.got == nil || len(st.got.Must) != 1 {
		t.Fatalf("expected single must condition, got %+v", st.got)
	}

	// No repo argument → no repo condition, both repos come back.
	hits, err = s.Search(context.Background(), "task", SearchOpts{K: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("without repo=all hits expected, got %d", len(hits))
	}
	if st.got != nil && len(st.got.Must) != 0 {
		t.Fatalf("no filter expected, got %+v", st.got)
	}
}

// TestSyncStampsRepoPayload (card #843): every chunk upserted by an
// indexer configured with GitRepo carries payload.repo = basename, so
// multi-repo instances sharing one collection stay distinguishable.
func TestSyncStampsRepoPayload(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "some-repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	src := &fakeSource{sinceAware: true, tasks: []Task{{ID: 7, Title: "stamped", Updated: "2026-10-23T10:00:00Z"}}}
	st := newFakeStore()
	ix, _ := newTestIndexer(t, src, &fakeEmbedder{dim: 4}, st, nil)
	ix.cfg.GitRepo = repo

	if _, err := ix.SyncFull(context.Background()); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, payload := range st.points {
		if got, _ := payload["repo"].(string); got != "some-repo" {
			t.Fatalf("payload.repo = %q, want some-repo", got)
		}
		n++
	}
	if n == 0 {
		t.Fatal("no points upserted")
	}
}
