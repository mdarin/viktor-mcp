package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"viktor/internal/client"
)

func TestLiveTaskComplete(t *testing.T) {
	url := os.Getenv("VIKUNJA_URL")
	token := os.Getenv("VIKUNJA_TOKEN")
	if url == "" || token == "" {
		t.Skip("live test: set VIKUNJA_URL and VIKUNJA_TOKEN")
	}
	projectID := 8

	api := client.New(url, token, 0)
	var created map[string]any
	if err := api.CreateTask(context.Background(), projectID, map[string]any{"title": "viktor-complete-live-probe"}, &created); err != nil {
		t.Skipf("live create unavailable: %v", err)
	}
	taskID := int(created["id"].(float64))
	defer func() { _ = api.DeleteTask(context.Background(), taskID) }()

	srv := mcp.NewServer(&mcp.Implementation{Name: "viktor-live", Version: "0.0.0"}, nil)
	NewRegistry(srv, api).RegisterAll()

	ctx := context.Background()
	c := mcp.NewClient(&mcp.Implementation{Name: "live-client", Version: "0.0.0"}, nil)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := srv.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = serverSession.Close() }()
	session, err := c.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "task_complete",
		Arguments: map[string]any{"task_id": taskID},
	})
	if err != nil {
		t.Fatalf("task_complete: %v", err)
	}
	if res.IsError {
		for _, cc := range res.Content {
			if tc, ok := cc.(*mcp.TextContent); ok {
				t.Fatalf("tool error: %s", tc.Text)
			}
		}
		t.Fatalf("tool error: %v", res.Content)
	}

	var current map[string]any
	if err := api.GetTask(ctx, taskID, &current); err != nil {
		t.Fatal(err)
	}
	if current["done"] != true {
		t.Errorf("done = %v, want true", current["done"])
	}
	var buckets []map[string]any
	if err := api.ViewTasks(ctx, projectID, 32, &buckets); err != nil {
		t.Fatal(err)
	}
	got, ok := findTaskBucket(buckets, taskID)
	if !ok {
		t.Fatal("probe not found in any kanban bucket after completion")
	}
	if got != 43 {
		// Vikunja <=2.6.0 v2 kanban routes accept but no-op bucket moves;
		// the tool surfaces bucket_moved=false + warning in that case.
		t.Logf("probe landed in bucket %d (server did not apply the move; done=true is the contract)", got)
	}
}

// TestLiveWatchdog: claim a fresh probe task, stall it via watchdog_scan
// with a near-zero threshold, resume it with an audited actor, then reset
// it to backlog. Exercises the FSM store against the real tracker.
func TestLiveWatchdog(t *testing.T) {
	url := os.Getenv("VIKUNJA_URL")
	token := os.Getenv("VIKUNJA_TOKEN")
	if url == "" || token == "" {
		t.Skip("live test: set VIKUNJA_URL and VIKUNJA_TOKEN")
	}
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	projectID := 8

	api := client.New(url, token, 0)
	var created map[string]any
	if err := api.CreateTask(context.Background(), projectID, map[string]any{"title": "viktor-watchdog-live-probe"}, &created); err != nil {
		t.Skipf("live create unavailable: %v", err)
	}
	taskID := int(created["id"].(float64))
	defer func() { _ = api.DeleteTask(context.Background(), taskID) }()

	srv := mcp.NewServer(&mcp.Implementation{Name: "viktor-live", Version: "0.0.0"}, nil)
	NewRegistry(srv, api).RegisterAll()

	ctx := context.Background()
	c := mcp.NewClient(&mcp.Implementation{Name: "live-client", Version: "0.0.0"}, nil)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := srv.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = serverSession.Close() }()
	session, err := c.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	call := func(name string, args map[string]any) (map[string]any, error) {
		res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			return nil, err
		}
		if res.IsError {
			for _, cc := range res.Content {
				if tc, ok := cc.(*mcp.TextContent); ok {
					return nil, fmt.Errorf("%s: %s", name, tc.Text)
				}
			}
			return nil, fmt.Errorf("%s: %v", name, res.Content)
		}
		out, _ := json.Marshal(res.StructuredContent)
		var m map[string]any
		_ = json.Unmarshal(out, &m)
		return m, nil
	}

	if _, err := call("claim_task", map[string]any{"task_id": taskID, "request_id": "live-watch-claim"}); err != nil {
		t.Fatalf("claim_task: %v", err)
	}
	res, err := call("watchdog_scan", map[string]any{"threshold_hours": 0.000001})
	if err != nil {
		t.Fatalf("watchdog_scan: %v", err)
	}
	stalled, _ := res["stalled"].([]any)
	var hit bool
	for _, sv := range stalled {
		if s, _ := sv.(map[string]any); int(s["task_id"].(float64)) == taskID {
			hit = true
		}
	}
	if !hit {
		t.Fatalf("watchdog_scan did not stall probe %d: %v", taskID, res)
	}

	rres, err := call("watchdog_resume", map[string]any{"task_id": taskID, "actor": "live-probe"})
	if err != nil {
		t.Fatalf("watchdog_resume: %v", err)
	}
	if rres["state"] != "in_progress" {
		t.Fatalf("resume state = %v, want in_progress", rres["state"])
	}

	if _, err := call("fsm_state", map[string]any{"task_id": taskID, "to": "backlog"}); err != nil {
		t.Fatalf("reset to backlog: %v", err)
	}
}
