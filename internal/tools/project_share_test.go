package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"viktor/internal/client"
)

type fakeShare struct {
	users         []map[string]any
	shared        []map[string]any
	putBody       map[string]any
	searchQueries []string
}

func (f *fakeShare) handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v2/users":
		q := r.URL.Query().Get("s")
		f.searchQueries = append(f.searchQueries, q)
		var out []map[string]any
		for _, u := range f.users {
			un, _ := u["username"].(string)
			if un == q {
				out = append(out, u)
			}
		}
		if out == nil {
			out = []map[string]any{}
		}
		json.NewEncoder(w).Encode(out)
	case r.Method == http.MethodPut && r.URL.Path == "/api/v2/projects/9/users":
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&f.putBody)
		f.shared = append(f.shared, map[string]any{
			"id":         f.putBody["user_id"],
			"username":   f.putBody["username"],
			"permission": f.putBody["permission"],
		})
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(b)
	case r.Method == http.MethodGet && r.URL.Path == "/api/v2/projects/9/users":
		json.NewEncoder(w).Encode(f.shared)
	default:
		http.Error(w, "unexpected: "+r.Method+" "+r.URL.Path, http.StatusNotFound)
	}
}

func newShareFixture(t *testing.T) (*fakeShare, *mcp.ClientSession) {
	t.Helper()
	f := &fakeShare{
		users: []map[string]any{
			{"id": float64(1), "username": "admin"},
			{"id": float64(2), "username": "crewai-agent"},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(srv.Close)

	api := client.New(srv.URL, "test-token", 0)
	srvMCP := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.0.0"}, nil)
	NewRegistry(srvMCP, api).RegisterAll()

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
	return f, session
}

func callShare(t *testing.T, session *mcp.ClientSession, args map[string]any) (map[string]any, error) {
	t.Helper()
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "project_share",
		Arguments: args,
	})
	if err != nil {
		return nil, err
	}
	if res.IsError {
		return nil, fmt.Errorf("tool error: %v", res.Content)
	}
	out, _ := json.Marshal(res.StructuredContent)
	var m map[string]any
	json.Unmarshal(out, &m)
	return m, nil
}

func TestShareUsernameOnly(t *testing.T) {
	f, session := newShareFixture(t)
	out, err := callShare(t, session, map[string]any{
		"project_id": 9, "username": "crewai-agent", "permission": 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if f.putBody["user_id"] != float64(2) {
		t.Errorf("user_id must be resolved: %v", f.putBody)
	}
	if f.putBody["username"] != "crewai-agent" {
		t.Errorf("username must be sent: %v", f.putBody)
	}
	if out["user_id"] != float64(2) || out["permission"] != float64(1) {
		t.Errorf("out = %v", out)
	}
}

func TestShareUserIDOnlyUnresolvable(t *testing.T) {
	f, session := newShareFixture(t)
	_, err := callShare(t, session, map[string]any{
		"project_id": 9, "user_id": 1, "permission": 0,
	})
	if err == nil {
		t.Fatal("id-only share must fail when instance cannot resolve username")
	}
	if len(f.searchQueries) != 1 || f.searchQueries[0] != "1" {
		t.Errorf("resolve attempt expected: %v", f.searchQueries)
	}
}

func TestShareBothProvidedNoSearch(t *testing.T) {
	f, session := newShareFixture(t)
	if _, err := callShare(t, session, map[string]any{
		"project_id": 9, "user_id": 2, "username": "crewai-agent", "permission": 2,
	}); err != nil {
		t.Fatal(err)
	}
	if len(f.searchQueries) != 0 {
		t.Errorf("no /users search expected when both fields given, got %v", f.searchQueries)
	}
	if f.putBody["permission"] != float64(2) {
		t.Errorf("permission = %v", f.putBody["permission"])
	}
}

func TestShareUnknownUser(t *testing.T) {
	_, session := newShareFixture(t)
	if _, err := callShare(t, session, map[string]any{
		"project_id": 9, "username": "ghost", "permission": 1,
	}); err == nil {
		t.Fatal("unknown user must be rejected")
	}
}

func TestShareBadPermission(t *testing.T) {
	_, session := newShareFixture(t)
	if _, err := callShare(t, session, map[string]any{
		"project_id": 9, "username": "admin", "permission": 5,
	}); err == nil {
		t.Fatal("permission 5 must be rejected")
	}
}
