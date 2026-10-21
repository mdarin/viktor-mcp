package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"viktor/internal/client"
)

type fakeVikunja struct {
	task          map[string]any
	view          map[string]any
	buckets       []map[string]any
	resetBucket   bool
	nextBucketID  int
	calls         map[string]int
	positionsBody map[string]any
}

func (f *fakeVikunja) count(method, path string) {
	f.calls[method+" "+path]++
}

func (f *fakeVikunja) handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	f.count(r.Method, r.URL.Path)
	body, _ := io.ReadAll(r.Body)
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v2/tasks/42":
		json.NewEncoder(w).Encode(f.task)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v2/tasks/42":
		var patch map[string]any
		json.Unmarshal(body, &patch)
		for k, v := range patch {
			f.task[k] = v
		}
		if f.resetBucket {
			f.task["bucket_id"] = float64(0)
		}
		json.NewEncoder(w).Encode(f.task)
	case r.Method == http.MethodGet && r.URL.Path == "/api/v2/projects/8/views":
		json.NewEncoder(w).Encode([]map[string]any{f.view})
	case r.Method == http.MethodGet && r.URL.Path == "/api/v2/projects/8/views/32/buckets":
		json.NewEncoder(w).Encode(f.buckets)
	case r.Method == http.MethodPut && r.URL.Path == "/api/v2/projects/8/views/32/buckets":
		var nb map[string]any
		json.Unmarshal(body, &nb)
		f.nextBucketID++
		nb["id"] = float64(f.nextBucketID)
		f.buckets = append(f.buckets, nb)
		json.NewEncoder(w).Encode(nb)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v2/projects/8/views/32":
		var patch map[string]any
		json.Unmarshal(body, &patch)
		for k, v := range patch {
			f.view[k] = v
		}
		json.NewEncoder(w).Encode(f.view)
	case r.Method == http.MethodGet && r.URL.Path == "/api/v2/projects/8/views/32/tasks":
		buckets := make([]map[string]any, 0, len(f.buckets))
		for _, b := range f.buckets {
			buckets = append(buckets, map[string]any{
				"id":    b["id"],
				"title": b["title"],
				"tasks": []any{},
			})
		}
		for i, b := range f.buckets {
			if int(b["id"].(float64)) == int(f.task["bucket_id"].(float64)) {
				buckets[i]["tasks"] = []any{f.task}
			}
		}
		json.NewEncoder(w).Encode(buckets)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v2/tasks/42/position":
		var pos map[string]any
		json.Unmarshal(body, &pos)
		f.positionsBody = pos
		f.task["bucket_id"] = pos["bucket_id"]
		w.WriteHeader(http.StatusOK)
	default:
		http.Error(w, "unexpected: "+r.Method+" "+r.URL.Path, http.StatusNotFound)
	}
}

func newCompleteFixture(t *testing.T, resetBucket bool) (*fakeVikunja, *mcp.ClientSession) {
	t.Helper()
	f := &fakeVikunja{
		task: map[string]any{
			"id":         float64(42),
			"title":      "close me",
			"done":       false,
			"priority":   float64(3),
			"bucket_id":  float64(41),
			"project_id": float64(8),
		},
		view: map[string]any{
			"id":             float64(32),
			"view_kind":      "kanban",
			"done_bucket_id": float64(43),
		},
		buckets: []map[string]any{
			{"id": float64(41), "title": "todo"},
			{"id": float64(42), "title": "in-progress"},
			{"id": float64(43), "title": "done"},
		},
		resetBucket:  resetBucket,
		nextBucketID: 50,
		calls:        map[string]int{},
	}
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(srv.Close)

	api := client.New(srv.URL, "test-token", 0)
	srvMCP := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.0.0"}, nil)
	NewRegistry(srvMCP, api).RegisterAll()

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
	return f, session
}

