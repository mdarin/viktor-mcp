package tools

import (
	"context"
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
	fmt.Printf("probe %d: done=%v bucket=%d\n", taskID, current["done"], got)
	if got != 43 {
		t.Errorf("probe landed in bucket %d, want done bucket 43", got)
	}
}
