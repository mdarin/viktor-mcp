package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"viktor/internal/client"
	"viktor/internal/shadow"
)

// newShadowSession builds an MCP session whose tracker client points at a
// dead URL (connection refused) with a temp shadow store enabled.
func newShadowSession(t *testing.T) (*mcp.ClientSession, *shadow.Store) {
	t.Helper()
	store, err := shadow.NewStore(filepath.Join(t.TempDir(), "shadow"), "test")
	if err != nil {
		t.Fatal(err)
	}
	srvMCP := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.0.0"}, nil)
	reg := NewRegistry(srvMCP, client.New("http://127.0.0.1:1", "tok", 0))
	reg.SetShadowStore(store)
	reg.RegisterAll()

	ctx := context.Background()
	c := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.0"}, nil)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := srvMCP.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	clientSession, err := c.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = clientSession.Close() })
	return clientSession, store
}

func callToolJSON(t *testing.T, s *mcp.ClientSession, name, args string) (string, bool) {
	t.Helper()
	res, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: json.RawMessage(args)})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var text string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text += tc.Text
		}
	}
	return text, !res.IsError
}

func assertContains(t *testing.T, got, want string) {
	t.Helper()
	compact := strings.Map(func(r rune) rune {
		if r == ' ' || r == '\n' || r == '\t' {
			return -1
		}
		return r
	}, got)
	if !strings.Contains(compact, strings.ReplaceAll(want, " ", "")) {
		t.Fatalf("result missing %q:\n%s", want, got)
	}
}

func TestShadowCreateQueuesWhenUnreachable(t *testing.T) {
	s, store := newShadowSession(t)
	text, ok := callToolJSON(t, s, "task_create", `{"project_id": 64, "title": "offline card", "priority": 3}`)
	if !ok {
		t.Fatalf("task_create must not fail in shadow mode: %s", text)
	}
	assertContains(t, text, `"shadowed":true`)
	assertContains(t, text, `"shadow_id":"s-1"`)
	if store.PendingCount() != 1 {
		t.Fatalf("pending = %d, want 1", store.PendingCount())
	}
}

func TestShadowUnsupportedToolFailsCleanly(t *testing.T) {
	s, _ := newShadowSession(t)
	text, ok := callToolJSON(t, s, "kanban_setup", `{"project_id": 64}`)
	if ok {
		t.Fatalf("out-of-scope write must fail offline: %s", text)
	}
	assertContains(t, text, "not supported offline")
}

func TestShadowStatusReportsPending(t *testing.T) {
	s, _ := newShadowSession(t)
	if _, ok := callToolJSON(t, s, "task_create", `{"project_id": 64, "title": "a"}`); !ok {
		t.Fatal("create should shadow")
	}
	text, ok := callToolJSON(t, s, "shadow_status", `{}`)
	if !ok {
		t.Fatal(text)
	}
	assertContains(t, text, `"mode":"shadow (offline)"`)
	assertContains(t, text, `"pending":1`)
}

func TestShadowReadServesMirrorForCreatedTask(t *testing.T) {
	s, store := newShadowSession(t)
	if _, ok := callToolJSON(t, s, "task_create", `{"project_id": 64, "title": "mirrored"}`); !ok {
		t.Fatal("create should shadow")
	}
	// Simulate a synced task: snapshot stored under its remote numeric id.
	ref, _ := store.FindProjectByID(64)
	_ = store.SaveTask(64, ref.Title, "42", shadow.TaskSnapshot{ID: 42, Title: "mirrored", ShadowID: "s-1"})
	text, ok := callToolJSON(t, s, "task_get", `{"task_id": 42}`)
	if !ok {
		t.Fatalf("task_get should serve mirror: %s", text)
	}
	assertContains(t, text, `"shadowed_data":true`)
	assertContains(t, text, `"title":"mirrored"`)
}

func TestShadowSyncProbeStillDown(t *testing.T) {
	s, _ := newShadowSession(t)
	if _, ok := callToolJSON(t, s, "task_create", `{"project_id": 64, "title": "a"}`); !ok {
		t.Fatal("create should shadow")
	}
	text, ok := callToolJSON(t, s, "shadow_sync", `{}`)
	if !ok {
		t.Fatal(text)
	}
	assertContains(t, text, `"synced":false`)
	assertContains(t, text, `"pending":1`)
}
