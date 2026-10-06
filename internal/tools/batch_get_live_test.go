package tools

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"viktor/internal/client"
)

func TestLiveBatchGet(t *testing.T) {
	url := os.Getenv("VIKUNJA_URL")
	token := os.Getenv("VIKUNJA_TOKEN")
	if url == "" || token == "" {
		t.Skip("live test: set VIKUNJA_URL and VIKUNJA_TOKEN")
	}
	api := client.New(url, token, 0)
	srv := mcp.NewServer(&mcp.Implementation{Name: "viktor-live", Version: "0.0.0"}, nil)
	NewRegistry(srv, api).RegisterAll()
	ctx := context.Background()
	c := mcp.NewClient(&mcp.Implementation{Name: "live-client", Version: "0.0.0"}, nil)
	st, ct := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ss.Close() }()
	session, err := c.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	var probeTasks []int
	var probe map[string]any
	if err := api.CreateProject(ctx, map[string]any{"title": "viktor-batch-live-probe"}, &probe); err != nil {
		t.Skipf("live create unavailable: %v", err)
	}
	pid := asInt(probe["id"])
	defer func() { _ = api.DeleteProject(ctx, pid) }()
	for i := 0; i < 3; i++ {
		var tk map[string]any
		if err := api.CreateTask(ctx, pid, map[string]any{"title": fmt.Sprintf("batch-probe-%d", i)}, &tk); err != nil {
			t.Skipf("live create task unavailable: %v", err)
		}
		probeTasks = append(probeTasks, asInt(tk["id"]))
	}
	ids := probeTasks
	ids = append(ids, 99999999)

	start := time.Now()
	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "task_batch_get",
		Arguments: map[string]any{
			"task_ids":    ids,
			"concurrency": 8,
		},
	})
	if err != nil || res.IsError {
		t.Fatalf("batch get: err=%v isError=%v", err, res.IsError)
	}
	out, _ := res.StructuredContent.(map[string]any)
	if out == nil {
		t.Fatal("no structured content")
	}
	got := len(out["tasks"].([]any))
	fails := len(out["errors"].([]any))
	if got != 3 || fails != 1 {
		t.Errorf("tasks %d + errors %d, want 3 + 1", got, fails)
	}
	fmt.Printf("live batch: tasks=%d errors=%d elapsed=%v\n", got, fails, time.Since(start))
}
