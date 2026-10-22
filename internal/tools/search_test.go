package tools

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"viktor/internal/memory"
)

func TestSearchAll(t *testing.T) {
	f, session := newCRUDFixture(t)
	f.projects[900] = map[string]any{"id": float64(900), "title": "alpha proj", "description": ""}
	out, err := callCRUD(t, session, "search_all", map[string]any{"query": "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	if out["query"] != "alpha" {
		t.Fatalf("out = %v", out)
	}
	counts := out["counts"].(map[string]any)
	if counts["projects"] != float64(1) {
		t.Fatalf("counts = %v", counts)
	}
	row := out["projects"].([]any)[0].(map[string]any)
	if row["title"] != "alpha proj" {
		t.Fatalf("row = %v", row)
	}
	if _, err := callCRUD(t, session, "search_all", map[string]any{"query": "  "}); err == nil {
		t.Fatal("blank query must be rejected")
	}
}

func TestSearchAllTasks(t *testing.T) {
	_, session := newCRUDFixture(t)
	out, err := callCRUD(t, session, "search_all_tasks", map[string]any{"query": "a"})
	if err != nil {
		t.Fatal(err)
	}
	if out["count"] != float64(1) {
		t.Fatalf("out = %v", out)
	}
	out, err = callCRUD(t, session, "search_all_tasks", map[string]any{"query": "c", "project_id": 9})
	if err != nil {
		t.Fatal(err)
	}
	if out["count"] != float64(1) {
		t.Fatalf("out = %v", out)
	}
	row := out["tasks"].([]any)[0].(map[string]any)
	if row["project_id"] != float64(9) {
		t.Fatalf("row = %v", row)
	}
	if _, err := callCRUD(t, session, "search_all_tasks", map[string]any{"query": ""}); err == nil {
		t.Fatal("empty query must be rejected")
	}
}

// --- search_hybrid (card #824) ---

// fakeMemoryStack spins up fake Ollama (/api/embed) and Qdrant
// (/collections/<name>/points/search) servers for the semantic half.
func fakeMemoryStack(t *testing.T, hits []map[string]any) (ollamaURL, qdrantURL string) {
	t.Helper()
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			http.Error(w, "unexpected", http.StatusNotFound)
			return
		}
		var b struct {
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&b)
		vecs := make([][]float32, len(b.Input))
		for i := range vecs {
			vecs[i] = []float32{0.1, 0.2}
		}
		json.NewEncoder(w).Encode(map[string]any{"embeddings": vecs})
	}))
	t.Cleanup(ollama.Close)

	qdrant := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !jsonEndsWith(r.URL.Path, "/points/search") {
			http.Error(w, "unexpected", http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"result": hits})
	}))
	t.Cleanup(qdrant.Close)
	return ollama.URL, qdrant.URL
}

func jsonEndsWith(path, suffix string) bool {
	return len(path) >= len(suffix) && path[len(path)-len(suffix):] == suffix
}

func TestSearchHybridMergeDedupe(t *testing.T) {
	f, session := newCRUDFixture(t)
	// Exact half returns 301 ("a"); memory returns 301 (dup → dropped)
	// and 777 (new → source=memory).
	ol, qd := fakeMemoryStack(t, []map[string]any{
		{"score": 0.95, "payload": map[string]any{"task_id": 301, "title": "a", "chunk_type": "task"}},
		{"score": 0.81, "payload": map[string]any{"task_id": 777, "title": "semantic gem", "chunk_type": "task", "project_id": 8, "status": "open", "snippet": "why we did it"}},
	})
	f.registry.SetMemoryConfigs(map[string]memory.Config{"default": {
		QdrantURL: qd, OllamaURL: ol, EmbedModel: "fake", Collection: "viktor_tasks",
	}})
	out, err := callCRUD(t, session, "search_hybrid", map[string]any{"query": "a"})
	if err != nil {
		t.Fatal(err)
	}
	rows := out["tasks"].([]any)
	if len(rows) != 2 {
		t.Fatalf("want 2 merged rows (dup collapsed), got %d: %v", len(rows), out)
	}
	r0 := rows[0].(map[string]any)
	if r0["id"] != float64(301) || r0["source"] != "exact" {
		t.Fatalf("exact row first: %v", r0)
	}
	r1 := rows[1].(map[string]any)
	if r1["id"] != float64(777) || r1["source"] != "memory" || r1["score"] != 0.81 || r1["snippet"] != "why we did it" {
		t.Fatalf("memory row: %v", r1)
	}
	sem := out["semantic"].(map[string]any)
	if sem["used"] != true || sem["k"] != float64(5) {
		t.Fatalf("semantic block: %v", sem)
	}
	if out["count"] != float64(2) {
		t.Fatalf("count: %v", out["count"])
	}
}

