package tools

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"viktor/internal/client"
)

func newMultiFixture(t *testing.T) (*mcp.ClientSession, *httptest.Server, *httptest.Server) {
	t.Helper()
	srvA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/info":
			fmt.Fprint(w, `{"version":"1.0.0"}`)
		case r.URL.Path == "/api/v1/tasks/1":
			fmt.Fprint(w, `{"id":1,"title":"from-a"}`)
		default:
			fmt.Fprint(w, `[]`)
		}
	}))
	srvB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/info":
			fmt.Fprint(w, `{"version":"2.0.0"}`)
		case r.URL.Path == "/api/v1/tasks/1":
			fmt.Fprint(w, `{"id":1,"title":"from-b"}`)
		default:
			fmt.Fprint(w, `[]`)
		}
	}))
	t.Cleanup(srvA.Close)
	t.Cleanup(srvB.Close)

	clients := map[string]*client.Client{
		"a": client.New(srvA.URL, "tk_a", 0),
		"b": client.New(srvB.URL, "tk_b", 0),
	}
	srvMCP := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.0.0"}, nil)
	registry := NewRegistry(srvMCP, clients["a"])
	registry.SetInstances(clients, "a")
	registry.RegisterAll()

	ctx := context.Background()
	c := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.0"}, nil)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := srvMCP.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	session, err := c.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session, srvA, srvB
}

func call(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) map[string]any {
	t.Helper()
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("%s: tool error: %v", name, res.Content)
	}
	out, _ := res.StructuredContent.(map[string]any)
	if out == nil {
		t.Fatalf("%s: no structured content", name)
	}
	return out
}

func callErr(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) string {
	t.Helper()
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("%s: expected error", name)
	}
	if len(res.Content) == 0 {
		return ""
	}
	type textContent interface{ Text() string }
	if tc, ok := res.Content[0].(textContent); ok {
		return tc.Text()
	}
	return fmt.Sprintf("%v", res.Content[0])
}

func TestInstanceList(t *testing.T) {
	session, _, _ := newMultiFixture(t)
	out := call(t, session, "instance_list", map[string]any{})
	if out["active"] != "a" {
		t.Errorf("active = %v, want a", out["active"])
	}
	list := out["instances"].([]any)
	if len(list) != 2 {
		t.Fatalf("instances = %d, want 2", len(list))
	}
	first := list[0].(map[string]any)
	if first["name"] != "a" || first["active"] != true || first["token_set"] != true {
		t.Errorf("first instance = %v", first)
	}
}

func TestInstanceSwitchAffectsDefaultClient(t *testing.T) {
	session, _, _ := newMultiFixture(t)
	out := call(t, session, "task_batch_get", map[string]any{"task_ids": []int{1}})
	if got := out["tasks"].([]any)[0].(map[string]any)["title"]; got != "from-a" {
		t.Errorf("before switch title = %v, want from-a", got)
	}
	call(t, session, "instance_switch", map[string]any{"name": "b"})
	out = call(t, session, "task_batch_get", map[string]any{"task_ids": []int{1}})
	if got := out["tasks"].([]any)[0].(map[string]any)["title"]; got != "from-b" {
		t.Errorf("after switch title = %v, want from-b", got)
	}
	ctxOut := call(t, session, "ctx_get", map[string]any{})
	if ctxOut["instance"] != "b" {
		t.Errorf("ctx instance = %v, want b", ctxOut["instance"])
	}
}

func TestInstanceCheckHealth(t *testing.T) {
	session, _, _ := newMultiFixture(t)
	out := call(t, session, "instance_check_health", map[string]any{"instance": "b"})
	if out["valid"] != true || out["version"] != "2.0.0" {
		t.Errorf("health b = %v", out)
	}
	out = call(t, session, "instance_check_health", map[string]any{})
	if out["version"] != "1.0.0" {
		t.Errorf("health default = %v, want active a", out)
	}
}

func TestInstanceConnectDisconnectRename(t *testing.T) {
	session, _, _ := newMultiFixture(t)

	call(t, session, "instance_connect", map[string]any{"name": "c", "url": "http://c.example", "token": "tk_c"})
	out := call(t, session, "instance_list", map[string]any{})
	if len(out["instances"].([]any)) != 3 {
		t.Errorf("instances after connect = %v", out["instances"])
	}
	if msg := callErr(t, session, "instance_connect", map[string]any{"name": "c", "url": "http://x", "token": "t"}); msg == "" {
		t.Error("duplicate connect should fail")
	}

	if msg := callErr(t, session, "instance_disconnect", map[string]any{"name": "a"}); msg == "" {
		t.Error("disconnect active should fail")
	}
	call(t, session, "instance_disconnect", map[string]any{"name": "c"})
	out = call(t, session, "instance_list", map[string]any{})
	if len(out["instances"].([]any)) != 2 {
		t.Errorf("instances after disconnect = %v", out["instances"])
	}

	call(t, session, "instance_rename", map[string]any{"old_name": "a", "new_name": "main"})
	ctxOut := call(t, session, "ctx_get", map[string]any{})
	if ctxOut["instance"] != "main" {
		t.Errorf("ctx after rename = %v, want main", ctxOut["instance"])
	}
	if msg := callErr(t, session, "instance_rename", map[string]any{"old_name": "zzz", "new_name": "x"}); msg == "" {
		t.Error("rename unknown should fail")
	}
}

func TestCtxSetProject(t *testing.T) {
	session, _, _ := newMultiFixture(t)
	out := call(t, session, "ctx_set", map[string]any{"project_id": 8})
	if out["project_id"] != float64(8) || out["instance"] != "a" {
		t.Errorf("ctx_set = %v", out)
	}
}

func TestClientForUnknownInstance(t *testing.T) {
	session, _, _ := newMultiFixture(t)
	msg := callErr(t, session, "instance_check_health", map[string]any{"instance": "nope"})
	if msg == "" {
		t.Error("unknown instance should fail")
	}
}
