package tools

import (
	"fmt"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestLiveTriage(t *testing.T) {
	ctx, _, session := newProjectLiveSession(t)
	call := func(name string, args map[string]any) map[string]any {
		return liveProjectCall(t, ctx, session, name, args)
	}
	t.Setenv("VIKTOR_SETTINGS", t.TempDir()+"/settings.json")

	proj := call("project_create_from_template", map[string]any{"title": fmt.Sprintf("vit-triage-%d", time.Now().Unix()), "mode": "dev"})
	pid := int(proj["project_id"].(float64))
	t.Cleanup(func() {
		time.Sleep(2 * time.Second)
		session.CallTool(ctx, &mcp.CallToolParams{Name: "project_delete", Arguments: map[string]any{"project_id": pid, "confirm": true}})
	})

	time.Sleep(2 * time.Second)
	out := call("triage_park", map[string]any{"project_id": pid, "reason": "shelved"})
	if out["archived"] != true || out["reason"] != "shelved" {
		t.Fatalf("park = %v", out)
	}

	time.Sleep(2 * time.Second)
	out = call("triage_parked", map[string]any{})
	rows := out["parked"].([]any)
	found := false
	for _, r := range rows {
		if int(r.(map[string]any)["project_id"].(float64)) == pid {
			found = true
		}
	}
	if out["count"] != float64(1) || !found {
		t.Fatalf("parked = %v", out)
	}
}
