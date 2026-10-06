package tools

import (
	"fmt"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestLiveAssignQueue(t *testing.T) {
	ctx, _, session := newProjectLiveSession(t)
	call := func(name string, args map[string]any) map[string]any {
		return liveProjectCall(t, ctx, session, name, args)
	}

	out := call("assign_queue", map[string]any{})
	qpid := int(out["project_id"].(float64))
	if out["count"] == nil || len(out["buckets"].([]any)) < 2 {
		t.Fatalf("queue = %v", out)
	}

	task := call("task_create", map[string]any{"project_id": qpid, "title": "handoff live probe", "priority": 4})
	tid := int(task["id"].(float64))
	t.Cleanup(func() {
		session.CallTool(ctx, &mcp.CallToolParams{Name: "task_delete", Arguments: map[string]any{"task_id": tid}})
	})

	out = call("assign_queue", map[string]any{})
	var mine map[string]any
	for _, it := range out["items"].([]any) {
		row := it.(map[string]any)
		if int(row["task_id"].(float64)) == tid {
			mine = row
		}
	}
	if mine == nil {
		t.Fatalf("created task not listed: %v", out)
	}
	bucketName := fmt.Sprint(mine["bucket"])

	out = call("assign_apply", map[string]any{"task_id": tid, "destination": bucketName, "notes": "claimed live"})
	if out["applied"] != true || out["bucket"] != bucketName || out["comment_added"] != true {
		t.Fatalf("apply = %v", out)
	}
}

func TestLiveCalAddEvent(t *testing.T) {
	ctx, _, session := newProjectLiveSession(t)
	call := func(name string, args map[string]any) map[string]any {
		return liveProjectCall(t, ctx, session, name, args)
	}

	proj := call("project_create_from_template", map[string]any{"title": fmt.Sprintf("vit-cal-%d", time.Now().Unix()), "mode": "dev"})
	pid := int(proj["project_id"].(float64))
	t.Cleanup(func() {
		time.Sleep(2 * time.Second)
		session.CallTool(ctx, &mcp.CallToolParams{Name: "project_delete", Arguments: map[string]any{"project_id": pid, "confirm": true}})
	})

	time.Sleep(2 * time.Second)
	out := call("cal_add_event", map[string]any{
		"project_id": pid, "title": "review sync", "due_date": "2026-10-15T12:00:00Z",
		"start_date": "2026-10-15T11:00:00Z",
	})
	if out["label"] != "calendar" || out["task_id"] == nil {
		t.Fatalf("event = %v", out)
	}
	tid := int(out["task_id"].(float64))

	time.Sleep(2 * time.Second)
	task := call("task_get", map[string]any{"task_id": tid})
	labels, _ := task["labels"].([]any)
	found := false
	for _, l := range labels {
		if fmt.Sprint(l.(map[string]any)["title"]) == "calendar" {
			found = true
		}
	}
	if !found || fmt.Sprint(task["due_date"])[:10] != "2026-10-15" {
		t.Fatalf("task = %v", task)
	}
}
