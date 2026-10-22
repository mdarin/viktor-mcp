package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"viktor/internal/client"
)

type fakeBots struct {
	srvURL    string
	nextBot   int
	bots      map[int]map[string]any
	nextToken int
	tokens    []map[string]any
	tokenBody map[string]any
}

func (f *fakeBots) handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	path := r.URL.Path
	switch {
	case r.Method == http.MethodPost && path == "/api/v2/user/bots":
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		f.nextBot++
		bot := map[string]any{"id": f.nextBot, "username": b["username"], "name": b["name"], "bot_owner_id": 1, "status": 0}
		f.bots[f.nextBot] = bot
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(bot)
	case r.Method == http.MethodGet && path == "/api/v2/user/bots":
		var out []map[string]any
		for _, b := range f.bots {
			out = append(out, b)
		}
		if out == nil {
			out = []map[string]any{}
		}
		json.NewEncoder(w).Encode(map[string]any{"items": out, "total": len(out)})
	case r.Method == http.MethodPut && strings.HasPrefix(path, "/api/v2/user/bots/"):
		id := pathID(path, "/api/v2/user/bots/")
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		b, ok := f.bots[id]
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		b["status"] = body["status"]
		json.NewEncoder(w).Encode(b)
	case r.Method == http.MethodDelete && strings.HasPrefix(path, "/api/v2/user/bots/"):
		id := pathID(path, "/api/v2/user/bots/")
		if _, ok := f.bots[id]; !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		delete(f.bots, id)
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPost && path == "/api/v2/tokens":
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		f.tokenBody = b
		f.nextToken++
		tok := map[string]any{"id": f.nextToken, "title": b["title"], "owner_id": b["owner_id"], "expires_at": b["expires_at"], "permissions": b["permissions"], "token": "tk_secret_never_again"}
		f.tokens = append(f.tokens, tok)
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(tok)
	default:
		http.Error(w, "unexpected: "+r.Method+" "+path, http.StatusNotFound)
	}
}

func pathID(path, prefix string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(path, prefix))
	return n
}

func newBotsFixture(t *testing.T) (*fakeBots, *mcp.ClientSession) {
	t.Helper()
	f := &fakeBots{nextBot: 0, bots: map[int]map[string]any{}}
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	f.srvURL = srv.URL
	t.Cleanup(srv.Close)

	api := client.New(srv.URL, "test-token", 0)
	srvMCP := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.0.0"}, nil)
	registry := NewRegistry(srvMCP, api)
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
	return f, session
}

func TestBotCreateRequiresPrefix(t *testing.T) {
	_, session := newBotsFixture(t)
	if _, err := callCRUD(t, session, "bot_create", map[string]any{"username": "opencode"}); err == nil || !strings.Contains(err.Error(), "bot-") {
		t.Fatalf("want bot- prefix error, got %v", err)
	}
	out, err := callCRUD(t, session, "bot_create", map[string]any{"username": "bot-opencode", "name": "opencode agent"})
	if err != nil {
		t.Fatal(err)
	}
	if out["id"] == nil || out["bot_owner_id"] == nil {
		t.Fatalf("bot_create: missing id/bot_owner_id in %v", out)
	}
}

func TestBotLifecycle(t *testing.T) {
	f, session := newBotsFixture(t)
	out, err := callCRUD(t, session, "bot_create", map[string]any{"username": "bot-x"})
	if err != nil {
		t.Fatal(err)
	}
	id := out["id"]

	lst, err := callCRUD(t, session, "bot_list", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if lst["count"].(float64) != 1 {
		t.Fatalf("bot_list count = %v", lst["count"])
	}

	dis, err := callCRUD(t, session, "bot_disable", map[string]any{"bot_id": id})
	if err != nil {
		t.Fatal(err)
	}
	if dis["enabled"] != false || dis["status"].(float64) != 2 {
		t.Fatalf("bot_disable: %v", dis)
	}

	del, err := callCRUD(t, session, "bot_delete", map[string]any{"bot_id": id})
	if err != nil {
		t.Fatal(err)
	}
	if del["deleted"] != true || len(f.bots) != 0 {
		t.Fatalf("bot_delete: %v, bots left %d", del, len(f.bots))
	}
}

func TestBotTokenCreate(t *testing.T) {
	f, session := newBotsFixture(t)
	out, err := callCRUD(t, session, "bot_create", map[string]any{"username": "bot-t"})
	if err != nil {
		t.Fatal(err)
	}
	id := out["id"].(float64)

	tok, err := callCRUD(t, session, "bot_token_create", map[string]any{"bot_id": id, "title": "agent token", "expires_in_days": 730, "permissions": map[string]any{"tasks": []any{"read_all"}}})
	if err != nil {
		t.Fatal(err)
	}
	if tok["token"] != "tk_secret_never_again" {
		t.Fatalf("token cleartext missing: %v", tok)
	}
	if tok["expires_in_days"].(float64) != 730 {
		t.Fatalf("expires_in_days: %v", tok["expires_in_days"])
	}
	if f.tokenBody["owner_id"].(float64) != id {
		t.Fatalf("owner_id not bot id: %v", f.tokenBody)
	}
	if f.tokenBody["expires_at"] == "" {
		t.Fatal("expires_at required by upstream, got empty")
	}
	if _, ok := f.tokenBody["permissions"]; !ok {
		t.Fatal("permissions not forwarded")
	}

	if _, err := callCRUD(t, session, "bot_token_create", map[string]any{"bot_id": id, "title": "x", "expires_in_days": 5000}); err == nil || !strings.Contains(err.Error(), "3650") {
		t.Fatalf("want max-days guard, got %v", err)
	}
	dflt, err := callCRUD(t, session, "bot_token_create", map[string]any{"bot_id": id, "title": "y"})
	if err != nil {
		t.Fatal(err)
	}
	if dflt["expires_in_days"].(float64) != 365 {
		t.Fatalf("default expiry: %v", dflt["expires_in_days"])
	}
}
