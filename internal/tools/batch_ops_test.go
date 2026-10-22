package tools

import (
	"testing"
)

func TestBatchCreateTasks(t *testing.T) {
	f, session := newCRUDFixture(t)
	out, err := callCRUD(t, session, "batch_create_tasks", map[string]any{
		"project_id": 8,
		"tasks": []any{
			map[string]any{"title": "one", "ref": "r1", "priority": 3, "bucket": "To-Do"},
			map[string]any{"title": "two", "ref": "r2", "labels": []any{"qa"}},
			map[string]any{"title": "three", "blocked_by": []any{"r1"}, "blocks": []any{"r2"}},
		},
		"create_missing_labels": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out["count"] != float64(3) {
		t.Fatalf("count = %v errors=%v", out["count"], out["errors"])
	}
	if len(out["created"].([]any)) != 3 {
		t.Fatalf("created = %v", out["created"])
	}
	if len(f.moves) != 2 {
		t.Fatalf("moves = %v", f.moves)
	}
	if f.moves[0]["task_id"] != float64(301) {
		t.Fatalf("bucket move = %v", f.moves[0])
	}
	if f.moves[1]["position"] == nil {
		t.Fatalf("end-of-column position missing: %v", f.moves[1])
	}
	found := 0
	for _, task := range f.tasks {
		labels, _ := task["labels"].([]any)
		if len(labels) == 1 && labels[0].(map[string]any)["title"] == "qa" {
			found++
		}
	}
	if found != 1 {
		t.Fatalf("label qa not attached, tasks = %v", f.tasks)
	}
}

func TestBatchCreateValidation(t *testing.T) {
	_, session := newCRUDFixture(t)
	if _, err := callCRUD(t, session, "batch_create_tasks", map[string]any{"tasks": []any{}}); err == nil {
		t.Fatal("empty tasks must fail")
	}
	if _, err := callCRUD(t, session, "batch_create_tasks", map[string]any{"project_id": 8, "tasks": []any{map[string]any{}}}); err == nil {
		t.Fatal("missing title must fail")
	}
	if _, err := callCRUD(t, session, "batch_create_tasks", map[string]any{
		"project_id": 8,
		"tasks":      []any{map[string]any{"title": "x", "labels": []any{"ghost"}}},
	}); err == nil {
		t.Fatal("unknown label without create_missing_labels must fail")
	}
}

func TestBatchUpdateTasks(t *testing.T) {
	_, session := newCRUDFixture(t)
	out, err := callCRUD(t, session, "batch_update_tasks", map[string]any{
		"updates": []any{
			map[string]any{"task_id": 301, "title": "a2"},
			map[string]any{"task_id": 303, "priority": 7},
			map[string]any{"task_id": 999},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out["count"] != float64(2) || len(out["errors"].([]any)) != 1 {
		t.Fatalf("out = %v", out)
	}
	first := out["updated"].([]any)[0].(map[string]any)
	if first["title"] != "a2" || first["priority"] != float64(5) {
		t.Fatalf("update wiped fields: %v", first)
	}
}

func TestBatchRelabel(t *testing.T) {
	f, session := newCRUDFixture(t)
	f.tasks[301]["labels"] = []any{map[string]any{"id": float64(1), "title": "qa"}}
	out, err := callCRUD(t, session, "batch_relabel", map[string]any{
		"project_id": 8,
		"task_ids":   []any{301, 303},
		"add_labels": []any{"qa"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out["count"] != float64(2) {
		t.Fatalf("out = %v", out)
	}
	labels := f.tasks[303]["labels"].([]any)
	if len(labels) != 1 || labels[0].(map[string]any)["title"] != "qa" {
		t.Fatalf("303 labels = %v", labels)
	}
	_, err = callCRUD(t, session, "batch_relabel", map[string]any{
		"project_id":    8,
		"task_ids":      []any{301},
		"remove_labels": []any{"qa"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.tasks[301]["labels"].([]any)) != 0 {
		t.Fatalf("301 labels = %v", f.tasks[301]["labels"])
	}
}

func TestBatchCompleteAndMoveByLabel(t *testing.T) {
	f, session := newCRUDFixture(t)
	f.tasks[301]["labels"] = []any{map[string]any{"id": float64(1), "title": "qa"}}
	f.tasks[303]["labels"] = []any{map[string]any{"id": float64(1), "title": "qa"}}
	out, err := callCRUD(t, session, "batch_move_by_label", map[string]any{
		"project_id": 8, "view_id": 40, "bucket_id": 42, "label_filter": "QA",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out["count"] != float64(1) || out["moved"].([]any)[0] != float64(301) {
		t.Fatalf("out = %v", out)
	}
	if len(f.moves) != 2 || f.moves[0]["task_id"] != float64(301) {
		t.Fatalf("moves = %v", f.moves)
	}
	out, err = callCRUD(t, session, "batch_complete_by_label", map[string]any{
		"project_id": 8, "label_filter": "qa",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out["count"] != float64(1) || out["completed"].([]any)[0] != float64(301) {
		t.Fatalf("out = %v", out)
	}
	if f.tasks[301]["done"] != true {
		t.Fatalf("301 done = %v", f.tasks[301]["done"])
	}
}

func TestBatchLabelsCreate(t *testing.T) {
	f, session := newCRUDFixture(t)
	out, err := callCRUD(t, session, "batch_create_labels", map[string]any{
		"labels": []any{
			map[string]any{"title": "qa"},
			map[string]any{"title": "new1", "hex_color": "ff0000"},
			map[string]any{"title": "new1"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out["count"] != float64(1) || len(out["skipped"].([]any)) != 2 {
		t.Fatalf("out = %v", out)
	}
	if f.nextLabel != 2 {
		t.Fatalf("nextLabel = %d", f.nextLabel)
	}
}

func TestBatchAssignBucketsAndReorder(t *testing.T) {
	f, session := newCRUDFixture(t)
	out, err := callCRUD(t, session, "batch_assign_buckets", map[string]any{
		"project_id": 8, "view_id": 40,
		"assignments": []any{
			map[string]any{"task_id": 301, "bucket_id": 42},
			map[string]any{"task_id": 0, "bucket_id": 42},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out["count"] != float64(1) || len(out["errors"].([]any)) != 1 {
		t.Fatalf("out = %v", out)
	}
	if len(f.moves) != 2 {
		t.Fatalf("moves = %v", f.moves)
	}
	if f.moves[0]["task_id"] != float64(301) {
		t.Fatalf("bucket move = %v", f.moves[0])
	}
	if f.moves[1]["position"] == nil {
		t.Fatalf("end-of-column position missing: %v", f.moves[1])
	}
	out, err = callCRUD(t, session, "batch_reorder_tasks", map[string]any{
		"project_id": 8, "view_id": 39,
		"positions": []any{map[string]any{"task_id": 301, "position": 5}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out["count"] != float64(1) {
		t.Fatalf("out = %v", out)
	}
	if len(f.moves) != 3 || f.moves[1]["position"] == nil || f.moves[2]["position"] != float64(5) {
		t.Fatalf("moves = %v", f.moves)
	}
}

func TestBatchLabelToBuckets(t *testing.T) {
	f, session := newCRUDFixture(t)
	f.tasks[301]["labels"] = []any{map[string]any{"id": float64(1), "title": "qa"}}
	f.tasks[302]["labels"] = []any{map[string]any{"id": float64(1), "title": "qa"}}
	out, err := callCRUD(t, session, "batch_label_to_buckets", map[string]any{
		"project_id": 8, "view_id": 40,
		"label_to_bucket_map": map[string]any{"qa": 42},
	})
	if err != nil {
		t.Fatal(err)
	}
	moved := out["moved"].([]any)[0].(map[string]any)
	if out["count"] != float64(1) || moved["task_id"] != float64(301) {
		t.Fatalf("out = %v (302 done excluded)", out)
	}
	if len(f.moves) != 2 {
		t.Fatalf("moves = %v", f.moves)
	}
}
