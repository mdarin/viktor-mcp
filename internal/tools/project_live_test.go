package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"viktor/internal/client"
)

func newProjectLiveSession(t *testing.T) (context.Context, *client.Client, *mcp.ClientSession) {
	t.Helper()
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
	t.Cleanup(func() { serverSession.Close() })
	session, err := c.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return ctx, api, session
}

func liveProjectCall(t *testing.T, ctx context.Context, session *mcp.ClientSession, name string, args map[string]any) map[string]any {
	t.Helper()
	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("%s failed: %s", name, raw)
	}
	m, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("%s: unexpected content %T", name, res.StructuredContent)
	}
	return m
}

func TestLiveProjects(t *testing.T) {
	ctx, _, session := newProjectLiveSession(t)

	title := fmt.Sprintf("vit-live-projects-%d", time.Now().Unix())
	out := liveProjectCall(t, ctx, session, "project_create", map[string]any{"title": title})
	pid := int(out["id"].(float64))

	defer func() {
		time.Sleep(2 * time.Second)
		if res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "project_delete", Arguments: map[string]any{"project_id": pid, "confirm": true}}); err != nil || res.IsError {
			t.Logf("cleanup project_delete: %v %v", err, res)
		}
	}()

	time.Sleep(2 * time.Second)
	out = liveProjectCall(t, ctx, session, "project_get", map[string]any{"project_id": pid})
	if out["title"] != title {
		t.Fatalf("get = %v", out)
	}

	time.Sleep(2 * time.Second)
	out = liveProjectCall(t, ctx, session, "project_update", map[string]any{"project_id": pid, "description": "live test"})
	if out["description"] != "live test" {
		t.Fatalf("update = %v", out)
	}

	time.Sleep(2 * time.Second)
	out = liveProjectCall(t, ctx, session, "project_setup", map[string]any{"project_id": pid, "buckets": []string{"todo", "in-progress", "done"}})
	if int(out["done_bucket_id"].(float64)) == 0 {
		t.Fatalf("setup = %v", out)
	}

	time.Sleep(2 * time.Second)
	out = liveProjectCall(t, ctx, session, "project_analyze", map[string]any{"project_id": pid})
	if _, ok := out["total"]; !ok {
		t.Fatalf("analyze = %v", out)
	}

	time.Sleep(2 * time.Second)
	out = liveProjectCall(t, ctx, session, "project_list", map[string]any{})
	found := false
	for _, p := range out["projects"].([]any) {
		if int(p.(map[string]any)["id"].(float64)) == pid {
			found = true
		}
	}
	if !found {
		t.Fatal("created project not in list")
	}

	time.Sleep(2 * time.Second)
	out = liveProjectCall(t, ctx, session, "project_create_from_template", map[string]any{"title": title + "-tpl", "mode": "simple"})
	tplPID := int(out["project_id"].(float64))
	defer func() {
		time.Sleep(2 * time.Second)
		session.CallTool(ctx, &mcp.CallToolParams{Name: "project_delete", Arguments: map[string]any{"project_id": tplPID, "confirm": true}})
	}()
	buckets := out["buckets"].([]any)
	if len(buckets) != 2 {
		t.Fatalf("simple template buckets = %d", len(buckets))
	}
}
