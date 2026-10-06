package tools

import (
	"context"
	"os"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"viktor/internal/client"
)

func TestLiveProjectShare(t *testing.T) {
	url := os.Getenv("VIKUNJA_URL")
	token := os.Getenv("VIKUNJA_TOKEN")
	if url == "" || token == "" {
		t.Skip("live test: set VIKUNJA_URL and VIKUNJA_TOKEN")
	}

	api := client.New(url, token, 0)
	var probe map[string]any
	if err := api.CreateProject(context.Background(), map[string]any{"title": "viktor-share-live-probe"}, &probe); err != nil {
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
		Name: "project_share",
		Arguments: map[string]any{
			"project_id": pid,
			"username":   "admin",
			"permission": 1,
		},
	})
	if err != nil {
		t.Fatalf("project_share: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool error: %v", res.Content)
	}

	var users []map[string]any
	if err := api.ProjectUsers(ctx, pid, &users); err != nil {
		t.Fatal(err)
	}
	id, name, ok := resolveShareUser(users, ProjectShareIn{Username: "admin"})
	if !ok {
		t.Fatalf("admin not in shared users: %v", users)
	}
	perm := asInt(sharedEntryPermission(users, id))
	if name != "admin" || id != 1 || perm != 1 {
		t.Errorf("shared entry: id=%d name=%q perm=%d, want 1/admin/1", id, name, perm)
	}
}
