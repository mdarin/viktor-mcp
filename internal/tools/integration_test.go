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

func TestLiveTaskUpdateMerge(t *testing.T) {
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
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := srv.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	session, err := c.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	var created struct {
		ID          int    `json:"id"`
		Title       string `json:"title"`
		Priority    int    `json:"priority"`
		Description string `json:"description"`
	}
	projectID := 8
	if v := os.Getenv("VIKUNJA_TEST_PROJECT_ID"); v != "" {
		fmt.Sscanf(v, "%d", &projectID)
	}
	body := map[string]any{
		"title":       "viktor-merge-live-probe",
		"description": "keep me",
		"priority":    2,
		"due_date":    "2026-12-01T12:00:00Z",
	}
	if err := api.CreateTask(ctx, projectID, body, &created); err != nil {
		t.Skipf("live project %d unavailable: %v", projectID, err)
	}
	defer api.DeleteTask(ctx, created.ID)

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "task_update",
		Arguments: map[string]any{
			"task_id":     created.ID,
			"description": "patched via MCP",
		},
	})
	if err != nil {
		t.Fatalf("task_update: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool error: %v", res.Content)
	}

	var current map[string]any
	if err := api.GetTask(ctx, created.ID, &current); err != nil {
		t.Fatal(err)
	}
	if got := current["priority"]; got != float64(2) {
		t.Errorf("priority reset by partial update: got %v, want 2", got)
	}
	if got := current["due_date"]; got == nil || got == "" {
		t.Errorf("due_date reset by partial update: got %v", got)
	}
	if got := current["description"]; got != "patched via MCP" {
		t.Errorf("description not updated: %v", got)
	}
	b, _ := json.Marshal(current["title"])
	if string(b) != `"viktor-merge-live-probe"` {
		t.Errorf("title changed: %s", b)
	}
}
