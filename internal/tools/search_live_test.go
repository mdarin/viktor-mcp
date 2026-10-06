package tools

import (
	"fmt"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestLiveSearch(t *testing.T) {
	ctx, _, session := newProjectLiveSession(t)
	call := func(name string, args map[string]any) map[string]any {
		return liveProjectCall(t, ctx, session, name, args)
	}

	ptitle := fmt.Sprintf("vit-search-%d", time.Now().Unix())
	proj := call("project_create_from_template", map[string]any{"title": ptitle, "mode": "dev"})
	pid := int(proj["project_id"].(float64))
	t.Cleanup(func() {
		time.Sleep(2 * time.Second)
		session.CallTool(ctx, &mcp.CallToolParams{Name: "project_delete", Arguments: map[string]any{"project_id": pid, "confirm": true}})
	})

	time.Sleep(2 * time.Second)
	call("task_create", map[string]any{"project_id": pid, "title": "wunderbar unique"})

	time.Sleep(2 * time.Second)
	out := call("search_all_tasks", map[string]any{"query": "wunderbar"})
	if out["count"] != float64(1) {
		t.Fatalf("search_all_tasks = %v", out)
	}
	row := out["tasks"].([]any)[0].(map[string]any)
	if row["project_id"] != float64(pid) {
		t.Fatalf("row = %v", row)
	}

	time.Sleep(2 * time.Second)
	out = call("search_all_tasks", map[string]any{"query": "wunderbar", "project_id": pid})
	if out["count"] != float64(1) {
		t.Fatalf("scoped = %v", out)
	}

	time.Sleep(2 * time.Second)
	out = call("search_all", map[string]any{"query": ptitle})
	found := false
	for _, p := range out["projects"].([]any) {
		if int(p.(map[string]any)["id"].(float64)) == pid {
			found = true
		}
	}
	if !found {
		t.Fatalf("project not found: %v", out["projects"])
	}

}
