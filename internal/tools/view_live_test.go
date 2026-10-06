package tools

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestLiveViewsBuckets(t *testing.T) {
	ctx, api, session := newProjectLiveSession(t)

	title := fmt.Sprintf("vit-live-views-%d", time.Now().Unix())
	var proj map[string]any
	if err := api.CreateProject(ctx, map[string]any{"title": title}, &proj); err != nil {
		t.Skipf("live: create probe project: %v", err)
	}
	pid := int(proj["id"].(float64))
	defer func() {
		time.Sleep(2 * time.Second)
		api.DeleteProject(ctx, pid)
	}()

	time.Sleep(2 * time.Second)
	var task map[string]any
	if err := api.CreateTask(ctx, pid, map[string]any{"title": "v1"}, &task); err != nil {
		t.Fatalf("create task: %v", err)
	}
	tid := int(task["id"].(float64))

	time.Sleep(2 * time.Second)
	call := func(name string, args map[string]any) map[string]any {
		return liveProjectCall(t, ctx, session, name, args)
	}

	out := call("view_list", map[string]any{"project_id": pid})
	views := out["views"].([]any)
	if len(views) == 0 {
		t.Fatal("no views in fresh project")
	}
	var kanbanID float64
	for _, v := range views {
		vm := v.(map[string]any)
		if vm["view_kind"] == "kanban" {
			kanbanID = vm["id"].(float64)
		}
	}
	if kanbanID == 0 {
		t.Fatal("fresh project has no kanban view")
	}
	vid := int(kanbanID)

	time.Sleep(2 * time.Second)
	out = call("kanban_list_buckets", map[string]any{"project_id": pid, "view_id": vid})
	buckets := out["buckets"].([]any)
	if len(buckets) == 0 {
		t.Fatal("no buckets")
	}
	var firstBucket float64
	for _, b := range buckets {
		bm := b.(map[string]any)
		if strings.EqualFold(fmt.Sprint(bm["title"]), "todo") || firstBucket == 0 {
			firstBucket = bm["id"].(float64)
		}
	}
	if firstBucket == 0 {
		t.Fatal("no todo bucket")
	}

	time.Sleep(2 * time.Second)
	out = call("kanban_create_bucket", map[string]any{"project_id": pid, "view_id": vid, "title": "review", "position": 5000})
	if out["title"] != "review" {
		t.Fatalf("create bucket = %v", out)
	}
	reviewID := int(out["id"].(float64))

	time.Sleep(2 * time.Second)
	out = call("kanban_get", map[string]any{"project_id": pid})
	board := out["buckets"].([]any)
	if len(board) < 2 {
		t.Fatalf("board = %v", board)
	}

	time.Sleep(2 * time.Second)
	out = call("view_get_tasks", map[string]any{"project_id": pid, "view_id": vid})
	if out["view_id"] != float64(vid) {
		t.Fatalf("view_get_tasks = %v", out)
	}

	time.Sleep(2 * time.Second)
	out = call("view_set_position", map[string]any{"task_id": tid, "view_id": vid, "position": 12345.0})
	if out["position"] == nil {
		t.Fatalf("view_set_position = %v", out)
	}

	time.Sleep(2 * time.Second)
	out = call("kanban_tasks_by_bucket", map[string]any{"project_id": pid, "view_id": vid})
	grouped := out["by_bucket"].(map[string]any)
	if grouped["To-Do"] == nil && grouped["todo"] == nil {
		t.Fatalf("by_bucket = %v", out)
	}

	time.Sleep(2 * time.Second)
	out = call("kanban_sort_bucket", map[string]any{"project_id": pid, "view_id": vid, "bucket_id": int(firstBucket), "sort_by": "title"})
	t.Logf("sort: %v", out)

	time.Sleep(2 * time.Second)
	out = call("view_update", map[string]any{"project_id": pid, "view_id": vid, "title": "Board"})
	if out["title"] != "Board" {
		t.Fatalf("view_update = %v", out)
	}

	time.Sleep(2 * time.Second)
	out = call("kanban_delete_bucket", map[string]any{"project_id": pid, "view_id": vid, "bucket_id": reviewID})
	if out["deleted"] != true {
		t.Fatalf("delete bucket = %v", out)
	}

	time.Sleep(2 * time.Second)
	out = call("view_create", map[string]any{"project_id": pid, "title": "Extra", "view_kind": "table"})
	extraID := int(out["id"].(float64))
	time.Sleep(2 * time.Second)
	out = call("view_delete", map[string]any{"project_id": pid, "view_id": extraID})
	if out["deleted"] != true {
		t.Fatalf("view_delete = %v", out)
	}
}
