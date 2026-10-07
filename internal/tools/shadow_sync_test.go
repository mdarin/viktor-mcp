package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"viktor/internal/client"
	"viktor/internal/shadow"
)

// newSyncSession builds an MCP session whose tracker client points at a fake
// Vikunja accepting task writes, with a temp shadow store enabled. The second
// return value exposes the registry for direct inspection.
func newSyncSession(t *testing.T) (*mcp.ClientSession, *Registry, *shadow.Store) {
	t.Helper()
	var created atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		switch {
		case r.URL.Path == "/api/v1/info":
			writeTestJSON(w, map[string]any{"version": "test"})
		case r.Method == "PUT" && strings.HasPrefix(r.URL.Path, "/api/v1/projects/") && strings.HasSuffix(r.URL.Path, "/tasks"):
			id := 100 + created.Add(1)
			writeTestJSON(w, map[string]any{"id": id, "title": "from shadow"})
		case r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/api/v1/tasks/"):
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			body["id"] = float64(42)
			writeTestJSON(w, body)
		case r.Method == "GET" && strings.Contains(r.URL.Path, "/tasks") && page != "1":
			writeTestJSON(w, []map[string]any{})
		case r.Method == "GET" && strings.Contains(r.URL.Path, "/tasks"):
			writeTestJSON(w, []map[string]any{{"id": 7, "title": "from shadow", "done": false}})
		case r.Method == "GET" && page != "1":
			writeTestJSON(w, []map[string]any{})
		case r.Method == "GET":
			writeTestJSON(w, []map[string]any{{"id": 64, "title": "p64", "is_archived": false}})
		default:
			writeTestJSON(w, map[string]any{})
		}
	}))
	t.Cleanup(srv.Close)

	store, err := shadow.NewStore(filepath.Join(t.TempDir(), "shadow"), "test")
	if err != nil {
		t.Fatal(err)
	}
	srvMCP := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.0.0"}, nil)
	reg := NewRegistry(srvMCP, client.New(srv.URL, "tok", 0))
	reg.SetShadowStore(store)
	reg.RegisterAll()

	ctx := context.Background()
	c := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.0"}, nil)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := srvMCP.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	clientSession, err := c.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = clientSession.Close() })
	return clientSession, reg, store
}

func writeTestJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func TestShadowSyncReplaysCreate(t *testing.T) {
	s, reg, store := newSyncSession(t)
	store.Append(&shadow.Entry{
		ShadowID: "s-1", TS: "2026-10-07T10:00:00Z", Op: "create",
		ProjectID: 64, Payload: json.RawMessage(`{"project_id":64,"title":"from shadow"}`),
	})
	text, ok := callToolJSON(t, s, "shadow_sync", `{}`)
	if !ok {
		t.Fatalf("shadow_sync should run: %s", text)
	}
	// The fake tracker already has an open "from shadow" task (id 7), so the
	// replay engine dedups the create instead of duplicating the card.
	assertContains(t, text, `"replayed":1`)
	assertContains(t, text, `"deduplicated":1`)
	assertContains(t, text, `"created":0`)
	assertContains(t, text, `"pending":0`)
	if reg.shadowStore.PendingCount() != 0 {
		t.Fatal("queue should be drained")
	}
}

func TestShadowSyncAutoAfterRestore(t *testing.T) {
	s, reg, store := newSyncSession(t)
	store.Append(&shadow.Entry{
		ShadowID: "s-1", TS: "2026-10-07T10:00:00Z", Op: "complete",
		ProjectID: 64, Payload: json.RawMessage(`{"task_id":42}`),
	})
	if reg.shadowStore.PendingCount() != 1 {
		t.Fatal("entry should be pending")
	}
	// Mark the tracker offline first (auto-sync fires on the offline→online
	// transition; the registry starts in the online state).
	reg.noteReachable(context.Background(), false)
	if reg.shadowStore.PendingCount() != 1 {
		t.Fatal("entry should stay pending while offline")
	}
	// A successful reachable call flips reachable and triggers auto-sync.
	if _, ok := callToolJSON(t, s, "project_list", `{}`); !ok {
		t.Fatal("project_list should succeed against fake server")
	}
	if reg.shadowStore.PendingCount() != 0 {
		t.Fatalf("auto-sync should drain queue, pending=%d", reg.shadowStore.PendingCount())
	}
}

func TestSyncShadowCreateBodyFields(t *testing.T) {
	body := createBody(map[string]any{
		"title": "t", "priority": float64(3), "due_date": "0001-01-01T00:00:00Z",
		"description": "d",
	})
	if body["due_date"] != nil {
		t.Fatal("zero due_date must be dropped")
	}
	if body["priority"] != 3 || body["description"] != "d" || body["title"] != "t" {
		t.Fatalf("unexpected body: %v", body)
	}
}
