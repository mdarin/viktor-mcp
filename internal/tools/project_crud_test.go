package tools

import (
	"testing"
)

func TestProjectList(t *testing.T) {
	f, session := newCRUDFixture(t)
	f.projects = map[int]map[string]any{
		8: {"id": float64(8), "title": "p8", "description": "d"},
		9: {"id": float64(9), "title": "p9"},
	}
	out, err := callCRUD(t, session, "project_list", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if out["count"] != float64(2) {
		t.Fatalf("list = %v", out)
	}
}

func TestProjectGetCreateUpdate(t *testing.T) {
	f, session := newCRUDFixture(t)
	f.projects = map[int]map[string]any{
		8: {"id": float64(8), "title": "old", "description": "keep"},
	}
	out, err := callCRUD(t, session, "project_get", map[string]any{"project_id": 8})
	if err != nil || out["title"] != "old" {
		t.Fatalf("get = %v err=%v", out, err)
	}
	out, err = callCRUD(t, session, "project_create", map[string]any{"title": "new", "description": "d"})
	if err != nil || out["title"] != "new" {
		t.Fatalf("create = %v err=%v", out, err)
	}
	if _, ok := f.projects[901]; !ok {
		t.Fatalf("created project not stored: %v", f.projects)
	}
	out, err = callCRUD(t, session, "project_update", map[string]any{"project_id": 8, "title": "renamed"})
	if err != nil || out["title"] != "renamed" {
		t.Fatalf("update = %v err=%v", out, err)
	}
	if f.projects[8]["description"] != "keep" {
		t.Fatalf("update wiped description: %v", f.projects[8])
	}
	if _, err := callCRUD(t, session, "project_update", map[string]any{"project_id": 8}); err == nil {
		t.Fatal("empty update must be rejected")
	}
	if _, err := callCRUD(t, session, "project_create", map[string]any{"title": ""}); err == nil {
		t.Fatal("empty title must be rejected")
	}
}

func TestProjectDeleteConfirm(t *testing.T) {
	f, session := newCRUDFixture(t)
	f.projects = map[int]map[string]any{8: {"id": float64(8), "title": "p"}}
	if _, err := callCRUD(t, session, "project_delete", map[string]any{"project_id": 8}); err == nil {
		t.Fatal("delete without confirm must be refused")
	}
	if len(f.projects) != 1 {
		t.Fatal("refused delete must not remove")
	}
	out, err := callCRUD(t, session, "project_delete", map[string]any{"project_id": 8, "confirm": true})
	if err != nil || out["deleted"] != true {
		t.Fatalf("delete = %v err=%v", out, err)
	}
	if len(f.projects) != 0 {
		t.Fatal("confirmed delete must remove")
	}
}

func TestProjectTemplateAndSetup(t *testing.T) {
	// route views/buckets on project 900 (created by template fixture)
	_, session := newCRUDFixture(t)
	out, err := callCRUD(t, session, "project_create_from_template", map[string]any{"title": "tpl", "mode": "dev"})
	if err != nil {
		t.Fatal(err)
	}
	if out["mode"] != "dev" {
		t.Fatalf("template = %v", out)
	}
	buckets := out["buckets"].([]any)
	if len(buckets) != 5 {
		t.Fatalf("dev template buckets = %d", len(buckets))
	}
	if _, err = callCRUD(t, session, "project_create_from_template", map[string]any{"title": "tpl2", "mode": "bogus"}); err == nil {
		t.Fatal("unknown mode must be rejected")
	}
}

func TestProjectAnalyze(t *testing.T) {
	f, session := newCRUDFixture(t)
	f.tasks[301]["labels"] = []any{map[string]any{"id": float64(1), "title": "qa"}}
	f.tasks[303]["project_id"] = float64(8)
	out, err := callCRUD(t, session, "project_analyze", map[string]any{"project_id": 8})
	if err != nil {
		t.Fatal(err)
	}
	if out["open"] == nil || out["by_label"] == nil {
		t.Fatalf("analyze = %v", out)
	}
	lbl := out["by_label"].(map[string]any)
	if lbl["qa"] != float64(1) {
		t.Fatalf("by_label = %v", lbl)
	}
}

func TestProjectListAll(t *testing.T) {
	_, session := newCRUDFixture(t)
	out, err := callCRUD(t, session, "project_list_all", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	instances := out["instances"].([]any)
	if len(instances) == 0 {
		t.Fatal("expected at least one instance")
	}
	first := instances[0].(map[string]any)
	if first["instance"] == "" {
		t.Fatalf("instance row = %v", first)
	}
}
