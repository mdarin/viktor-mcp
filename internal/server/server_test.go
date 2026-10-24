package server

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"viktor/internal/config"
)

func TestBuildE2EToolsList(t *testing.T) {
	t.Setenv("VIKUNJA_URL", "http://localhost:9999")
	t.Setenv("VIKUNJA_TOKEN", "tk_test")

	ctx := context.Background()
	srv, _, err := Build(ctx, os.Stderr)
	if err != nil {
		t.Fatal(err)
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "e2e", Version: "0.0.1"}, nil)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := srv.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	info := session.InitializeResult()
	if info == nil {
		t.Fatal("no initialize result")
	}
	if info.ServerInfo.Name != "vikunja-mcp" {
		t.Errorf("server name = %q", info.ServerInfo.Name)
	}

	res, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tools) != 113 {
		t.Errorf("tools/list = %d, want 113", len(res.Tools))
	}
	names := map[string]bool{}
	raw, _ := json.Marshal(res.Tools)
	var tools []struct {
		Name string `json:"name"`
	}
	json.Unmarshal(raw, &tools)
	for _, tl := range tools {
		names[tl.Name] = true
	}
	for _, want := range []string{"task_create", "project_list", "today_actions", "kanban_setup", "memory_search", "memory_status"} {
		if !names[want] {
			t.Errorf("missing tool %q", want)
		}
	}
}

func TestMemoryPerInstance(t *testing.T) {
	cfg := &config.Config{
		DefaultInstance: "work",
		Instances: map[string]*config.Instance{
			"work":  {URL: "http://a"},
			"other": {URL: "http://b"},
			"third": {URL: "http://c"},
		},
		Memory: &config.MemorySection{
			EmbedModel: "bge-m3",
			GitEnrich:  map[string]string{"work": "/repo/work"},
		},
	}
	got := memoryPerInstance(cfg)
	if len(got) != 3 {
		t.Fatalf("configs = %d, want one per instance", len(got))
	}
	w := got["work"]
	if w.EmbedModel != "bge-m3" || w.Collection != "viktor_tasks" || w.GitRepo != "/repo/work" {
		t.Fatalf("work = %+v", w)
	}
	// Instance without git_enrich keeps indexing, just without history.
	if o := got["other"]; o.GitRepo != "" || o.QdrantURL != "http://localhost:6333" {
		t.Fatalf("other = %+v", o)
	}
}
