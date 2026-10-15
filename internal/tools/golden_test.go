package tools

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var updateSnapshot = flag.Bool("update-snapshot", false, "rewrite testdata/golden.json")

const expectedToolCount = 96

func newTestServer(t *testing.T) *mcp.Server {
	t.Helper()
	return mcp.NewServer(&mcp.Implementation{Name: "viktor-test", Version: "0.1.0"}, nil)
}

func listSurface(t *testing.T, srv *mcp.Server) []byte {
	t.Helper()
	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "golden", Version: "0.0.1"}, nil)
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
	res, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	out, err := json.MarshalIndent(res.Tools, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(out, '\n')
}

func TestGoldenSnapshot(t *testing.T) {
	srv := newTestServer(t)
	registry := NewRegistry(srv, nil)
	registry.RegisterAll()

	if got := len(registry.Names()); got != expectedToolCount {
		t.Errorf("registered %d tools, want %d", got, expectedToolCount)
	}

	surface := listSurface(t, srv)

	var names []string
	var parsed []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(surface, &parsed); err != nil {
		t.Fatal(err)
	}
	for _, tl := range parsed {
		names = append(names, tl.Name)
	}
	if len(names) != expectedToolCount {
		t.Errorf("tools/list returned %d tools, want %d", len(names), expectedToolCount)
	}

	golden := filepath.Join("testdata", "golden.json")
	if *updateSnapshot {
		if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, surface, 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}

	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("missing snapshot, run: go test ./internal/tools -update-snapshot: %v", err)
	}
	if string(want) != string(surface) {
		t.Errorf("tool surface drifted from snapshot; run: go test ./internal/tools -update-snapshot")
	}
}

func TestToolsListReachable(t *testing.T) {
	srv := newTestServer(t)
	NewRegistry(srv, nil).RegisterAll()

	res := listSurface(t, srv)
	if len(res) == 0 {
		t.Fatal("tools/list returned empty payload")
	}
}
