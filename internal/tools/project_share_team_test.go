package tools

import (
	"testing"
)

func TestProjectShareTeamLifecycle(t *testing.T) {
	f, ft, session := teamsFixture(t)
	f.projects[350] = map[string]any{"id": float64(350), "title": "shared-proj"}
	_, err := callCRUD(t, session, "teams_create", map[string]any{"name": "crew"})
	if err != nil {
		t.Fatal(err)
	}
	tid := ft.next

	out, err := callCRUD(t, session, "project_share_team", map[string]any{"project_id": 350, "team_id": tid, "permission": 1})
	if err != nil {
		t.Fatal(err)
	}
	if out["shared"] != true || out["permission"] != float64(1) {
		t.Fatalf("share = %v", out)
	}

	out, err = callCRUD(t, session, "project_share_team", map[string]any{"project_id": 350, "action": "list"})
	if err != nil {
		t.Fatal(err)
	}
	rows := out["teams"].([]any)
	if out["count"] != float64(1) || rows[0].(map[string]any)["team_id"] != float64(tid) || rows[0].(map[string]any)["name"] != "crew" {
		t.Fatalf("list = %v", out)
	}

	if _, err := callCRUD(t, session, "project_share_team", map[string]any{"project_id": 350, "team_id": tid, "permission": 2}); err == nil {
		t.Fatal("duplicate share must fail")
	}

	out, err = callCRUD(t, session, "project_share_team", map[string]any{"project_id": 350, "action": "revoke", "team_id": tid})
	if err != nil {
		t.Fatal(err)
	}
	if out["revoked"] != true {
		t.Fatalf("revoke = %v", out)
	}

	if _, err := callCRUD(t, session, "project_share_team", map[string]any{"project_id": 350, "action": "revoke", "team_id": tid}); err == nil {
		t.Fatal("revoke of unshared team must fail")
	}
}

func TestProjectShareTeamValidation(t *testing.T) {
	_, _, session := teamsFixture(t)
	if _, err := callCRUD(t, session, "project_share_team", map[string]any{"project_id": 350, "action": "share"}); err == nil {
		t.Fatal("share without team_id must fail")
	}
	if _, err := callCRUD(t, session, "project_share_team", map[string]any{"project_id": 350, "team_id": 8, "permission": 5}); err == nil {
		t.Fatal("permission 5 must fail")
	}
	if _, err := callCRUD(t, session, "project_share_team", map[string]any{"project_id": 350, "action": "explode"}); err == nil {
		t.Fatal("unknown action must fail")
	}
	if _, err := callCRUD(t, session, "project_share_team", map[string]any{"project_id": 350, "team_id": 999, "permission": 1}); err == nil {
		t.Fatal("missing team must fail")
	}
}
