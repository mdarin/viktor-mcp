package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"viktor/internal/client"
	"viktor/internal/config"
	"viktor/internal/memory"
)

type fakeKanban struct {
	projects      map[string]int
	nextProjectID int
	views         []map[string]any
	buckets       map[int][]map[string]any
	nextBucketID  int
	nextViewID    int
	// doneTasks answers GET /projects/9/tasks?filter=done = true for
	// the board_audit hygiene mode (card #855).
	doneTasks []map[string]any
}

// testMemoryCfgs, when set, is installed via SetMemoryConfigs by the
// next newKanbanFixture call (used by the hygiene audit tests).
var testMemoryCfgs map[string]memory.Config

func newKanbanFixture(t *testing.T, boards ...*config.BoardSection) (*fakeKanban, *mcp.ClientSession) {
	t.Helper()
	f := &fakeKanban{
		projects:      map[string]int{},
		nextProjectID: 8,
		nextViewID:    39,
		nextBucketID:  60,
		buckets:       map[int][]map[string]any{},
	}
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(srv.Close)

	api := client.New(srv.URL, "test-token", 0)
	srvMCP := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.0.0"}, nil)
	reg := NewRegistry(srvMCP, api)
	for _, b := range boards {
		reg.SetBoard(b)
	}
	if testMemoryCfgs != nil {
		reg.SetMemoryConfigs(testMemoryCfgs)
	}
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
	return f, session
}

func (f *fakeKanban) addDefaultBuckets(viewID int) {
	f.buckets[viewID] = []map[string]any{
		{"id": float64(1), "title": "Backlog", "position": float64(0)},
		{"id": float64(2), "title": "Done", "position": float64(1000)},
	}
}

var bucketMoveRe = regexp.MustCompile(`^/api/v2/projects/9/views/(\d+)/buckets/(\d+)/tasks$`)

// moveTaskInFixture relocates a task map between buckets of a view.
func (f *fakeKanban) moveTaskInFixture(viewID, bucketID, taskID int) {
	buckets := f.buckets[viewID]
	var moved map[string]any
	kept := buckets[:0]
	for _, b := range buckets {
		tasks, _ := b["tasks"].([]any)
		out := tasks[:0]
		for _, t := range tasks {
			tm, ok := t.(map[string]any)
			if ok && asInt(tm["id"]) == taskID {
				moved = tm
				continue
			}
			out = append(out, t)
		}
		b["tasks"] = out
		if asInt(b["id"]) == bucketID {
			kept = append(kept, b)
			continue
		}
		kept = append(kept, b)
	}
	if moved == nil {
		moved = map[string]any{"id": float64(taskID), "title": "moved", "position": float64(0)}
	}
	for _, b := range kept {
		if asInt(b["id"]) == bucketID {
			b["tasks"] = append(b["tasks"].([]any), moved)
		}
	}
	f.buckets[viewID] = kept
}

// setTaskPositionInFixture updates the position of a task wherever it sits.
func (f *fakeKanban) setTaskPositionInFixture(taskID, position int) {
	for _, buckets := range f.buckets {
		for _, b := range buckets {
			tasks, _ := b["tasks"].([]any)
			for _, t := range tasks {
				if tm, ok := t.(map[string]any); ok && asInt(tm["id"]) == taskID {
					tm["position"] = float64(position)
				}
			}
		}
	}
}

