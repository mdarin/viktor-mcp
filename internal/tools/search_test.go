package tools

import (
	"testing"
)

func TestSearchAll(t *testing.T) {
	f, session := newCRUDFixture(t)
	f.projects[900] = map[string]any{"id": float64(900), "title": "alpha proj", "description": ""}
	out, err := callCRUD(t, session, "search_all", map[string]any{"query": "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	if out["query"] != "alpha" {
		t.Fatalf("out = %v", out)
	}
	counts := out["counts"].(map[string]any)
	if counts["projects"] != float64(1) {
		t.Fatalf("counts = %v", counts)
	}
	row := out["projects"].([]any)[0].(map[string]any)
	if row["title"] != "alpha proj" {
		t.Fatalf("row = %v", row)
	}
	if _, err := callCRUD(t, session, "search_all", map[string]any{"query": "  "}); err == nil {
		t.Fatal("blank query must be rejected")
	}
}

func TestSearchAllTasks(t *testing.T) {
	_, session := newCRUDFixture(t)
	out, err := callCRUD(t, session, "search_all_tasks", map[string]any{"query": "a"})
	if err != nil {
		t.Fatal(err)
	}
	if out["count"] != float64(1) {
		t.Fatalf("out = %v", out)
	}
	out, err = callCRUD(t, session, "search_all_tasks", map[string]any{"query": "c", "project_id": 9})
	if err != nil {
		t.Fatal(err)
	}
	if out["count"] != float64(1) {
		t.Fatalf("out = %v", out)
	}
	row := out["tasks"].([]any)[0].(map[string]any)
	if row["project_id"] != float64(9) {
		t.Fatalf("row = %v", row)
	}
	if _, err := callCRUD(t, session, "search_all_tasks", map[string]any{"query": ""}); err == nil {
		t.Fatal("empty query must be rejected")
	}
}
