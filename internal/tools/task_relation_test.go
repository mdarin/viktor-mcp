package tools

import "testing"

func TestTaskCreateRelationShape(t *testing.T) {
	f, session := newCRUDFixture(t)
	out, err := callCRUD(t, session, "task_create_relation", map[string]any{
		"task_id": 301, "relation_kind": "subtask", "other_task_id": 303,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out["created"] != true || out["mirror_kind"] != "parenttask" {
		t.Fatalf("create_relation out = %v", out)
	}
	if len(f.relations[301]["subtask"]) != 1 || f.relations[301]["subtask"][0] != 303 {
		t.Fatalf("stored = %v", f.relations)
	}
}

func TestTaskCreateRelationValidation(t *testing.T) {
	_, session := newCRUDFixture(t)
	if _, err := callCRUD(t, session, "task_create_relation", map[string]any{
		"task_id": 301, "relation_kind": "bogus", "other_task_id": 303,
	}); err == nil {
		t.Fatal("unknown kind must be rejected client-side")
	}
	if _, err := callCRUD(t, session, "task_create_relation", map[string]any{
		"task_id": 301, "relation_kind": "related", "other_task_id": 301,
	}); err == nil {
		t.Fatal("self relation must be rejected client-side")
	}
	if _, err := callCRUD(t, session, "task_create_relation", map[string]any{
		"task_id": 301, "other_task_id": 303,
	}); err == nil {
		t.Fatal("missing kind must be rejected")
	}
}

func TestTaskListRelationsEmpty(t *testing.T) {
	_, session := newCRUDFixture(t)
	out, err := callCRUD(t, session, "task_list_relations", map[string]any{"task_id": 302})
	if err != nil {
		t.Fatal(err)
	}
	if out["count"] != float64(0) {
		t.Fatalf("empty relations: %v", out)
	}
}

func TestTaskListRelationsAndTaskGet(t *testing.T) {
	_, session := newCRUDFixture(t)
	if _, err := callCRUD(t, session, "task_create_relation", map[string]any{
		"task_id": 301, "relation_kind": "subtask", "other_task_id": 303,
	}); err != nil {
		t.Fatal(err)
	}
	out, err := callCRUD(t, session, "task_list_relations", map[string]any{"task_id": 301})
	if err != nil {
		t.Fatal(err)
	}
	if out["count"] != float64(1) {
		t.Fatalf("relations: %v", out)
	}
	rels := out["relations"].(map[string]any)
	list := rels["subtask"].([]any)
	first := list[0].(map[string]any)
	if first["id"] != float64(303) || first["title"] != "c" {
		t.Fatalf("subtask list = %v", first)
	}
	gout, err := callCRUD(t, session, "task_get", map[string]any{"task_id": 301})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := gout["related_tasks"]; !ok {
		t.Fatalf("task_get must surface related_tasks when relations exist: %v", gout)
	}
}