func (f *fakeKanban) handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	body := func() map[string]any {
		var m map[string]any
		json.NewDecoder(r.Body).Decode(&m)
		return m
	}
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/v2/projects":
		in := body()
		title, _ := in["title"].(string)
		f.nextProjectID++
		f.projects[title] = f.nextProjectID
		json.NewEncoder(w).Encode(map[string]any{"id": float64(f.nextProjectID), "title": title})
	case r.Method == http.MethodGet && r.URL.Path == "/api/v2/projects/9/views":
		if p := r.URL.Query().Get("page"); p != "" && p != "1" {
			json.NewEncoder(w).Encode([]any{})
			return
		}
		json.NewEncoder(w).Encode(f.views)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v2/projects/9/views":
		in := body()
		f.nextViewID++
		in["id"] = float64(f.nextViewID)
		f.views = append(f.views, in)
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(in)
	case r.Method == http.MethodPut && r.URL.Path == "/api/v2/projects/9/views/40":
		in := body()
		for _, v := range f.views {
			if asInt(v["id"]) == 40 {
				for k, val := range in {
					v[k] = val
				}
			}
		}
		json.NewEncoder(w).Encode(in)
	case r.Method == http.MethodGet && r.URL.Path == "/api/v2/projects/9/tasks":
		if strings.Contains(r.URL.Query().Get("filter"), "done = true") {
			if p := r.URL.Query().Get("page"); p != "" && p != "1" {
				json.NewEncoder(w).Encode([]any{})
				return
			}
			json.NewEncoder(w).Encode(f.doneTasks)
			return
		}
		http.Error(w, "unexpected task list filter", http.StatusBadRequest)
	case r.Method == http.MethodGet && r.URL.Path == "/api/v2/projects/9/views/40/buckets/tasks":
		if p := r.URL.Query().Get("page"); p != "" && p != "1" {
			json.NewEncoder(w).Encode([]any{})
			return
		}
		json.NewEncoder(w).Encode(f.buckets[40])
	case r.Method == http.MethodPut && bucketMoveRe.FindStringSubmatch(r.URL.Path) != nil:
		m := bucketMoveRe.FindStringSubmatch(r.URL.Path)
		viewID, _ := strconv.Atoi(m[1])
		bucketID, _ := strconv.Atoi(m[2])
		taskID := asInt(body()["task_id"])
		f.moveTaskInFixture(viewID, bucketID, taskID)
		json.NewEncoder(w).Encode(map[string]any{"task_id": float64(taskID), "bucket_id": float64(bucketID), "project_view_id": float64(viewID)})
	case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/api/v2/tasks/") && strings.HasSuffix(r.URL.Path, "/position"):
		in := body()
		idStr := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v2/tasks/"), "/position")
		taskID, _ := strconv.Atoi(idStr)
		f.setTaskPositionInFixture(taskID, asInt(in["position"]))
		json.NewEncoder(w).Encode(map[string]any{"task_id": float64(taskID), "position": in["position"]})
	case r.Method == http.MethodGet && len(r.URL.Path) > 20 &&
		r.URL.Path == "/api/v2/projects/9/views/40/buckets":
		if p := r.URL.Query().Get("page"); p != "" && p != "1" {
			json.NewEncoder(w).Encode([]any{})
			return
		}
		json.NewEncoder(w).Encode(f.buckets[40])
	case r.Method == http.MethodPost && r.URL.Path == "/api/v2/projects/9/views/40/buckets":
		in := body()
		f.nextBucketID++
		in["id"] = float64(f.nextBucketID)
		f.buckets[40] = append(f.buckets[40], in)
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(in)
	case r.Method == http.MethodDelete && r.URL.Path == "/api/v2/projects/9/views/40/buckets/1":
		kept := f.buckets[40][:0]
		for _, b := range f.buckets[40] {
			if asInt(b["id"]) != 1 {
				kept = append(kept, b)
			}
		}
		f.buckets[40] = kept
		w.WriteHeader(http.StatusOK)
	default:
		http.Error(w, "unexpected: "+r.Method+" "+r.URL.Path, http.StatusNotFound)
	}
}

func boolPtr(b bool) *bool { return &b }

func callSetup(t *testing.T, session *mcp.ClientSession, args map[string]any) map[string]any {
	t.Helper()
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "kanban_setup",
		Arguments: args,
	})
	if err != nil {
		t.Fatalf("kanban_setup: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool error: %v", res.Content)
	}
	out, _ := json.Marshal(res.StructuredContent)
	var m map[string]any
	json.Unmarshal(out, &m)
	return m
}

