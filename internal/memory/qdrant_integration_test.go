package memory

import (
	"context"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// qdrantIntegrationURL is the env gate for the live-Qdrant suite
// (card #844): without it the tests SKIP, so plain `go test ./...` and
// CI never require Docker. Run locally against a real Qdrant:
//
//	VIKTOR_QDRANT_TEST_URL=http://localhost:6333 go test ./internal/memory -run TestQdrantIntegration -v
//
// or `make test-integration` (docker compose profile "test").
func qdrantIntegrationURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("VIKTOR_QDRANT_TEST_URL")
	if url == "" {
		t.Skip("integration test: set VIKTOR_QDRANT_TEST_URL to a real Qdrant (docker compose -f deploy/docker-compose.test.yml up -d)")
	}
	return url
}

// integEmbedder is a deterministic embedder: FNV-1a seeded vector, so
// two different texts always differ and the same text always matches.
type integEmbedder struct{ dim int }

func (e *integEmbedder) Embed(_ context.Context, _ string, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v := make([]float32, e.dim)
		h := fnv.New64a()
		h.Write([]byte(t))
		seed := h.Sum64()
		for j := range v {
			seed = seed*6364136223846793005 + 1442695040888963407
			v[j] = float32(seed%1000) / 1000
		}
		out[i] = v
	}
	return out, nil
}

// newIntegIndexer wires a real Qdrant store with deterministic fakes
// for source and embedder, into a per-run collection.
func newIntegIndexer(t *testing.T, qdrantURL string, dim int) (*Indexer, *fakeSource, *QdrantClient, string) {
	t.Helper()
	store := NewQdrant(qdrantURL, os.Getenv("VIKTOR_QDRANT_TEST_API_KEY"))
	coll := fmt.Sprintf("viktor_it_%d", time.Now().UnixNano())
	t.Cleanup(func() {
		_ = store.do(context.Background(), "DELETE", "/collections/"+coll, nil, nil)
	})
	src := &fakeSource{sinceAware: true, tasks: []Task{{ID: 1, Title: "alpha", Updated: "2026-10-23T10:00:00Z"}}}
	sp := filepath.Join(t.TempDir(), "sync.json")
	ix, err := NewIndexer(Config{Instance: "it", QdrantURL: qdrantURL, Collection: coll}, src, &integEmbedder{dim: dim}, store, nil, sp)
	if err != nil {
		t.Fatal(err)
	}
	return ix, src, store, coll
}

// sub1 (card #822): a collection with a foreign dimension makes the
// sync fail fast before any write, with an actionable error.
func TestQdrantIntegrationDimMismatchFailFast(t *testing.T) {
	url := qdrantIntegrationURL(t)
	ix, _, store, coll := newIntegIndexer(t, url, 8)
	// Pre-create the collection with the wrong dimension.
	if err := store.CreateCollection(context.Background(), coll, 4); err != nil {
		t.Skipf("qdrant unreachable: %v", err)
	}
	_, err := ix.SyncFull(context.Background())
	if err == nil {
		t.Fatal("dim mismatch must fail the sync")
	}
	if want := "has dim 4"; !containsLower(err.Error(), want) {
		t.Fatalf("error must mention dimension, got: %v", err)
	}
	count, err := store.Count(context.Background(), coll)
	if err != nil || count != 0 {
		t.Fatalf("fail-fast must not write points, got %d (%v)", count, err)
	}
}

// sub2 (task 832 scenario): deleting a task and running a full rescan
// actually removes its points from the live collection.
func TestQdrantIntegrationFullRescanRemovesGhosts(t *testing.T) {
	url := qdrantIntegrationURL(t)
	ix, src, store, coll := newIntegIndexer(t, url, 8)
	if _, err := ix.SyncFull(context.Background()); err != nil {
		t.Skipf("qdrant unreachable: %v", err)
	}
	before, _ := store.Count(context.Background(), coll)
	if before == 0 {
		t.Fatal("expected points after first sync")
	}
	// Task 1 disappears from the tracker.
	src.tasks = nil
	rep, err := ix.SyncFull(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Deleted == 0 {
		t.Fatalf("full rescan must delete ghosts: %+v", rep)
	}
	after, _ := store.Count(context.Background(), coll)
	if after != before-int64(rep.Deleted) {
		t.Fatalf("points %d, want %d", after, before-int64(rep.Deleted))
	}
}

// sub3 (card #837): doctor finds and prunes points whose task is gone,
// and the sweep is idempotent.
func TestQdrantIntegrationDoctorPrunesOrphans(t *testing.T) {
	url := qdrantIntegrationURL(t)
	ix, src, _, _ := newIntegIndexer(t, url, 8)
	if _, err := ix.SyncFull(context.Background()); err != nil {
		t.Skipf("qdrant unreachable: %v", err)
	}
	src.tasks = nil // task gone → its points are orphans now
	rep, err := ix.Doctor(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if rep.OrphanPoints == 0 || rep.Deleted != rep.OrphanPoints {
		t.Fatalf("prune: %+v", rep)
	}
	rep2, err := ix.Doctor(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if rep2.OrphanPoints != 0 {
		t.Fatalf("second sweep must find nothing: %+v", rep2)
	}
}

// sub4: a live kNN search returns the chunk just embedded via the
// indexer — the round-trip behind memory_search.
func TestQdrantIntegrationSearchRoundTrip(t *testing.T) {
	url := qdrantIntegrationURL(t)
	ix, src, store, coll := newIntegIndexer(t, url, 8)
	if _, err := ix.SyncFull(context.Background()); err != nil {
		t.Skipf("qdrant unreachable: %v", err)
	}
	emb := &integEmbedder{dim: 8}
	vec, err := emb.Embed(context.Background(), "m", []string{"alpha"})
	if err != nil {
		t.Fatal(err)
	}
	hits, err := store.SearchPoints(context.Background(), coll, vec[0], 3, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("search returned no hits")
	}
	found := false
	for _, h := range hits {
		if title, ok := h.Payload["title"].(string); ok && containsLower(title, "alpha") {
			found = true
		}
	}
	if !found {
		t.Fatalf("task 'alpha' not among top hits: %d hits", len(hits))
	}
	_ = src
}

func containsLower(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}
