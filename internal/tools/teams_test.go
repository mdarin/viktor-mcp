package tools

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type fakeTeams struct {
	teams map[int]map[string]any
	next  int
}

func teamsFixture(t *testing.T) (*fakeCRUD, *fakeTeams, *mcp.ClientSession) {
	f, session := newCRUDFixture(t)
	ft := &fakeTeams{teams: map[int]map[string]any{}, next: 8}
	f.teamsFake = ft
	return f, ft, session
}

func TestTeamsListCreate(t *testing.T) {
	ft, _, session := teamsFixture(t)
	out, err := callCRUD(t, session, "teams_list", map[string]any{})
	if err != nil || out["count"] != float64(0) {
		t.Fatalf("empty list = %v err=%v", out, err)
	}
	out, err = callCRUD(t, session, "teams_create", map[string]any{"name": "crew", "description": "d"})
	if err != nil || out["name"] != "crew" {
		t.Fatalf("create = %v err=%v", out, err)
	}
	if len(ft.teamsFake.teams) != 1 {
		t.Fatalf("stored = %v", ft.teamsFake.teams)
	}

	if _, err := callCRUD(t, session, "teams_create", map[string]any{"name": "bad name"}); err == nil {
		t.Fatal("space in name must be rejected")
	}
	if _, err := callCRUD(t, session, "teams_create", map[string]any{}); err == nil {
		t.Fatal("empty name must be rejected")
	}
}

func TestTeamsGetUpdateDelete(t *testing.T) {
	_, _, session := teamsFixture(t)
	_, err := callCRUD(t, session, "teams_create", map[string]any{"name": "t1"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := callCRUD(t, session, "teams_search", map[string]any{"query": "t1"})
	if err != nil || out["count"] != float64(1) {
		t.Fatalf("search = %v err=%v", out, err)
	}
	if _, err := callCRUD(t, session, "teams_search", map[string]any{"query": ""}); err == nil {
		t.Fatal("empty query must be rejected")
	}
	out, err = callCRUD(t, session, "teams_update", map[string]any{"team_id": 9, "name": "t2"})
	if err != nil || out["name"] != "t2" {
		t.Fatalf("update = %v err=%v", out, err)
	}
	out, err = callCRUD(t, session, "teams_update", map[string]any{"team_id": 9, "description": "desc"})
	if err != nil || out["description"] != "desc" {
		t.Fatalf("update desc = %v err=%v", out, err)
	}
	if out["name"] != "t2" {
		t.Fatalf("update desc wiped name: %v", out)
	}
	out, err = callCRUD(t, session, "teams_delete", map[string]any{"team_id": 9})
	if err != nil || out["deleted"] != true {
		t.Fatalf("delete = %v err=%v", out, err)
	}
}

func TestTeamsMembers(t *testing.T) {
	f, _, session := teamsFixture(t)
	callCRUD(t, session, "teams_create", map[string]any{"name": "m1"})
	out, err := callCRUD(t, session, "teams_member_add", map[string]any{"team_id": 9, "username": "admin"})
	if err != nil || out["username"] != "admin" {
		t.Fatalf("add = %v err=%v", out, err)
	}
	if len(f.teamsFake.teams[9]["members"].([]any)) != 1 {
		t.Fatalf("members = %v", f.teamsFake.teams[9])
	}
	out, err = callCRUD(t, session, "teams_list", map[string]any{})
	if err != nil || out["count"] != float64(1) {
		t.Fatalf("list = %v err=%v", out, err)
	}
	team := out["teams"].([]any)[0].(map[string]any)
	if team["member_count"] != float64(1) {
		t.Fatalf("team row = %v", team)
	}
	out, err = callCRUD(t, session, "teams_member_remove", map[string]any{"team_id": 9, "username": "admin"})
	if err != nil || out["removed"] != true {
		t.Fatalf("remove = %v err=%v", out, err)
	}
	if _, err := callCRUD(t, session, "teams_member_add", map[string]any{"team_id": 9}); err == nil {
		t.Fatal("missing username must be rejected")
	}
	if _, err := callCRUD(t, session, "teams_member_remove", map[string]any{"team_id": 9, "username": "no/slash"}); err == nil {
		t.Fatal("invalid username must be rejected")
	}
}
