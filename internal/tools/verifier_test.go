package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"viktor/internal/memory"
	"viktor/internal/shadow"
)

// fakeEmbedBackend serves deterministic embeddings and a mini Qdrant so
// the verify_review tool runs end to end without real services.
type fakeEmbedBackend struct {
	points map[uint64]memory.Point
}

func vectorFor(text string) []float32 {
	v := make([]float32, 8)
	if strings.Contains(text, "retry") || strings.Contains(text, "backoff") {
		for i := range v {
			v[i] = 1
		}
		return v
	}
	v[0] = 1 // orthogonal to the "retry" vector
	return v
}

func (b *fakeEmbedBackend) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/embed" && r.Method == http.MethodPost:
			var in struct {
				Input []string `json:"input"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			emb := make([][]float32, 0, len(in.Input))
			for _, t := range in.Input {
				emb = append(emb, vectorFor(t))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": emb})
		case r.URL.Path == "/collections/viktor_tasks" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{
				"status": "green",
				"config": map[string]any{"params": map[string]any{"vectors": map[string]any{"size": 8}}},
			}})
		case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/points"):
			var in struct {
				Points []memory.Point `json:"points"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			for _, p := range in.Points {
				b.points[p.ID] = p
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"result": "ok"})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/points/delete"):
			var in struct {
				Points []uint64 `json:"points"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			for _, id := range in.Points {
				delete(b.points, id)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"result": "ok"})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/points/search"):
			var in struct {
				Vector []float32 `json:"vector"`
				Limit  int       `json:"limit"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			type hit struct {
				Score   float64        `json:"score"`
				Payload map[string]any `json:"payload"`
			}
			hits := []hit{}
			for _, p := range b.points {
				if sameVec(in.Vector, p.Vector) {
					hits = append(hits, hit{Score: 1, Payload: p.Payload})
				} else if orthogonal(in.Vector, p.Vector) {
					hits = append(hits, hit{Score: 0, Payload: p.Payload})
				}
			}
			if len(hits) > in.Limit {
				hits = hits[:in.Limit]
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"result": hits})
		case r.Method == http.MethodPut:
			_ = json.NewEncoder(w).Encode(map[string]any{"result": "ok"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

func sameVec(a, b []float32) bool {
	for i := range a {
		if i >= len(b) || a[i] != b[i] {
			return false
		}
	}
	return true
}

func orthogonal(a, b []float32) bool {
	dot := float32(0)
	for i := range a {
		if i < len(b) {
			dot += a[i] * b[i]
		}
	}
	return dot == 0
}

// verifierFixture wires a registry against the fake backend and a git
// repo whose worktree contains the given file content.
func verifierFixture(t *testing.T, worktreeContent string) (*fakeCRUD, *mcp.ClientSession, *shadow.Store) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	f, session := newCRUDFixture(t)

	backend := &fakeEmbedBackend{points: map[uint64]memory.Point{}}
	srv := httptest.NewServer(backend.handler())
	t.Cleanup(srv.Close)

	// One task with a semantic title to anchor on.
	out, err := callCRUD(t, session, "task_create", map[string]any{
		"project_id": 8, "title": "Implement retry policy for HTTP client",
	})
	if err != nil {
		t.Fatal(err)
	}
	taskID := int(out["id"].(float64))

	// A git repo whose HEAD diff is the submitted work.
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
	if err := os.WriteFile(filepath.Join(repo, "work.txt"), []byte(worktreeContent), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-qm", "submitted work")

	f.verifierTaskID = taskID
	f.verifierRepo = repo
	sh, err := shadow.NewStore(filepath.Join(t.TempDir(), "shadow"), "default")
	if err != nil {
		t.Fatal(err)
	}
	registry := f.registry
	registry.SetShadowStore(sh)
	registry.SetMemoryConfigs(map[string]memory.Config{"default": {
		Instance:   "default",
		QdrantURL:  srv.URL,
		OllamaURL:  srv.URL,
		EmbedModel: "fake",
		Collection: "viktor_tasks",
	}})
	return f, session, sh
}

// TestVerifyReviewOnTopic: a diff about retry policy matches the task
// anchors → no drift, no shadow task.
func TestVerifyReviewOnTopic(t *testing.T) {
	f, session, sh := verifierFixture(t, "adds retry policy with exponential backoff to the HTTP client\n")
	out, err := callCRUD(t, session, "verify_review", map[string]any{"task_id": f.verifierTaskID, "repo": f.verifierRepo})
	if err != nil {
		t.Fatal(err)
	}
	if out["drift"] != false {
		t.Fatalf("verdict = %v, want drift=false", out)
	}
	if entries, _ := sh.ReadAll(); len(entries) != 0 {
		t.Fatalf("on-topic verdict queued shadow entries: %+v", entries)
	}
}

// TestVerifyReviewDriftQueuesShadowTask: a diff about an unrelated topic
// must be flagged as drift and queue a Shadow Task correction.
func TestVerifyReviewDriftQueuesShadowTask(t *testing.T) {
	f, session, sh := verifierFixture(t, "reschedule the garden irrigation pumps and valves\n")
	out, err := callCRUD(t, session, "verify_review", map[string]any{"task_id": f.verifierTaskID, "repo": f.verifierRepo})
	if err != nil {
		t.Fatal(err)
	}
	if out["drift"] != true {
		t.Fatalf("verdict = %v, want drift=true", out)
	}
	entries, _ := sh.ReadAll()
	found := false
	for _, e := range entries {
		if e.Tool == "verify_review" && e.Op == "create" && strings.Contains(string(e.Payload), "Semantic drift") {
			found = true
		}
	}
	if !found {
		t.Fatalf("shadow journal missing correction entry: %+v", entries)
	}
}

// TestVerifyReviewNotConfigured: no memory section → explicit error, not
// a silent pass (ADR 0003 contract).
func TestVerifyReviewNotConfigured(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	_, session := newCRUDFixture(t)
	_, err := callCRUD(t, session, "verify_review", map[string]any{"task_id": 301})
	if err == nil || !strings.Contains(err.Error(), "no memory section") {
		t.Fatalf("err = %v, want 'no memory section'", err)
	}
}

// TestSubmitForReviewVerifierGraceful: the async verifier hook must not
// break submit_for_review when the backend is unreachable.
func TestSubmitForReviewVerifierGraceful(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	f, session := newCRUDFixture(t)
	f.registry.SetMemoryConfigs(map[string]memory.Config{"default": {
		Instance: "default", QdrantURL: "http://127.0.0.1:1", OllamaURL: "http://127.0.0.1:1", EmbedModel: "fake", Collection: "viktor_tasks",
	}})
	// This test claims task 301 in its own fresh FSM store; close the
	// cached stores so later tests (watchdog) start from a clean journal.
	CloseFSMStores()
	defer CloseFSMStores()
	if _, err := callCRUD(t, session, "claim_task", map[string]any{"task_id": 301, "request_id": "req-verify-graceful-claim"}); err != nil {
		t.Fatal(err)
	}
	out, err := callCRUD(t, session, "submit_for_review", map[string]any{
		"task_id": 301, "request_id": "req-verify-graceful", "actor": "alice", "repo": f.verifierRepo,
	})
	if err != nil {
		t.Fatalf("submit_for_review must succeed despite verifier outage: %v", err)
	}
	if out["state"] != "in_review" {
		t.Fatalf("state = %v", out["state"])
	}
	time.Sleep(50 * time.Millisecond) // let the background hook fail into the log, not the test
	_ = context.Background()
	_ = time.Now
}
