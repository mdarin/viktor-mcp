package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"viktor/internal/client"
	"viktor/internal/memory"
	"viktor/internal/shadow"
	"viktor/internal/verifier"
)

// TestLiveVerifier: real Qdrant + Ollama + tracker. Creates a probe task,
// verifies an on-topic diff (no drift) and an off-topic diff (drift →
// Shadow Task), then cleans up.
func TestLiveVerifier(t *testing.T) {
	url := os.Getenv("VIKUNJA_URL")
	token := os.Getenv("VIKUNJA_TOKEN")
	if url == "" || token == "" {
		t.Skip("live test: set VIKUNJA_URL and VIKUNJA_TOKEN")
	}
	const (
		qdrantURL  = "http://localhost:6333"
		ollamaURL  = "http://localhost:11434"
		collection = "viktor_tasks"
		model      = "bge-m3"
	)
	// Skip when the services are down — graceful degradation, not failure.
	if resp, err := http.Get(qdrantURL + "/collections"); err != nil || resp.StatusCode >= 400 {
		t.Skipf("qdrant unavailable: %v", err)
	}

	t.Setenv("XDG_STATE_HOME", t.TempDir())
	projectID := 8
	api := client.New(url, token, 0)
	var created map[string]any
	if err := api.CreateTask(context.Background(), projectID, map[string]any{
		"title":       "Implement retry policy for HTTP client",
		"description": "exponential backoff\nrespect Retry-After header",
	}, &created); err != nil {
		t.Skipf("live create unavailable: %v", err)
	}
	taskID := int(created["id"].(float64))
	defer func() { _ = api.DeleteTask(context.Background(), taskID) }()

	// Repo with the submitted work; content is swapped between cases.
	repo := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	write := func(content string) {
		if err := os.WriteFile(filepath.Join(repo, "work.txt"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("adds retry policy with exponential backoff to the HTTP client\n")
	run("add", ".")
	run("commit", "-qm", "submitted work")

	sh, err := shadow.NewStore(filepath.Join(t.TempDir(), "shadow"), "default")
	if err != nil {
		t.Fatal(err)
	}

	srv := mcp.NewServer(&mcp.Implementation{Name: "viktor-live", Version: "0.0.0"}, nil)
	registry := NewRegistry(srv, api)
	registry.SetShadowStore(sh)
	registry.SetMemoryConfigs(map[string]memory.Config{"default": {
		Instance: "default", QdrantURL: qdrantURL, OllamaURL: ollamaURL,
		EmbedModel: model, Collection: collection,
	}})
	registry.RegisterAll()

	ctx := context.Background()
	c := mcp.NewClient(&mcp.Implementation{Name: "live-client", Version: "0.0.0"}, nil)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := srv.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = serverSession.Close() }()
	session, err := c.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	call := func(name string, args map[string]any) (map[string]any, error) {
		res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			return nil, err
		}
		if res.IsError {
			for _, cc := range res.Content {
				if tc, ok := cc.(*mcp.TextContent); ok {
					return nil, fmt.Errorf("%s: %s", name, tc.Text)
				}
			}
			return nil, fmt.Errorf("%s: %v", name, res.Content)
		}
		out, _ := json.Marshal(res.StructuredContent)
		var m map[string]any
		_ = json.Unmarshal(out, &m)
		return m, nil
	}

	// 1. On-topic diff: first call anchors the task (real bge-m3 embed).
	onTopic, err := call("verify_review", map[string]any{"task_id": taskID, "repo": repo})
	if err != nil {
		t.Fatalf("verify_review on-topic: %v", err)
	}
	if onTopic["drift"] != false {
		t.Fatalf("on-topic verdict = %v (semantic=%v exact=%v)", onTopic, onTopic["semantic"], onTopic["exact"])
	}

	// 2. Off-topic diff: unrelated change in a new file (no context lines
	// from the on-topic work can rescue the exact leg).
	if err := os.WriteFile(filepath.Join(repo, "other.txt"), []byte("reschedule the garden irrigation pumps and valves\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-qm", "unrelated work")
	offTopic, err := call("verify_review", map[string]any{"task_id": taskID, "repo": repo})
	if err != nil {
		t.Fatalf("verify_review drift: %v", err)
	}
	if offTopic["drift"] != true {
		t.Fatalf("drift verdict = %v (semantic=%v exact=%v)", offTopic, offTopic["semantic"], offTopic["exact"])
	}
	deadline := time.Now().Add(5 * time.Second)
	entries, _ := sh.ReadAll()
	for len(entries) == 0 && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		entries, _ = sh.ReadAll()
	}
	found := false
	for _, e := range entries {
		if e.Tool == "verify_review" && e.Op == "create" && strings.Contains(string(e.Payload), "Semantic drift") {
			found = true
		}
	}
	if !found {
		t.Fatalf("shadow journal missing correction entry: %+v", entries)
	}

	// 3. Cleanup anchors (probe hygiene in the shared collection).
	vcfg := verifier.Config{Instance: "default", QdrantURL: qdrantURL, OllamaURL: ollamaURL, EmbedModel: model, Collection: collection}.WithDefaults()
	v := verifier.New(vcfg, api, memory.NewOllama(ollamaURL), memory.NewQdrant(qdrantURL, ""), verifier.GitDiff, sh)
	if err := v.CleanupAnchors(ctx, taskID); err != nil {
		t.Logf("anchor cleanup: %v", err)
	}
}
