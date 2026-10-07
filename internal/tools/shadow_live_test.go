package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"viktor/internal/client"
	"viktor/internal/shadow"
)

// TestLiveShadow exercises the full shadow cycle against a real Vikunja:
// offline create (queued), restore, sync, and verification on the tracker.
// Run with VIKUNJA_URL/VIKUNJA_TOKEN set; stop and start the tracker between
// the phases to force the offline/online transition (or point the store at a
// dead URL for phase 1 via VIKTOR_SHADOW_DEAD_URL=1).
func TestLiveShadow(t *testing.T) {
	url := os.Getenv("VIKUNJA_URL")
	token := os.Getenv("VIKUNJA_TOKEN")
	if url == "" || token == "" {
		t.Skip("live test: set VIKUNJA_URL and VIKUNJA_TOKEN")
	}
	projectID := 8
	title := fmt.Sprintf("viktor-shadow-live-%d", os.Getpid())

	dead := client.New("http://127.0.0.1:1", token, 0)
	alive := client.New(url, token, 0)
	var project map[string]any
	if err := alive.GetProject(context.Background(), projectID, &project); err != nil {
		t.Skipf("live project unavailable: %v", err)
	}
	pTitle, _ := project["title"].(string)

	store, err := shadow.NewStore(filepath.Join(os.TempDir(), "viktor-live-shadow"), "live")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.DeleteProject(int64(projectID), pTitle) }()

	// Phase 1: offline — task_create must queue, not fail.
	srv := mcp.NewServer(&mcp.Implementation{Name: "viktor-live", Version: "0.0.0"}, nil)
	reg := NewRegistry(srv, dead)
	reg.SetShadowStore(store)
	reg.RegisterAll()
	s := connectLive(t, srv)

	ctx := context.Background()
	res, err := s.CallTool(ctx, &mcp.CallToolParams{
		Name:      "task_create",
		Arguments: map[string]any{"project_id": projectID, "title": title},
	})
	if err != nil {
		t.Fatalf("task_create: %v", err)
	}
	if res.IsError {
		t.Fatalf("offline create must not fail: %v", res.Content)
	}
	if store.PendingCount() != 1 {
		t.Fatalf("pending = %d, want 1", store.PendingCount())
	}
	fmt.Printf("shadow live: queued offline, pending=%d\n", store.PendingCount())

	// Phase 2: back online — the first successful call restores reachability
	// and auto-syncs the queue onto the tracker.
	reg.SetInstances(map[string]*client.Client{"default": alive}, "default")
	reg.noteReachable(ctx, false) // simulate the known-offline transition
	if _, _, err := callListLive(t, srv); err != nil {
		t.Fatal(err)
	}
	if store.PendingCount() != 0 {
		t.Fatalf("queue not drained after restore: pending=%d", store.PendingCount())
	}

	// Phase 3: verify the card exists on the tracker, then clean up.
	var tasks []map[string]any
	if err := alive.ProjectTasks(ctx, projectID, client.ListParams{}, &tasks); err != nil {
		t.Fatal(err)
	}
	var foundID float64
	for _, tk := range tasks {
		if tk["title"] == title {
			if id, ok := tk["id"].(float64); ok {
				foundID = id
			}
		}
	}
	if foundID == 0 {
		t.Fatalf("shadowed card %q did not land on the tracker", title)
	}
	defer func() { _ = alive.DeleteTask(ctx, int(foundID)) }()
	fmt.Printf("shadow live: card %d on tracker after sync\n", int(foundID))

	// Phase 4: MD mirror regenerated and cleaned up.
	keys, _ := store.TaskKeys(int64(projectID), pTitle)
	for _, k := range keys {
		var snap shadow.TaskSnapshot
		if store.LoadTask(int64(projectID), pTitle, k, &snap) == nil && snap.ShadowID != "" {
			all, _ := store.ReadAll()
			for _, e := range all {
				if e.ShadowID == snap.ShadowID && e.Status != "synced" {
					t.Fatalf("mirror still holds unsynced shadow task %s", k)
				}
			}
		}
	}
	if _, err := os.Stat(filepath.Join(store.ProjectDir(int64(projectID), pTitle), "project.md")); err == nil {
		fmt.Println("shadow live: project.md regenerated")
	}
}

func connectLive(t *testing.T, srv *mcp.Server) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	c := mcp.NewClient(&mcp.Implementation{Name: "live-client", Version: "0.0.0"}, nil)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := srv.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	session, err := c.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func callListLive(t *testing.T, srv *mcp.Server) (string, bool, error) {
	t.Helper()
	s := connectLive(t, srv)
	res, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: "project_list", Arguments: map[string]any{}})
	if err != nil {
		return "", false, err
	}
	var text string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text += tc.Text
		}
	}
	return text, !res.IsError, nil
}
