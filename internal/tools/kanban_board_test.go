package tools

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"viktor/internal/config"
)

func callToolMust(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) map[string]any {
	t.Helper()
	m, err := callTool(t, session, name, args)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return m
}

func boardFixture(t *testing.T) (*fakeKanban, *mcp.ClientSession) {
	t.Helper()
	f, session := newKanbanFixture(t)
	f.views = []map[string]any{{
		"id":             float64(40),
		"title":          "Kanban",
		"view_kind":      "kanban",
		"done_bucket_id": float64(2),
	}}
	f.buckets[40] = []map[string]any{
		{"id": float64(1), "title": "todo", "position": float64(0), "tasks": []any{
			map[string]any{"id": float64(51), "title": "a", "position": float64(5)},
			map[string]any{"id": float64(52), "title": "b", "position": float64(9)},
		}},
		{"id": float64(2), "title": "in-progress", "position": float64(500), "tasks": []any{}},
		{"id": float64(3), "title": "done", "position": float64(1000), "tasks": []any{
			map[string]any{"id": float64(50), "title": "c", "position": float64(10)},
		}},
	}
	return f, session
}

func TestBoardAuditOK(t *testing.T) {
	_, session := boardFixture(t)
	out := callToolMust(t, session, "board_audit", map[string]any{"project_id": 9})
	if out["ok"] != true {
		t.Errorf("ok = %v, want true (out=%v)", out["ok"], out)
	}
	if len(out["missing"].([]any)) != 0 || len(out["extra"].([]any)) != 0 {
		t.Errorf("unexpected drift: missing=%v extra=%v", out["missing"], out["extra"])
	}
	if out["done_bucket_id"] != float64(2) {
		t.Errorf("done_bucket_id = %v, want 2", out["done_bucket_id"])
	}
	buckets, _ := out["buckets"].([]any)
	if len(buckets) != 3 {
		t.Fatalf("buckets = %d, want 3", len(buckets))
	}
	first := buckets[0].(map[string]any)
	if first["count"] != float64(2) {
		t.Errorf("todo count = %v, want 2", first["count"])
	}
}

func TestBoardAuditDrift(t *testing.T) {
	f, session := boardFixture(t)
	f.buckets[40] = append(f.buckets[40], map[string]any{"id": float64(4), "title": "review", "position": float64(2000), "tasks": []any{}})
	out := callToolMust(t, session, "board_audit", map[string]any{"project_id": 9})
	if out["ok"] != false {
		t.Errorf("ok = %v, want false", out["ok"])
	}
	if extra, _ := out["extra"].([]any); len(extra) != 1 || extra[0] != "review" {
		t.Errorf("extra = %v, want [review]", out["extra"])
	}
	// Explicit profile argument wins over the configured/default one.
	out = callToolMust(t, session, "board_audit", map[string]any{
		"project_id": 9,
		"profile":    []string{"todo", "blocked"},
	})
	if missing, _ := out["missing"].([]any); len(missing) != 1 || missing[0] != "blocked" {
		t.Errorf("missing = %v, want [blocked]", out["missing"])
	}
	if extra, _ := out["extra"].([]any); len(extra) != 3 {
		t.Errorf("extra = %v, want 3 columns", out["extra"])
	}
}

func TestKanbanSetupUsesBoardProfile(t *testing.T) {
	_, session := newKanbanFixture(t, &config.BoardSection{Profile: []string{"backlog", "doing", "done"}})
	out := callSetup(t, session, map[string]any{"project_title": "profiled"})
	buckets, _ := out["buckets"].([]any)
	if len(buckets) != 3 {
		t.Fatalf("buckets = %d, want 3", len(buckets))
	}
	titles := map[string]bool{}
	for _, b := range buckets {
		titles[b.(map[string]any)["title"].(string)] = true
	}
	if !titles["backlog"] || !titles["doing"] {
		t.Errorf("profile columns missing: %v", titles)
	}
}

func TestTaskSetPositionPlacesAtEnd(t *testing.T) {
	f, session := boardFixture(t)
	out := callToolMust(t, session, "task_set_position", map[string]any{
		"task_id":    51,
		"project_id": 9,
		"bucket":     "done",
	})
	if out["bucket_id"] != float64(3) {
		t.Fatalf("bucket_id = %v, want 3", out["bucket_id"])
	}
	if out["position"] != float64(11) {
		t.Errorf("position = %v, want 11 (max done position 10 + 1)", out["position"])
	}
	var doneTasks []any
	for _, b := range f.buckets[40] {
		if asInt(b["id"]) == 3 {
			doneTasks = b["tasks"].([]any)
		}
	}
	if len(doneTasks) != 2 {
		t.Errorf("done bucket tasks = %d, want 2", len(doneTasks))
	}
	for _, tsk := range doneTasks {
		if asInt(tsk.(map[string]any)["id"]) == 51 && asInt(tsk.(map[string]any)["position"]) != 11 {
			t.Errorf("task 51 position = %v, want 11", tsk.(map[string]any)["position"])
		}
	}
}
