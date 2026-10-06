package tools

import (
	"testing"
)

func TestAssignQueueListsItems(t *testing.T) {
	f, session := newCRUDFixture(t)
	f.projects[350] = map[string]any{"id": float64(350), "title": "assignment-queue"}
	f.viewTasks = []any{
		map[string]any{"id": float64(360), "title": "handoff A", "priority": float64(3)},
		map[string]any{"id": float64(361), "title": "handoff B"},
	}
	out, err := callCRUD(t, session, "assign_queue", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if out["count"] != float64(2) || out["project_id"] != float64(350) {
		t.Fatalf("queue = %v", out)
	}
	items := out["items"].([]any)
	first := items[0].(map[string]any)
	if first["task_id"] != float64(360) || first["bucket"] != "todo" || first["priority"] != float64(3) {
		t.Fatalf("item = %v", first)
	}
}

func TestAssignQueueNoProject(t *testing.T) {
	_, session := newCRUDFixture(t)
	if _, err := callCRUD(t, session, "assign_queue", map[string]any{}); err == nil {
		t.Fatal("missing queue project must fail")
	}
}

func TestAssignApplyClaim(t *testing.T) {
	f, session := newCRUDFixture(t)
	f.projects[350] = map[string]any{"id": float64(350), "title": "assignment-queue"}
	f.tasks[360] = map[string]any{"id": float64(360), "title": "handoff A", "project_id": float64(350)}
	out, err := callCRUD(t, session, "assign_apply", map[string]any{"task_id": 360, "destination": "done", "notes": "picked up"})
	if err != nil {
		t.Fatal(err)
	}
	if out["applied"] != true || out["bucket"] != "Done" || out["comment_added"] != true {
		t.Fatalf("apply = %v", out)
	}
	if len(f.comments[360]) != 1 || f.comments[360][0] != "picked up" {
		t.Fatalf("notes = %v", f.comments)
	}
	if len(f.moves) != 1 || f.moves[0]["task_id"] != float64(360) {
		t.Fatalf("moves = %v", f.moves)
	}
}

func TestAssignApplyWrongProject(t *testing.T) {
	f, session := newCRUDFixture(t)
	f.projects[350] = map[string]any{"id": float64(350), "title": "assignment-queue"}
	f.tasks[361] = map[string]any{"id": float64(361), "title": "alien", "project_id": float64(9)}
	if _, err := callCRUD(t, session, "assign_apply", map[string]any{"task_id": 361, "destination": "done"}); err == nil {
		t.Fatal("task outside queue project must fail")
	}
}

func TestAssignApplyUnknownBucket(t *testing.T) {
	f, session := newCRUDFixture(t)
	f.projects[350] = map[string]any{"id": float64(350), "title": "assignment-queue"}
	f.tasks[360] = map[string]any{"id": float64(360), "title": "handoff A", "project_id": float64(350)}
	if _, err := callCRUD(t, session, "assign_apply", map[string]any{"task_id": 360, "destination": "nowhere"}); err == nil {
		t.Fatal("unknown destination must fail")
	}
}

func TestCalAddEvent(t *testing.T) {
	_, session := newCRUDFixture(t)
	out, err := callCRUD(t, session, "cal_add_event", map[string]any{
		"project_id": 8, "title": "sync", "due_date": "2026-10-10T10:00:00Z",
		"start_date": "2026-10-10T09:00:00Z", "label_name": "meeting",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out["label"] != "meeting" || out["task_id"] == nil {
		t.Fatalf("event = %v", out)
	}
}
