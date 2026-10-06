package tools

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"viktor/internal/client"
)

func TestLiveKanbanSetup(t *testing.T) {
	url := os.Getenv("VIKUNJA_URL")
	token := os.Getenv("VIKUNJA_TOKEN")
	if url == "" || token == "" {
		t.Skip("live test: set VIKUNJA_URL and VIKUNJA_TOKEN")
	}

	api := client.New(url, token, 0)
	var probe map[string]any
	if err := api.CreateProject(context.Background(), map[string]any{"title": "viktor-kanban-live-probe"}, &probe); err != nil {
		t.Skipf("live create unavailable: %v", err)
	}
	pid := asInt(probe["id"])
	defer func() { _ = api.DeleteProject(context.Background(), pid) }()

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
		Name: "kanban_setup",
		Arguments: map[string]any{
			"project_id": pid,
			"columns":    []string{"todo", "in-progress", "done"},
		},
	})
	if err != nil || res.IsError {
		t.Fatalf("first setup: err=%v isError=%v content=%v", err, res.IsError, res.Content)
	}

	var buckets []map[string]any
	viewID := 0
	var views []map[string]any
	if err := api.ListViews(ctx, pid, &views); err != nil {
		t.Fatal(err)
	}
	viewID, _, _ = pickKanbanView(views)
	if err := api.ListBuckets(ctx, pid, viewID, &buckets); err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 3 {
		t.Fatalf("buckets after setup = %d, want 3", len(buckets))
	}
	var view map[string]any
	for _, v := range views {
		if asInt(v["id"]) == viewID {
			view = v
		}
	}
	if asInt(view["done_bucket_id"]) == 0 {
		t.Errorf("done_bucket_id not configured: %v", view)
	}
	fmt.Printf("live setup: view=%d buckets=%d done_bucket=%d\n", viewID, len(buckets), asInt(view["done_bucket_id"]))
}
