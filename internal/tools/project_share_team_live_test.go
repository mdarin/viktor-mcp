package tools

import (
	"fmt"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestLiveProjectShareTeam(t *testing.T) {
	ctx, _, session := newProjectLiveSession(t)
	call := func(name string, args map[string]any) map[string]any {
		return liveProjectCall(t, ctx, session, name, args)
	}

	proj := call("project_create_from_template", map[string]any{"title": fmt.Sprintf("vit-share-%d", time.Now().Unix()), "mode": "dev"})
	pid := int(proj["project_id"].(float64))
	team, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "teams_create", Arguments: map[string]any{"name": fmt.Sprintf("vit-share-team-%d", time.Now().Unix())}})
	if err != nil || team.IsError {
		t.Fatalf("teams_create: %v", err)
	}
	teamOut, _ := team.StructuredContent.(map[string]any)
	tid := int(teamOut["id"].(float64))
	t.Cleanup(func() {
		time.Sleep(2 * time.Second)
		session.CallTool(ctx, &mcp.CallToolParams{Name: "project_delete", Arguments: map[string]any{"project_id": pid, "confirm": true}})
		session.CallTool(ctx, &mcp.CallToolParams{Name: "teams_delete", Arguments: map[string]any{"team_id": tid}})
	})

	time.Sleep(2 * time.Second)
	out := call("project_share_team", map[string]any{"project_id": pid, "team_id": tid, "permission": 1})
	if out["shared"] != true || out["permission"] != float64(1) {
		t.Fatalf("share = %v", out)
	}

	time.Sleep(2 * time.Second)
	out = call("project_share_team", map[string]any{"project_id": pid, "action": "list"})
	rows, _ := out["teams"].([]any)
	if out["count"] != float64(1) || len(rows) != 1 || int(rows[0].(map[string]any)["team_id"].(float64)) != tid {
		t.Fatalf("list = %v", out)
	}

	time.Sleep(2 * time.Second)
	out = call("project_share_team", map[string]any{"project_id": pid, "action": "revoke", "team_id": tid})
	if out["revoked"] != true {
		t.Fatalf("revoke = %v", out)
	}
}
