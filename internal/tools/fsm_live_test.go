package tools

import (
	"os"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestLiveFSME2E runs the claim → context → submit → done cycle against
// the live instance; a throwaway probe task is created and deleted.
func TestLiveFSME2E(t *testing.T) {
	ctx, _, session := newProjectLiveSession(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	CloseFSMStores()
	t.Cleanup(CloseFSMStores)

	call := func(name string, args map[string]any) map[string]any {
		return liveProjectCall(t, ctx, session, name, args)
	}

	task := call("task_create", map[string]any{
		"project_id": 8, "title": "fsm-live-probe", "priority": 1,
	})
	tid := int(task["id"].(float64))
	t.Cleanup(func() {
		call("task_delete", map[string]any{"task_id": tid})
		CloseFSMStores()
	})

	out := call("claim_task", map[string]any{"task_id": tid, "request_id": "live-claim-1", "actor": "fsm-live-probe"})
	if out["state"] != "in_progress" || out["replayed"] == true {
		t.Fatalf("claim: %v", out)
	}

	out = call("get_task_context", map[string]any{"task_id": tid})
	if out["state"] != "in_progress" {
		t.Fatalf("context: %v", out)
	}
	if _, ok := out["title"]; !ok {
		t.Fatalf("context budget must include title: %v", out)
	}

	// Forbidden jump must be rejected by the FSM.
	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "fsm_state", Arguments: map[string]any{"task_id": tid, "to": "done", "request_id": "live-forbidden"},
	})
	if err == nil && res != nil && res.IsError {
		t.Logf("forbidden transition correctly rejected")
	} else {
		t.Fatal("in_progress → done must be rejected on live stand")
	}

	out = call("submit_for_review", map[string]any{"task_id": tid, "repo": "..", "request_id": "live-submit-1"})
	if out["state"] != "in_review" {
		t.Fatalf("submit: %v", out)
	}

	out = call("fsm_state", map[string]any{"task_id": tid, "to": "done", "request_id": "live-done-1"})
	if out["state"] != "done" {
		t.Fatalf("complete: %v", out)
	}

	out = call("claim_task", map[string]any{"task_id": tid, "request_id": "live-claim-1"})
	if out["replayed"] != true {
		t.Fatalf("claim replay: %v", out)
	}
	_ = os.Getenv("XDG_STATE_HOME")
}
