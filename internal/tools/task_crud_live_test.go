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

func TestLiveTaskCRUD(t *testing.T) {
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

	var probe struct {
		ID int `json:"id"`
	}
	if err := api.CreateProject(ctx, map[string]any{"title": fmt.Sprintf("live-267-%d", time.Now().Unix())}, &probe); err != nil {
		t.Skipf("live: create probe project: %v", err)
	}
	pid := probe.ID
	defer func() {
		time.Sleep(2 * time.Second)
		if err := api.DeleteProject(ctx, pid); err != nil {
			t.Logf("cleanup project %d: %v", pid, err)
		}
	}()

	cout, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "task_create", Arguments: map[string]any{
		"project_id": pid, "title": "crud overdue", "due_date": "2026-09-01T10:00:00Z", "priority": 5,
	}})
	if err != nil || cout.IsError {
		t.Fatalf("task_create: %v %v", err, cout.Content)
	}
	tid := int(cout.StructuredContent.(map[string]any)["id"].(float64))
	t.Logf("created task %d", tid)

	gout, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "task_get", Arguments: map[string]any{"task_id": tid}})
	if err != nil || gout.IsError {
		t.Fatalf("task_get: %v %v", err, gout.Content)
	}
	g := gout.StructuredContent.(map[string]any)
	if g["title"] != "crud overdue" || g["priority"] != float64(5) {
		t.Fatalf("task_get = %v", g)
	}

	lout, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "task_list", Arguments: map[string]any{"project_id": pid}})
	if err != nil || lout.IsError {
		t.Fatalf("task_list: %v %v", err, lout.Content)
	}
	if lout.StructuredContent.(map[string]any)["count"] != float64(1) {
		t.Fatalf("task_list = %v", lout.StructuredContent)
	}

	time.Sleep(2 * time.Second)
	rout, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "task_set_reminders", Arguments: map[string]any{
		"task_id": tid, "reminders": []string{"2026-10-20T09:00:00Z", "2026-10-21T09:00:00Z"},
	}})
	if err != nil || rout.IsError {
		t.Fatalf("task_set_reminders: %v %v", err, rout.Content)
	}
	if rout.StructuredContent.(map[string]any)["count"] != float64(2) {
		t.Fatalf("reminders = %v", rout.StructuredContent)
	}

	aout, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "task_assign_user", Arguments: map[string]any{
		"task_id": tid, "user_id": 2,
	}})
	if err != nil || aout.IsError {
		t.Fatalf("task_assign_user: %v %v", err, aout.Content)
	}

	time.Sleep(2 * time.Second)
	qout, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "task_query", Arguments: map[string]any{
		"query": "overdue", "project_id": pid,
	}})
	if err != nil || qout.IsError {
		for _, c := range qout.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				t.Fatalf("task_query overdue: %v", tc.Text)
			}
		}
		t.Fatalf("task_query overdue: %v %v", err, qout.Content)
	}
	if qout.StructuredContent.(map[string]any)["count"] != float64(1) {
		t.Fatalf("overdue = %v", qout.StructuredContent)
	}

	time.Sleep(2 * time.Second)
	sout, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "task_query", Arguments: map[string]any{"query": "summary", "project_id": pid}})
	if err != nil || sout.IsError {
		t.Fatalf("task_query summary: %v %v", err, sout.Content)
	}
	s := sout.StructuredContent.(map[string]any)
	if s["overdue"] != float64(1) || s["due_today"] != float64(1) || s["unscheduled"] != float64(0) {
		t.Fatalf("summary = %v", s)
	}

	var views []struct {
		ID       int    `json:"id"`
		ViewKind string `json:"view_kind"`
	}
	if err := api.Get(ctx, fmt.Sprintf("/projects/%d/views", pid), nil, &views); err != nil {
		t.Skipf("live: list views: %v", err)
	}
	var kanbanView int
	for _, v := range views {
		if v.ViewKind == "kanban" {
			kanbanView = v.ID
		}
	}
	if kanbanView != 0 {
		time.Sleep(2 * time.Second)
		pout2, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "task_set_position", Arguments: map[string]any{
			"task_id": tid, "project_id": pid, "view_id": kanbanView, "bucket_id": 42,
		}})
		if err != nil || pout2.IsError {
			t.Fatalf("task_set_position: %v %v", err, pout2.Content)
		}
		t.Logf("set_position: %v", pout2.StructuredContent)
	}

	time.Sleep(2 * time.Second)
	dout, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "task_delete", Arguments: map[string]any{"task_id": tid}})
	if err != nil || dout.IsError {
		t.Fatalf("task_delete: %v %v", err, dout.Content)
	}
	if dout.StructuredContent.(map[string]any)["deleted"] != true {
		t.Fatalf("delete = %v", dout.StructuredContent)
	}
}
