package tools

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestLiveBatchOps(t *testing.T) {
	ctx, _, session := newProjectLiveSession(t)
	call := func(name string, args map[string]any) map[string]any {
		return liveProjectCall(t, ctx, session, name, args)
	}

	proj := call("project_create_from_template", map[string]any{"title": fmt.Sprintf("vit-batch-%d", time.Now().Unix()), "mode": "dev"})
	pid := int(proj["project_id"].(float64))

	time.Sleep(2 * time.Second)
	board := call("kanban_get", map[string]any{"project_id": pid})
	todo, doing := 0, 0
	for _, b := range board["buckets"].([]any) {
		m := b.(map[string]any)
		switch strings.ToLower(m["title"].(string)) {
		case "todo":
			todo = int(m["id"].(float64))
		case "in-progress", "doing":
			doing = int(m["id"].(float64))
		}
	}
	if todo == 0 || doing == 0 {
		t.Fatalf("buckets = %v", board["buckets"])
	}

	time.Sleep(2 * time.Second)
	out := call("batch_create_tasks", map[string]any{
		"project_id": pid,
		"tasks": []any{
			map[string]any{"title": "b1", "ref": "r1", "labels": []any{"vit-batch"}, "bucket": "todo"},
			map[string]any{"title": "b2", "ref": "r2", "blocked_by": []any{"r1"}},
		},
		"create_missing_labels": true,
	})
	if out["count"] != float64(2) || len(out["errors"].([]any)) != 0 || len(out["relation_error"].([]any)) != 0 {
		t.Fatalf("create = %v", out)
	}
	created := out["created"].([]any)
	b1 := int(created[0].(map[string]any)["id"].(float64))
	b2 := int(created[1].(map[string]any)["id"].(float64))

	time.Sleep(2 * time.Second)
	out = call("batch_update_tasks", map[string]any{
		"updates": []any{map[string]any{"task_id": b2, "title": "b2-renamed", "priority": 5}},
	})
	if out["count"] != float64(1) || len(out["errors"].([]any)) != 0 {
		t.Fatalf("update = %v", out)
	}
	row := out["updated"].([]any)[0].(map[string]any)
	if row["title"] != "b2-renamed" || row["priority"] != float64(5) {
		t.Fatalf("row = %v", row)
	}

	time.Sleep(2 * time.Second)
	out = call("batch_move_by_label", map[string]any{
		"project_id": pid, "view_id": board["view_id"], "bucket_id": doing, "label_filter": "vit-batch",
	})
	if out["count"] != float64(1) || out["moved"].([]any)[0] != float64(b1) {
		t.Fatalf("move_by_label = %v", out)
	}

	time.Sleep(2 * time.Second)
	out = call("batch_relabel", map[string]any{
		"project_id": pid, "task_ids": []any{b1}, "remove_labels": []any{"vit-batch"},
	})
	if out["count"] != float64(1) || len(out["errors"].([]any)) != 0 {
		t.Fatalf("relabel = %v", out)
	}

	time.Sleep(2 * time.Second)
	out = call("batch_create_labels", map[string]any{
		"labels": []any{map[string]any{"title": "vit-batch"}, map[string]any{"title": "vit-batch-2"}},
	})
	if out["count"] != float64(1) || len(out["skipped"].([]any)) != 1 {
		t.Fatalf("labels = %v", out)
	}

	time.Sleep(2 * time.Second)
	call("batch_complete_by_label", map[string]any{"project_id": pid, "label_filter": "vit-batch-2-does-not-exist"})

	t.Cleanup(func() {
		time.Sleep(2 * time.Second)
		session.CallTool(ctx, &mcp.CallToolParams{Name: "project_delete", Arguments: map[string]any{"project_id": pid, "confirm": true}})
	})
	_ = b2
}
