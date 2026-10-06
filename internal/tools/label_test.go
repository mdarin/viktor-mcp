package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"viktor/internal/client"
)

type fakeLabels struct {
	labels   []map[string]any
	nextID   int
	attached map[int][]int
	deleted  map[int]bool
}

func (f *fakeLabels) task(id int) map[string]any {
	var labels []any
	for _, lid := range f.attached[id] {
		if f.deleted[lid] {
			continue
		}
		labels = append(labels, map[string]any{"id": float64(lid), "title": fmt.Sprintf("lbl-%d", lid)})
	}
	if labels == nil {
		labels = []any{}
	}
	return map[string]any{"id": float64(id), "project_id": float64(8), "labels": labels}
}

func (f *fakeLabels) handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/labels":
		out := []map[string]any{}
		for _, l := range f.labels {
			if !f.deleted[int(l["id"].(float64))] {
				out = append(out, l)
			}
		}
		json.NewEncoder(w).Encode(out)
	case r.Method == http.MethodPut && r.URL.Path == "/api/v1/labels":
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		f.nextID++
		created := map[string]any{"id": float64(f.nextID), "title": b["title"], "hex_color": b["hex_color"]}
		f.labels = append(f.labels, created)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(created)
	default:
		var delID int
		if _, err := fmt.Sscanf(r.URL.Path, "/api/v1/labels/%d", &delID); err == nil && r.Method == http.MethodDelete {
			f.deleted[delID] = true
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]any{"ok": true})
			return
		}
		var taskID, labelID int
		if _, err := fmt.Sscanf(r.URL.Path, "/api/v1/tasks/%d/labels", &taskID); err == nil {
			switch r.Method {
			case http.MethodPut:
				var b map[string]int
				json.NewDecoder(r.Body).Decode(&b)
				labelID = b["label_id"]
				for _, l := range f.attached[taskID] {
					if l == labelID {
						http.Error(w, `{"code":4003}`, http.StatusConflict)
						return
					}
				}
				f.attached[taskID] = append(f.attached[taskID], labelID)
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]any{"label_id": labelID})
				return
			}
		}
		var tid int
		if _, err := fmt.Sscanf(r.URL.Path, "/api/v1/tasks/%d", &tid); err == nil && r.Method == http.MethodGet {
			json.NewEncoder(w).Encode(f.task(tid))
			return
		}
		http.Error(w, "unexpected: "+r.Method+" "+r.URL.Path, http.StatusNotFound)
	}
}

func newLabelsFixture(t *testing.T) (*fakeLabels, *mcp.ClientSession) {
	t.Helper()
	f := &fakeLabels{nextID: 37, attached: map[int][]int{}, deleted: map[int]bool{}}
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

func callTool(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) (map[string]any, error) {
	t.Helper()
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return nil, err
	}
	if res.IsError {
		return nil, fmt.Errorf("tool error: %v", res.Content)
	}
	out, _ := json.Marshal(res.StructuredContent)
	var m map[string]any
	json.Unmarshal(out, &m)
	return m, nil
}

func TestLabelCreateAndList(t *testing.T) {
	f, session := newLabelsFixture(t)
	out, err := callTool(t, session, "label_create", map[string]any{"title": "bug", "hex_color": "ef4444"})
	if err != nil {
		t.Fatal(err)
	}
	if out["id"] != float64(38) || out["title"] != "bug" {
		t.Fatalf("create out = %v", out)
	}
	out, err = callTool(t, session, "label_list", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if out["count"] != float64(1) {
		t.Fatalf("list = %v", out)
	}
	_ = f
}

func TestLabelDelete(t *testing.T) {
	_, session := newLabelsFixture(t)
	out, err := callTool(t, session, "label_create", map[string]any{"title": "tmp"})
	if err != nil {
		t.Fatal(err)
	}
	id := out["id"].(float64)
	out, err = callTool(t, session, "label_delete", map[string]any{"label_id": id})
	if err != nil {
		t.Fatal(err)
	}
	if out["deleted"] != true {
		t.Fatalf("delete out = %v", out)
	}
	out, err = callTool(t, session, "label_list", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if out["count"] != float64(0) {
		t.Fatalf("list after delete = %v", out)
	}
}

func TestLabelCreateEmptyTitle(t *testing.T) {
	_, session := newLabelsFixture(t)
	if _, err := callTool(t, session, "label_create", map[string]any{"title": ""}); err == nil {
		t.Fatal("empty title must be rejected")
	}
}

func TestTaskAddLabel(t *testing.T) {
	_, session := newLabelsFixture(t)
	created, err := callTool(t, session, "label_create", map[string]any{"title": "review"})
	if err != nil {
		t.Fatal(err)
	}
	lid := created["id"].(float64)
	out, err := callTool(t, session, "task_add_label", map[string]any{"task_id": 268, "label_id": lid})
	if err != nil {
		t.Fatal(err)
	}
	if out["attached"] != true || out["already"] != nil {
		t.Fatalf("add out = %v", out)
	}
}

func TestTaskAddLabelIdempotent(t *testing.T) {
	_, session := newLabelsFixture(t)
	created, _ := callTool(t, session, "label_create", map[string]any{"title": "dup"})
	lid := created["id"].(float64)
	if _, err := callTool(t, session, "task_add_label", map[string]any{"task_id": 268, "label_id": lid}); err != nil {
		t.Fatal(err)
	}
	out, err := callTool(t, session, "task_add_label", map[string]any{"task_id": 268, "label_id": lid})
	if err != nil {
		t.Fatalf("second attach must be idempotent, got error: %v", err)
	}
	if out["already"] != true {
		t.Fatalf("second attach out = %v", out)
	}
}
