package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"viktor/internal/client"
)

func TestLiveTaskRelations(t *testing.T) {
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

	call := func(name string, args map[string]any) map[string]any {
		res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil || res.IsError {
			t.Fatalf("%s: %v %v", name, err, res.Content)
		}
		return res.StructuredContent.(map[string]any)
	}

	var probe struct {
		ID int `json:"id"`
	}
	if err := api.CreateProject(ctx, map[string]any{"title": fmt.Sprintf("live-rel-%d", time.Now().Unix())}, &probe); err != nil {
		t.Skipf("live: create probe project: %v", err)
	}
	pid := probe.ID
	defer func() {
		time.Sleep(3 * time.Second)
		if err := api.DeleteProject(ctx, pid); err != nil {
			t.Logf("cleanup project %d: %v", pid, err)
		}
	}()

	mk := func(title string) int {
		out := call("task_create", map[string]any{"project_id": pid, "title": title})
		return int(out["id"].(float64))
	}
	parent := mk("rel-parent")
	time.Sleep(2 * time.Second)
	child := mk("rel-child")

	sc := call("task_create_relation", map[string]any{
		"task_id": parent, "relation_kind": "subtask", "other_task_id": child,
	})
	if sc["created"] != true || sc["mirror_kind"] != "parenttask" {
		t.Fatalf("create_relation = %v", sc)
	}

	time.Sleep(2 * time.Second)
	lm := call("task_list_relations", map[string]any{"task_id": parent})
	if lm["count"] != float64(1) {
		t.Fatalf("parent relations = %v", lm)
	}
	lj, _ := json.Marshal(lm["relations"])
	t.Logf("parent relations: %s", lj)

	lm2 := call("task_list_relations", map[string]any{"task_id": child})
	if lm2["count"] != float64(1) {
		t.Fatalf("child must see mirror parenttask: %v", lm2)
	}
	lj2, _ := json.Marshal(lm2["relations"])
	if !strings.Contains(string(lj2), `"parenttask"`) {
		t.Fatalf("child relations missing parenttask: %s", lj2)
	}

	selfRes, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "task_create_relation", Arguments: map[string]any{
		"task_id": parent, "relation_kind": "related", "other_task_id": parent,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !selfRes.IsError {
		t.Fatal("self relation must fail")
	}
}
