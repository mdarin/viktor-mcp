package tools

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"viktor/internal/memory"
)

// newMemoryFixture spins fake Ollama and Qdrant backends and a registry
// with the memory config pointed at them.
func newMemoryFixture(t *testing.T, qdrantUp, ollamaUp bool) *mcp.ClientSession {
	t.Helper()

	// Isolate the sync-state file (XDG_STATE_HOME): without this the
	// memory_status test reads the developer's real state and reports
	// "ok" instead of "not synced yet".
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	// Fake Qdrant: one collection with two points (open task, closed
	// task, commit chunk) searchable by score order.
	mux := http.NewServeMux()
	if qdrantUp {
		mux.HandleFunc("POST /collections/viktor_tasks/points/search", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `{"result":[
				{"score":0.91,"payload":{"task_id":301,"project_id":8,"title":"cache invalidation policy","status":"open","labels":["memory"],"chunk_type":"task","snippet":"почему инвалидируется кэш","commits":["abc123"]}},
				{"score":0.84,"payload":{"task_id":302,"project_id":8,"title":"stale TTL fix","status":"done","labels":[],"chunk_type":"task","snippet":"root cause: stale TTL"}},
				{"score":0.71,"payload":{"sha":"dddd444","title":"shadow #360: rate limit","chunk_type":"commit","snippet":"shadow #360: rate limit","task_ids":[360]}}
			]}`)
		})
		mux.HandleFunc("GET /collections/viktor_tasks", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `{"result":{"status":"green","points_count":3,"config":{"params":{"vectors":{"size":4}}}}}`)
		})
	}
	qdrant := httptest.NewServer(mux)
	t.Cleanup(qdrant.Close)

	embedMux := http.NewServeMux()
	if ollamaUp {
		embedMux.HandleFunc("POST /api/embed", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `{"embeddings":[[0.5,0.5,0.5,0.5]]}`)
		})
	}
	embed := httptest.NewServer(embedMux)
	t.Cleanup(embed.Close)

	qURL, eURL := qdrant.URL, embed.URL
	if !qdrantUp {
		qURL = "http://127.0.0.1:1"
	}
	if !ollamaUp {
		eURL = "http://127.0.0.1:1"
	}

	srvMCP := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.0.0"}, nil)
	reg := NewRegistry(srvMCP, nil)
	reg.SetMemoryConfigs(map[string]memory.Config{
		"default": {Instance: "default", QdrantURL: qURL, OllamaURL: eURL, EmbedModel: "bge-m3", Collection: "viktor_tasks"},
	})
	reg.RegisterAll()

	ctx := context.Background()
	c := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.0"}, nil)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := srvMCP.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	session, err := c.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestMemorySearch(t *testing.T) {
	session := newMemoryFixture(t, true, true)
	out, err := callCRUD(t, session, "memory_search", map[string]any{"query": "кэш инвалидация", "k": 5})
	if err != nil {
		t.Fatal(err)
	}
	if out["supported"] != true || out["count"] != float64(3) {
		t.Fatalf("out = %v", out)
	}
	hits := out["hits"].([]any)
	first := hits[0].(map[string]any)
	// Closed tasks are indexed too: they must be searchable, not filtered.
	if first["task_id"] != float64(301) || first["title"] != "cache invalidation policy" {
		t.Fatalf("first hit = %v", first)
	}
	if first["snippet"] == "" || first["chunk_type"] != "task" {
		t.Fatalf("first hit missing snippet/type: %v", first)
	}
	if cs, ok := first["commits"].([]any); !ok || len(cs) != 1 || cs[0] != "abc123" {
		t.Fatalf("commits = %v", first["commits"])
	}
	// Commit chunk carries sha + task_ids bridge.
	last := hits[2].(map[string]any)
	if last["chunk_type"] != "commit" || last["sha"] != "dddd444" {
		t.Fatalf("commit hit = %v", last)
	}
	if ids, ok := last["task_ids"].([]any); !ok || len(ids) != 1 || ids[0] != float64(360) {
		t.Fatalf("task_ids = %v", last["task_ids"])
	}
}

func TestMemorySearchProjectFilter(t *testing.T) {
	session := newMemoryFixture(t, true, true)
	out, err := callCRUD(t, session, "memory_search", map[string]any{"query": "кэш", "project_id": 8, "labels": "memory"})
	if err != nil {
		t.Fatal(err)
	}
	if out["supported"] != true {
		t.Fatalf("out = %v", out)
	}
}

func TestMemorySearchNotConfigured(t *testing.T) {
	_, session := newCRUDFixture(t)
	out, err := callCRUD(t, session, "memory_search", map[string]any{"query": "any"})
	if err != nil {
		t.Fatal(err)
	}
	if out["supported"] != false {
		t.Fatalf("must degrade to not configured, got %v", out)
	}
	if msg, _ := out["message"].(string); msg == "" {
		t.Fatalf("message required: %v", out)
	}
	out, err = callCRUD(t, session, "memory_status", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if out["supported"] != false {
		t.Fatalf("status must degrade too: %v", out)
	}
}

func TestMemorySearchBackendDown(t *testing.T) {
	session := newMemoryFixture(t, false, true) // Qdrant dead
	out, err := callCRUD(t, session, "memory_search", map[string]any{"query": "any"})
	if err != nil {
		t.Fatal(err)
	}
	if out["supported"] != false {
		t.Fatalf("backend down must degrade, got %v", out)
	}
	if msg, _ := out["message"].(string); msg == "" {
		t.Fatalf("message required: %v", out)
	}

	session = newMemoryFixture(t, true, false) // Ollama dead
	out, err = callCRUD(t, session, "memory_search", map[string]any{"query": "any"})
	if err != nil {
		t.Fatal(err)
	}
	if out["supported"] != false {
		t.Fatalf("embedder down must degrade, got %v", out)
	}
}

func TestMemorySearchEmptyQuery(t *testing.T) {
	session := newMemoryFixture(t, true, true)
	if _, err := callCRUD(t, session, "memory_search", map[string]any{"query": "   "}); err == nil {
		t.Fatal("empty query must be a real error")
	}
}

func TestMemoryStatus(t *testing.T) {
	session := newMemoryFixture(t, true, true)
	out, err := callCRUD(t, session, "memory_status", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if out["supported"] != true {
		t.Fatalf("out = %v", out)
	}
	if out["points"] != float64(3) || out["embed_model"] != "bge-m3" || out["collection"] != "viktor_tasks" {
		t.Fatalf("status = %v", out)
	}
	// Fresh index without sync state.
	if st, _ := out["state"].(string); st != "not synced yet" {
		t.Fatalf("state = %v", out["state"])
	}
}