func callComplete(t *testing.T, session *mcp.ClientSession) map[string]any {
	t.Helper()
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "task_complete",
		Arguments: map[string]any{"task_id": 42},
	})
	if err != nil {
		t.Fatalf("task_complete: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool error: %v", res.Content)
	}
	out, _ := json.Marshal(res.StructuredContent)
	var m map[string]any
	json.Unmarshal(out, &m)
	return m
}

func TestCompleteHappyPath(t *testing.T) {
	f, session := newCompleteFixture(t, false)
	out := callComplete(t, session)

	if out["done"] != true {
		t.Errorf("done = %v", out["done"])
	}
	if out["bucket_id"] != float64(43) {
		t.Errorf("bucket_id = %v, want 43", out["bucket_id"])
	}
	if out["fallback_used"] == true {
		t.Error("fallback must not fire on happy path")
	}
	for _, ep := range []string{
		"POST /api/v2/projects/8/views/32",
		"PUT /api/v2/projects/8/views/32/buckets",
		"POST /api/v2/tasks/42/position",
	} {
		if f.calls[ep] != 0 {
			t.Errorf("%s must not be called on happy path", ep)
		}
	}
	if f.task["done"] != true {
		t.Error("fake task not marked done")
	}
}

func TestCompleteAutoConfiguresDoneBucket(t *testing.T) {
	f, session := newCompleteFixture(t, false)
	f.view["done_bucket_id"] = float64(0)
	out := callComplete(t, session)

	if f.calls["POST /api/v2/projects/8/views/32"] != 1 {
		t.Errorf("view must be configured once, calls=%v", f.calls)
	}
	if f.calls["PUT /api/v2/projects/8/views/32/buckets"] != 0 {
		t.Error("existing done bucket must be reused, not recreated")
	}
	if out["bucket_id"] != float64(43) {
		t.Errorf("bucket_id = %v, want 43", out["bucket_id"])
	}
	if f.view["done_bucket_id"] != float64(43) {
		t.Errorf("view done_bucket_id = %v", f.view["done_bucket_id"])
	}
}

func TestCompleteCreatesMissingDoneBucket(t *testing.T) {
	f, session := newCompleteFixture(t, false)
	f.view["done_bucket_id"] = float64(0)
	f.buckets = f.buckets[:2]

	out := callComplete(t, session)

	if f.calls["PUT /api/v2/projects/8/views/32/buckets"] != 1 {
		t.Errorf("done bucket must be created once, calls=%v", f.calls)
	}
	if out["bucket_id"] != float64(51) {
		t.Errorf("bucket_id = %v, want created id 51", out["bucket_id"])
	}
	if f.view["done_bucket_id"] != float64(51) {
		t.Errorf("view done_bucket_id = %v, want 51", f.view["done_bucket_id"])
	}
}

func TestCompleteFallsBackToPosition(t *testing.T) {
	f, session := newCompleteFixture(t, true)
	out := callComplete(t, session)

	if f.calls["POST /api/v2/tasks/42/position"] != 1 {
		t.Errorf("fallback position must fire once, calls=%v", f.calls)
	}
	if out["fallback_used"] != true {
		t.Error("fallback_used must be reported")
	}
	if out["bucket_id"] != float64(43) {
		t.Errorf("final bucket_id = %v, want 43", out["bucket_id"])
	}
	pos := fmt.Sprint(f.positionsBody["bucket_id"])
	if pos != "43" {
		t.Errorf("position body bucket_id = %v, want 43", f.positionsBody)
	}
	if f.positionsBody["project_view_id"] != float64(32) {
		t.Errorf("position body project_view_id = %v, want 32", f.positionsBody)
	}
}

func TestCompleteNoKanbanView(t *testing.T) {
	f, session := newCompleteFixture(t, false)
	f.view["view_kind"] = "list"
	out := callComplete(t, session)

	if out["done"] != true {
		t.Errorf("done = %v", out["done"])
	}
	if f.task["done"] != true {
		t.Error("task must still be marked done without kanban view")
	}
	if f.calls["POST /api/v2/tasks/42/position"] != 0 {
		t.Error("no bucket ops without kanban view")
	}
}
