package tools

import (
	"fmt"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestLiveComments(t *testing.T) {
	ctx, _, session := newProjectLiveSession(t)
	call := func(name string, args map[string]any) map[string]any {
		return liveProjectCall(t, ctx, session, name, args)
	}

	proj := call("project_create_from_template", map[string]any{"title": fmt.Sprintf("vit-comments-%d", time.Now().Unix()), "mode": "dev"})
	pid := int(proj["project_id"].(float64))
	t.Cleanup(func() {
		time.Sleep(2 * time.Second)
		session.CallTool(ctx, &mcp.CallToolParams{Name: "project_delete", Arguments: map[string]any{"project_id": pid, "confirm": true}})
	})

	time.Sleep(2 * time.Second)
	task := call("task_create", map[string]any{"project_id": pid, "title": "comment target"})
	tid := int(task["id"].(float64))

	time.Sleep(2 * time.Second)
	out := call("comment_add", map[string]any{"task_id": tid, "text": "first note"})
	cid := int(out["id"].(float64))
	if out["text"] != "first note" || out["author"] == "" {
		t.Fatalf("add = %v", out)
	}

	time.Sleep(2 * time.Second)
	out = call("comment_update", map[string]any{"task_id": tid, "comment_id": cid, "text": "edited note"})
	if out["text"] != "edited note" {
		t.Fatalf("update = %v", out)
	}

	time.Sleep(2 * time.Second)
	out = call("comment_list", map[string]any{"task_id": tid})
	rows := out["comments"].([]any)
	if out["count"] != float64(1) || rows[0].(map[string]any)["text"] != "edited note" {
		t.Fatalf("list = %v", out)
	}

	time.Sleep(2 * time.Second)
	out = call("comment_delete", map[string]any{"task_id": tid, "comment_id": cid})
	if out["deleted"] != true {
		t.Fatalf("delete = %v", out)
	}

	time.Sleep(2 * time.Second)
	out = call("comment_list", map[string]any{"task_id": tid})
	if out["count"] != float64(0) {
		t.Fatalf("list after delete = %v", out)
	}

	out = call("comment_recent", map[string]any{})
	if out["supported"] != false {
		t.Fatalf("recent = %v", out)
	}
}
