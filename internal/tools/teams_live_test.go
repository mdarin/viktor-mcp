package tools

import (
	"fmt"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestLiveTeams(t *testing.T) {
	ctx, _, session := newProjectLiveSession(t)
	call := func(name string, args map[string]any) map[string]any {
		return liveProjectCall(t, ctx, session, name, args)
	}

	name := fmt.Sprintf("vit-live-teams-%d", time.Now().Unix())
	out := call("teams_create", map[string]any{"name": name})
	tid := int(out["id"].(float64))
	defer func() {
		time.Sleep(2 * time.Second)
		session.CallTool(ctx, &mcp.CallToolParams{Name: "teams_delete", Arguments: map[string]any{"team_id": tid}})
	}()

	time.Sleep(2 * time.Second)
	out = call("teams_search", map[string]any{"query": name[:20]})
	found := false
	for _, tm := range out["teams"].([]any) {
		if int(tm.(map[string]any)["id"].(float64)) == tid {
			found = true
		}
	}
	if !found {
		t.Fatal("created team not found by search")
	}

	time.Sleep(2 * time.Second)
	out = call("teams_member_add", map[string]any{"team_id": tid, "username": "admin"})
	if out["username"] != "admin" {
		t.Fatalf("member_add = %v", out)
	}

	time.Sleep(2 * time.Second)
	out = call("teams_list", map[string]any{})
	found = false
	for _, tm := range out["teams"].([]any) {
		if int(tm.(map[string]any)["id"].(float64)) == tid {
			found = true
		}
	}
	if !found {
		t.Fatal("team not in list")
	}

	time.Sleep(2 * time.Second)
	out = call("teams_update", map[string]any{"team_id": tid, "description": "live"})
	if out["description"] != "live" {
		t.Fatalf("update = %v", out)
	}

	time.Sleep(2 * time.Second)
	out = call("teams_member_remove", map[string]any{"team_id": tid, "username": "admin"})
	if out["removed"] != true {
		t.Fatalf("member_remove = %v", out)
	}
}