func TestKanbanSetupNewProjectFreshBoard(t *testing.T) {
	f, session := newKanbanFixture(t)
	out := callSetup(t, session, map[string]any{
		"project_title": "fresh",
		"columns":       []string{"todo", "in-progress", "done"},
	})

	if out["project_created"] != true {
		t.Errorf("project_created = %v", out["project_created"])
	}
	if out["buckets_created"] != float64(3) {
		t.Errorf("buckets_created = %v, want 3", out["buckets_created"])
	}
	if out["buckets_deleted"] != float64(0) {
		t.Errorf("fresh board deleted = %v, want 0", out["buckets_deleted"])
	}
	if out["done_bucket_id"] == float64(0) {
		t.Error("done_bucket_id must be configured")
	}
	if f.views[0]["view_kind"] != "kanban" {
		t.Errorf("view kind = %v", f.views[0]["view_kind"])
	}
}

func TestKanbanSetupIdempotent(t *testing.T) {
	_, session := newKanbanFixture(t)
	first := callSetup(t, session, map[string]any{
		"project_title": "board",
		"columns":       []string{"todo", "in-progress", "done"},
	})
	second := callSetup(t, session, map[string]any{
		"project_id": first["project_id"],
		"columns":    []string{"todo", "in-progress", "done"},
	})

	if second["buckets_created"] != float64(0) {
		t.Errorf("second call created %v, want 0", second["buckets_created"])
	}
	if second["buckets_deleted"] != float64(0) {
		t.Errorf("second call deleted %v, want 0", second["buckets_deleted"])
	}
	if second["view_existed"] != true {
		t.Error("second call must reuse view")
	}
	if first["view_id"] != second["view_id"] {
		t.Errorf("view drifted: %v → %v", first["view_id"], second["view_id"])
	}
	buckets, _ := second["buckets"].([]any)
	if len(buckets) != 3 {
		t.Errorf("second call buckets = %d, want 3 (no duplicates)", len(buckets))
	}
}

func TestKanbanSetupDeletesForeignColumns(t *testing.T) {
	f, session := newKanbanFixture(t)
	first := callSetup(t, session, map[string]any{
		"project_title": "clean",
		"columns":       []string{"todo", "in-progress", "done"},
	})
	callSetup(t, session, map[string]any{
		"project_id": first["project_id"],
		"columns":    []string{"todo", "in-progress", "done", "review"},
	})

	titles := map[string]bool{}
	for _, b := range f.buckets[40] {
		titles[b["title"].(string)] = true
	}
	if titles["Backlog"] {
		t.Error("foreign column must be deleted")
	}
	if !titles["review"] {
		t.Error("requested column missing")
	}
}

func TestKanbanSetupKeepForeignColumns(t *testing.T) {
	f, session := newKanbanFixture(t)
	first := callSetup(t, session, map[string]any{
		"project_title":          "keep",
		"columns":                []string{"todo", "in-progress", "done"},
		"delete_default_buckets": boolPtr(false),
	})
	f.nextBucketID = 70
	callSetup(t, session, map[string]any{
		"project_id":             first["project_id"],
		"columns":                []string{"todo", "in-progress", "done", "extra"},
		"delete_default_buckets": boolPtr(false),
	})
	if len(f.buckets[40]) != 4 {
		t.Errorf("foreign column deleted despite flag: %d buckets", len(f.buckets[40]))
	}
}

func TestKanbanSetupDoneBucketFromExistingTitle(t *testing.T) {
	f, session := newKanbanFixture(t)
	f.views = []map[string]any{{
		"id":             float64(40),
		"title":          "Kanban",
		"view_kind":      "kanban",
		"done_bucket_id": float64(0),
	}}
	f.addDefaultBuckets(40)
	out := callSetup(t, session, map[string]any{
		"project_id": 9,
		"columns":    []string{"todo", "in-progress", "done"},
	})

	if out["done_bucket_id"] != float64(2) {
		t.Errorf("existing 'Done' bucket id=2 must be reused as done_bucket_id, got %v", out["done_bucket_id"])
	}
	if f.views[0]["done_bucket_id"] != float64(2) {
		t.Errorf("view.done_bucket_id = %v, want 2", f.views[0]["done_bucket_id"])
	}
}

func TestKanbanSetupRequiresTarget(t *testing.T) {
	_, session := newKanbanFixture(t)
	if _, err := callShare(t, session, map[string]any{"columns": []string{"todo"}}); err == nil {
		t.Log("kanban_setup requires project_id or project_title")
	}
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "kanban_setup",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("kanban_setup without project must fail")
	}
}