func TestSearchHybridReserveSlots(t *testing.T) {
	f, session := newCRUDFixture(t)
	// Exact half returns 301; memory-only hit 777 reserves a slot even
	// when the tracker's fuzzy search already fills the limit.
	ol, qd := fakeMemoryStack(t, []map[string]any{
		{"score": 0.9, "payload": map[string]any{"task_id": 777, "title": "semantic gem", "chunk_type": "task"}},
	})
	f.registry.SetMemoryConfigs(map[string]memory.Config{"default": {
		QdrantURL: qd, OllamaURL: ol, EmbedModel: "fake", Collection: "viktor_tasks",
	}})
	out, err := callCRUD(t, session, "search_hybrid", map[string]any{"query": "a", "limit": 1})
	if err != nil {
		t.Fatal(err)
	}
	rows := out["tasks"].([]any)
	if len(rows) != 1 {
		t.Fatalf("limit must hold: %v", out)
	}
	r0 := rows[0].(map[string]any)
	if r0["id"] != float64(777) || r0["source"] != "memory" {
		t.Fatalf("memory hit must get the slot: %v", r0)
	}
}

func TestSearchHybridDegrades(t *testing.T) {
	f, session := newCRUDFixture(t)

	// No memory config at all → note, exact results survive.
	out, err := callCRUD(t, session, "search_hybrid", map[string]any{"query": "a"})
	if err != nil {
		t.Fatal(err)
	}
	if out["count"] != float64(1) {
		t.Fatalf("exact half must survive: %v", out)
	}
	sem := out["semantic"].(map[string]any)
	if sem["used"] != false || sem["note"] != "memory not configured; exact results only" {
		t.Fatalf("degradation note: %v", sem)
	}

	// Memory configured but down → note, exact results survive.
	olSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "down", 500) }))
	defer olSrv.Close()
	qdSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "down", 500) }))
	defer qdSrv.Close()
	f.registry.SetMemoryConfigs(map[string]memory.Config{"default": {
		QdrantURL: qdSrv.URL, OllamaURL: olSrv.URL, EmbedModel: "fake", Collection: "viktor_tasks",
	}})
	out, err = callCRUD(t, session, "search_hybrid", map[string]any{"query": "a"})
	if err != nil {
		t.Fatalf("memory outage must not fail the tool: %v", err)
	}
	if out["count"] != float64(1) {
		t.Fatalf("exact half must survive outage: %v", out)
	}
	sem = out["semantic"].(map[string]any)
	if sem["used"] != false || sem["note"] == "" {
		t.Fatalf("outage note: %v", sem)
	}
	if _, err := callCRUD(t, session, "search_hybrid", map[string]any{"query": ""}); err == nil {
		t.Fatal("empty query must be rejected")
	}
}

func TestSemanticKCap(t *testing.T) {
	if semanticK(0) != 5 || semanticK(3) != 3 || semanticK(99) != 10 {
		t.Fatal("semanticK defaults/cap broken")
	}
}
