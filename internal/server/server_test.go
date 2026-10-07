package server

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
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
	if len(res.Tools) != 94 {
		t.Errorf("tools/list = %d, want 94", len(res.Tools))
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
	for _, want := range []string{"task_create", "project_list", "today_actions", "kanban_setup"} {
		if !names[want] {
			t.Errorf("missing tool %q", want)
		}
	}
}
