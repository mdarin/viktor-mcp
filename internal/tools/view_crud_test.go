package tools

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func viewSession(t *testing.T) (*fakeCRUD, *mcp.ClientSession) {
	f, session := newCRUDFixture(t)
	return f, session
}

func TestViewListCreate(t *testing.T) {
	_, session := viewSession(t)
	out, err := callCRUD(t, session, "view_list", map[string]any{"project_id": 8})
	if err != nil || out["count"] != float64(2) {
		t.Fatalf("view_list = %v err=%v", out, err)
	}
	out, err = callCRUD(t, session, "view_create", map[string]any{"project_id": 8, "title": "Board", "view_kind": "kanban"})
	if err != nil || out["title"] != "Board" {
		t.Fatalf("view_create = %v err=%v", out, err)
	}
	if _, err := callCRUD(t, session, "view_create", map[string]any{"project_id": 8, "title": "x"}); err == nil {
		t.Fatal("missing view_kind must be rejected")
	}
}

func TestViewGetTasksKanban(t *testing.T) {
	_, session := viewSession(t)
	out, err := callCRUD(t, session, "view_get_tasks", map[string]any{"project_id": 8, "view_id": 40})
	if err != nil {
		t.Fatal(err)
	}
	if out["view_id"] != float64(40) {
		t.Fatalf("kanban board = %v", out)
	}
	buckets := out["buckets"].([]any)
	if len(buckets) != 2 || buckets[0].(map[string]any)["title"] != "To-Do" {
		t.Fatalf("buckets = %v", buckets)
	}
}

func TestKanbanTasksByBucket(t *testing.T) {
	_, session := viewSession(t)
	out, err := callCRUD(t, session, "kanban_tasks_by_bucket", map[string]any{"project_id": 8, "view_id": 40})
	if err != nil {
		t.Fatal(err)
	}
	grouped := out["by_bucket"].(map[string]any)
	if grouped["To-Do"] == nil {
		t.Fatalf("by_bucket = %v", grouped)
	}
}

func TestViewGetTasksList(t *testing.T) {
	_, session := viewSession(t)
	out, err := callCRUD(t, session, "view_get_tasks", map[string]any{"project_id": 8, "view_id": 99})
	if err != nil {
		t.Fatal(err)
	}
	if out["count"] == nil || out["tasks"] == nil {
		t.Fatalf("list view tasks = %v", out)
	}
}

func TestViewSetPosition(t *testing.T) {
	_, session := viewSession(t)
	out, err := callCRUD(t, session, "view_set_position", map[string]any{"task_id": 301, "view_id": 29, "position": 55.5})
	if err != nil || out["position"] != 55.5 {
		t.Fatalf("view_set_position = %v err=%v", out, err)
	}
}

func TestKanbanCreateDeleteBucket(t *testing.T) {
	_, session := viewSession(t)
	out, err := callCRUD(t, session, "kanban_create_bucket", map[string]any{"project_id": 8, "view_id": 40, "title": "review", "position": 3000, "limit": 5})
	if err != nil || out["title"] != "review" || out["limit"] != float64(5) {
		t.Fatalf("create bucket = %v err=%v", out, err)
	}
	out, err = callCRUD(t, session, "kanban_delete_bucket", map[string]any{"project_id": 8, "view_id": 40, "bucket_id": 41})
	if err != nil || out["deleted"] != true {
		t.Fatalf("delete bucket = %v err=%v", out, err)
	}
	if _, err := callCRUD(t, session, "kanban_create_bucket", map[string]any{"project_id": 8, "view_id": 40}); err == nil {
		t.Fatal("empty title must be rejected")
	}
}

func TestKanbanSortBucket(t *testing.T) {
	f, session := viewSession(t)
	f.viewTasks = []any{
		map[string]any{"id": float64(301), "title": "b", "due_date": "2026-01-05"},
		map[string]any{"id": float64(302), "title": "a", "due_date": "2026-01-01"},
	}
	out, err := callCRUD(t, session, "kanban_sort_bucket", map[string]any{"project_id": 8, "view_id": 40, "bucket_id": 41, "sort_by": "due_date"})
	if err != nil {
		t.Fatal(err)
	}
	if out["tasks_moved"] != float64(2) {
		t.Fatalf("sort = %v", out)
	}
	if len(f.moves) != 2 {
		t.Fatalf("moves = %v", f.moves)
	}
	first := f.moves[0]
	if first["task_id"] != float64(302) || first["position"] != float64(1000) {
		t.Fatalf("first move = %v", first)
	}
	if _, err := callCRUD(t, session, "kanban_sort_bucket", map[string]any{"project_id": 8, "view_id": 40, "bucket_id": 41}); err == nil {
		t.Fatal("missing sort_by must be rejected")
	}
}
