package tools

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestLiveToday(t *testing.T) {
	ctx, _, session := newProjectLiveSession(t)
	call := func(name string, args map[string]any) map[string]any {
		return liveProjectCall(t, ctx, session, name, args)
	}

	t.Setenv("VIKTOR_SETTINGS", filepath.Join(t.TempDir(), "settings.json"))

	proj := call("project_create_from_template", map[string]any{"title": fmt.Sprintf("vit-today-%d", time.Now().Unix()), "mode": "dev"})
	pid := int(proj["project_id"].(float64))
	t.Cleanup(func() {
		time.Sleep(2 * time.Second)
		session.CallTool(ctx, &mcp.CallToolParams{Name: "project_delete", Arguments: map[string]any{"project_id": pid, "confirm": true}})
	})

	time.Sleep(2 * time.Second)
	task := call("task_create", map[string]any{
		"project_id": pid, "title": "snooze me", "priority": 5,
		"due_date": "2026-10-03T09:00:00Z",
	})
	tid := int(task["id"].(float64))

	time.Sleep(2 * time.Second)
	out := call("today_actions", map[string]any{})
	found := false
	for _, c := range out["intents"].([]any) {
		for _, it := range c.(map[string]any)["items"].([]any) {
			if int(it.(map[string]any)["task_id"].(float64)) == tid {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("task not ranked: %v", out)
	}

	time.Sleep(2 * time.Second)
	out = call("today_snooze", map[string]any{
		"task_id": tid, "reason": "dread", "until": "2026-10-10T09:00:00Z", "comment": "not today",
	})
	if out["deferred_count"] != float64(1) || out["dread_count"] != float64(1) {
		t.Fatalf("snooze = %v", out)
	}

	time.Sleep(2 * time.Second)
	out = call("today_reckoning", map[string]any{"threshold": 1})
	found = false
	for _, row := range out["reckoning"].([]any) {
		if int(row.(map[string]any)["task_id"].(float64)) == tid {
			found = true
		}
	}
	if !found {
		t.Fatalf("reckoning = %v", out)
	}

	time.Sleep(2 * time.Second)
	out = call("today_set_weights", map[string]any{"weights": map[string]any{"overdue": 12.0}})
	if out["saved"] != true {
		t.Fatalf("set_weights = %v", out)
	}
	out = call("today_get_weights", map[string]any{})
	w := out["weights"].(map[string]any)
	if w["overdue"] != float64(12) || w["priority"] != float64(8) {
		t.Fatalf("weights = %v", w)
	}
}
