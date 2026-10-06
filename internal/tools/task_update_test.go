package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"viktor/internal/client"
)

func currentTaskBody() map[string]any {
	return map[string]any{
		"id":          float64(42),
		"title":       "original title",
		"description": "original description",
		"priority":    float64(3),
		"due_date":    "2026-11-01T12:00:00Z",
		"start_date":  "0001-01-01T00:00:00Z",
		"labels":      []any{map[string]any{"id": float64(28), "title": "improvement"}},
		"bucket_id":   float64(42),
		"project_id":  float64(8),
		"done":        false,
	}
}

func TestMergeUpdateDescriptionOnly(t *testing.T) {
	current := currentTaskBody()
	patch := TaskUpdateIn{
		TaskID:      42,
		Description: strPtr("new description"),
	}
	merged, err := mergeTaskUpdate(current, patch)
	if err != nil {
		t.Fatal(err)
	}

	if merged["description"] != "new description" {
		t.Errorf("description = %v, want new description", merged["description"])
	}
	if merged["priority"] != float64(3) {
		t.Errorf("priority must survive: got %v", merged["priority"])
	}
	if merged["due_date"] != "2026-11-01T12:00:00Z" {
		t.Errorf("due_date must survive: got %v", merged["due_date"])
	}
	if merged["bucket_id"] != float64(42) {
		t.Errorf("bucket_id must survive: got %v", merged["bucket_id"])
	}
	labels, ok := merged["labels"].([]any)
	if !ok || len(labels) != 1 {
		t.Errorf("labels must survive: got %v", merged["labels"])
	}
	if len(merged) != len(current) {
		t.Errorf("merged must have same key count: got %d, want %d", len(merged), len(current))
	}
}

func TestMergeUpdateDoesNotMutateCurrent(t *testing.T) {
	current := currentTaskBody()
	_, _ = mergeTaskUpdate(current, TaskUpdateIn{TaskID: 42, Priority: intPtr(5)})
	if current["priority"] != float64(3) {
		t.Errorf("current mutated: priority = %v", current["priority"])
	}
}

func TestMergeUpdateClearDueDate(t *testing.T) {
	current := currentTaskBody()
	patch := TaskUpdateIn{TaskID: 42, DueDate: strPtr("")}
	merged, err := mergeTaskUpdate(current, patch)
	if err != nil {
		t.Fatal(err)
	}

	raw, _ := json.Marshal(merged)
	var got map[string]any
	json.Unmarshal(raw, &got)

	if v, present := got["due_date"]; present && v != nil {
		t.Errorf("due_date must marshal to null for clear: got %v", v)
	}
	if got["priority"] != float64(3) {
		t.Errorf("priority must survive clear: got %v", got["priority"])
	}
}

func TestMergeUpdateInvalidDate(t *testing.T) {
	current := currentTaskBody()
	_, err := mergeTaskUpdate(current, TaskUpdateIn{TaskID: 42, DueDate: strPtr("tomorrow")})
	if err == nil {
		t.Fatal("invalid date must be rejected")
	}
}

func TestMergeUpdateNoOp(t *testing.T) {
	current := currentTaskBody()
	merged, err := mergeTaskUpdate(current, TaskUpdateIn{TaskID: 42})
	if err != nil {
		t.Fatal(err)
	}

	a, _ := json.Marshal(current)
	b, _ := json.Marshal(merged)
	if string(a) != string(b) {
		t.Errorf("no-op merge changed body:\nwant %s\ngot  %s", a, b)
	}
}

func TestTaskUpdateE2E(t *testing.T) {
	var posted atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/tasks/42":
			json.NewEncoder(w).Encode(currentTaskBody())
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/tasks/42":
			body, _ := io.ReadAll(r.Body)
			posted.Store(string(body))
			json.NewEncoder(w).Encode(currentTaskBody())
		default:
			http.Error(w, "unexpected: "+r.Method+" "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer srv.Close()

	api := client.New(srv.URL, "test-token", 0)
	srvMCP := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.0.0"}, nil)
	NewRegistry(srvMCP, api).RegisterAll()

	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.0"}, nil)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := srvMCP.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "task_update",
		Arguments: map[string]any{
			"task_id":     42,
			"description": "patched description",
		},
	})
	if err != nil {
		t.Fatalf("task_update: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool error: %v", res.Content)
	}

	body := posted.Load().(string)
	if body == "" {
		t.Fatal("no POST body captured")
	}
	var sent map[string]any
	json.Unmarshal([]byte(body), &sent)

	if sent["description"] != "patched description" {
		t.Errorf("POST description = %v, want patched", sent["description"])
	}
	if sent["priority"] != float64(3) {
		t.Errorf("POST must carry full body, priority lost: %v", sent["priority"])
	}
	if _, ok := sent["labels"]; !ok {
		t.Error("POST must carry full body, labels lost")
	}
	if sent["due_date"] != "2026-11-01T12:00:00Z" {
		t.Errorf("POST must carry full body, due_date lost: %v", sent["due_date"])
	}
}

func strPtr(s string) *string { return &s }
func intPtr(i int) *int       { return &i }
