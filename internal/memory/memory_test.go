package memory

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"viktor/internal/client"
	"viktor/internal/shadow"
)

// --- test doubles ---

type fakeSource struct {
	tasks    []Task
	comments map[int64][]Comment
	calls    int
}

func (s *fakeSource) AllTasks(ctx context.Context) ([]Task, error) {
	s.calls++
	return s.tasks, nil
}

func (s *fakeSource) Comments(ctx context.Context, taskID int64) ([]Comment, error) {
	return s.comments[taskID], nil
}

func (s *fakeSource) Name() string { return "fake" }

type fakeEmbedder struct {
	dim   int
	calls int
}

func (e *fakeEmbedder) Embed(ctx context.Context, model string, texts []string) ([][]float32, error) {
	e.calls++
	out := make([][]float32, len(texts))
	for i := range texts {
		v := make([]float32, e.dim)
		v[0] = float32(len(texts[i])%97) / 97
		out[i] = v
	}
	return out, nil
}

type fakeStore struct {
	exists     bool
	points     map[uint64]map[string]any
	upserts    int
	deletes    int
	created    int
	failUpsert bool
}

func newFakeStore() *fakeStore { return &fakeStore{points: map[uint64]map[string]any{}} }

func (s *fakeStore) CollectionExists(ctx context.Context, name string) (bool, error) {
	return s.exists, nil
}

func (s *fakeStore) CreateCollection(ctx context.Context, name string, dim int) error {
	s.created++
	s.exists = true
	return nil
}

func (s *fakeStore) Upsert(ctx context.Context, name string, points []Point) error {
	if s.failUpsert {
		return errors.New("boom")
	}
	s.upserts += len(points)
	for _, p := range points {
		s.points[p.ID] = p.Payload
	}
	return nil
}

func (s *fakeStore) DeletePoints(ctx context.Context, name string, ids []uint64) error {
	s.deletes += len(ids)
	for _, id := range ids {
		delete(s.points, id)
	}
	return nil
}

func (s *fakeStore) Count(ctx context.Context, name string) (int64, error) {
	return int64(len(s.points)), nil
}

func newTestIndexer(t *testing.T, src TaskSource, emb Embedder, st VectorStore, git GitEnricher) (*Indexer, string) {
	t.Helper()
	dir := t.TempDir()
	sp := filepath.Join(dir, "sync.json")
	ix, err := NewIndexer(Config{Instance: "test"}, src, emb, st, git, sp)
	if err != nil {
		t.Fatalf("NewIndexer: %v", err)
	}
	return ix, sp
}

// --- chunking ---

func TestChunkTask(t *testing.T) {
	task := Task{
		ID: 44, ProjectID: 8, Title: "ADR memory layer",
		Description: "Зафиксировать архитектуру", Done: true,
		Updated: "2026-10-15T12:00:00Z",
		Labels:  []Label{{ID: 1, Title: "memory"}, {ID: 2, Title: "skill"}},
	}
	comments := []Comment{
		{ID: 900, TaskID: 44, Text: "Сделано, коммит 7489629", Author: "crewai-agent"},
		{ID: 901, TaskID: 44, Text: "  ", Author: "x"},
	}
	chunks := ChunkTask(task, comments, []string{"abc1234"})
	if len(chunks) != 2 {
		t.Fatalf("want 2 chunks (task + 1 non-empty comment), got %d", len(chunks))
	}
	tc := chunks[0]
	if tc.Type != ChunkTypeTask || tc.TaskID != 44 {
		t.Fatalf("task chunk: type=%s task_id=%d", tc.Type, tc.TaskID)
	}
	for _, field := range []string{"task_id", "project_id", "title", "status", "labels", "updated_at", "chunk_type"} {
		if _, ok := tc.Payload[field]; !ok {
			t.Errorf("task chunk payload missing %q", field)
		}
	}
	if tc.Payload["status"] != "done" {
		t.Errorf("status = %v, want done", tc.Payload["status"])
	}
	if tc.Payload["commits"].([]string)[0] != "abc1234" {
		t.Errorf("commits not in payload: %v", tc.Payload["commits"])
	}
	cc := chunks[1]
	if cc.Type != ChunkTypeComment || cc.TaskID != 44 {
		t.Fatalf("comment chunk: type=%s task_id=%d", cc.Type, cc.TaskID)
	}
	if cc.Payload["task_id"].(int64) != 44 {
		t.Errorf("comment chunk payload task_id = %v", cc.Payload["task_id"])
	}
	if !strings.Contains(cc.Text, "ADR memory layer") || !strings.Contains(cc.Text, "Сделано") {
		t.Errorf("comment chunk text lacks context: %q", cc.Text)
	}
}

// --- point id determinism ---

func TestPointIDStable(t *testing.T) {
	ids := []uint64{pointID("inst", "task-44"), pointID("inst", "task-44")}
	if ids[0] != ids[1] {
		t.Fatal("point id must be deterministic")
	}
	if pointID("a", "task-44") == pointID("b", "task-44") {
		t.Fatal("instances must not collide")
	}
}

// --- sync idempotency (DoD) ---

func TestSyncIdempotent(t *testing.T) {
	src := &fakeSource{
		tasks: []Task{
			{ID: 1, ProjectID: 8, Title: "Cache invalidation", Description: "почему инвалидируется кэш", Updated: "2026-10-15T10:00:00Z"},
			{ID: 2, ProjectID: 8, Title: "Other", Done: true},
		},
		comments: map[int64][]Comment{1: {{ID: 10, TaskID: 1, Text: "root cause: stale TTL"}}},
	}
	emb := &fakeEmbedder{dim: 4}
	st := newFakeStore()
	ix, sp := newTestIndexer(t, src, emb, st, nil)

	rep, err := ix.Sync(context.Background())
	if err != nil {
		t.Fatalf("first sync: %v", err)
	}
	if rep.Upserted != 3 || rep.Source != "fake" {
		t.Fatalf("first sync report: %+v", rep)
	}
	if st.created != 1 {
		t.Fatalf("collection must be created once, got %d", st.created)
	}
	firstEmbedCalls := emb.calls

	rep2, err := ix.Sync(context.Background())
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if rep2.Upserted != 0 {
		t.Fatalf("DoD violated: repeat sync upserted %d points, want 0", rep2.Upserted)
	}
	if rep2.Skipped != 3 || rep2.ChunksTotal != 3 {
		t.Fatalf("second sync report: %+v", rep2)
	}
	if emb.calls != firstEmbedCalls {
		t.Fatal("unchanged chunks must not be re-embedded")
	}

	// State survives restart (new indexer, same state file).
	ix2, _ := NewIndexer(Config{Instance: "test"}, src, emb, st, nil, sp)
	rep3, err := ix2.Sync(context.Background())
	if err != nil {
		t.Fatalf("third sync: %v", err)
	}
	if rep3.Upserted != 0 {
		t.Fatalf("state must persist: upserted %d", rep3.Upserted)
	}

	// Changing one task re-upserts only its chunks.
	src.tasks[0].Title = "Cache invalidation policy"
	rep4, err := ix.Sync(context.Background())
	if err != nil {
		t.Fatalf("fourth sync: %v", err)
	}
	if rep4.Upserted != 2 {
		t.Fatalf("changed task must re-upsert task+comment chunks, got %d", rep4.Upserted)
	}
}

func TestSyncDeletesVanishedTasks(t *testing.T) {
	src := &fakeSource{tasks: []Task{{ID: 1, Title: "gone soon"}}}
	st := newFakeStore()
	ix, _ := newTestIndexer(t, src, &fakeEmbedder{dim: 4}, st, nil)
	if _, err := ix.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	src.tasks = nil
	rep, err := ix.Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Deleted != 1 || len(st.points) != 0 {
		t.Fatalf("vanished task point must be deleted: %+v", rep)
	}
}

func TestSyncFailedUpsertDoesNotPersistState(t *testing.T) {
	src := &fakeSource{tasks: []Task{{ID: 1, Title: "x"}}}
	st := newFakeStore()
	st.failUpsert = true
	ix, sp := newTestIndexer(t, src, &fakeEmbedder{dim: 4}, st, nil)
	if _, err := ix.Sync(context.Background()); err == nil {
		t.Fatal("want upsert error")
	}
	st.failUpsert = false
	rep, err := ix.Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Upserted != 1 {
		t.Fatalf("failed sync must retry upserts, got %d", rep.Upserted)
	}
	if _, err := os.Stat(sp); err != nil {
		t.Fatal("state file must exist after success")
	}
}

// --- commit chunks (repo history projection) ---

type fakeGit struct {
	enriched map[int64][]CommitInfo
	all      []CommitRecord
	fail     bool
}

func (g *fakeGit) Commits(ctx context.Context, taskID int64) ([]CommitInfo, error) {
	if g.fail {
		return nil, errors.New("repo broken")
	}
	return g.enriched[taskID], nil
}

func (g *fakeGit) AllCommits(ctx context.Context) ([]CommitRecord, error) {
	if g.fail {
		return nil, errors.New("repo broken")
	}
	return g.all, nil
}

func TestExtractTaskRefs(t *testing.T) {
	cases := map[string][]int64{
		"fix cache (card #45)":          {45},
		"shadow #360: rate limit":       {360},
		"merge task 42 and #43":         {42, 43},
		"card #45 + card 45":            {45},
		"no refs here":                  nil,
		"version 1.2.3 released":        nil, // dot-separated numbers are not refs
		"card #45, issue #46 (task 47)": {45, 46, 47},
	}
	for msg, want := range cases {
		got := extractTaskRefs(msg)
		if len(got) != len(want) {
			t.Errorf("%q: got %v, want %v", msg, got, want)
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%q: got %v, want %v", msg, got, want)
				break
			}
		}
	}
}

func TestParseCommitLog(t *testing.T) {
	out := "aaaa111\x1fada\x1f2026-10-15T10:00:00Z\x1ffix cache (card #45)\x1e\n" +
		"internal/a.go\ninternal/b.go\n" +
		"bbbb222\x1fbob\x1f2026-10-15T11:00:00Z\x1fdocs: readme\x1e\n" +
		"README.md\n"
	commits := parseCommitLog(out)
	if len(commits) != 2 {
		t.Fatalf("want 2 commits, got %d", len(commits))
	}
	c0 := commits[0]
	if c0.SHA != "aaaa111" || c0.Author != "ada" || c0.Subject != "fix cache (card #45)" {
		t.Fatalf("c0 = %+v", c0)
	}
	if len(c0.Files) != 2 || c0.Files[0] != "internal/a.go" {
		t.Fatalf("c0 files = %v", c0.Files)
	}
	if len(c0.TaskIDs) != 1 || c0.TaskIDs[0] != 45 {
		t.Fatalf("c0 task ids = %v", c0.TaskIDs)
	}
	if commits[1].TaskIDs != nil {
		t.Fatalf("docs commit must have no task ids: %v", commits[1].TaskIDs)
	}
}

func TestChunkCommit(t *testing.T) {
	c := CommitRecord{
		SHA: "aaaa111", Author: "ada", Date: "2026-10-15T10:00:00Z",
		Subject: "fix cache (card #45)", Files: []string{"a.go", "b.go"},
		TaskIDs: []int64{45},
	}
	ch := ChunkCommit(c)
	if ch.Key != "commit-aaaa111" || ch.Type != ChunkTypeCommit {
		t.Fatalf("chunk = %+v", ch)
	}
	if !strings.Contains(ch.Text, "fix cache") || !strings.Contains(ch.Text, "a.go") {
		t.Fatalf("text = %q", ch.Text)
	}
	if ch.Payload["sha"] != "aaaa111" {
		t.Fatalf("payload = %+v", ch.Payload)
	}
	ids := ch.Payload["task_ids"].([]int64)
	if len(ids) != 1 || ids[0] != 45 {
		t.Fatalf("task_ids = %v", ids)
	}
}

func TestSyncIndexesCommits(t *testing.T) {
	src := &fakeSource{tasks: []Task{{ID: 45, Title: "Indexer"}}}
	git := &fakeGit{
		enriched: map[int64][]CommitInfo{45: {{SHA: "cccc333", Files: []string{"x.go"}}}},
		all: []CommitRecord{
			{SHA: "cccc333", Subject: "feat: indexer (card #45)", Files: []string{"x.go"}, TaskIDs: []int64{45}},
			{SHA: "dddd444", Subject: "chore: cleanup"},
		},
	}
	emb := &fakeEmbedder{dim: 4}
	st := newFakeStore()
	ix, _ := newTestIndexer(t, src, emb, st, git)

	rep, err := ix.Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// 1 task chunk + 2 commit chunks.
	if rep.Upserted != 3 || rep.Commits != 2 {
		t.Fatalf("report: %+v", rep)
	}
	commitChunks := 0
	shas := map[string]bool{}
	for _, p := range st.points {
		switch p["chunk_type"] {
		case "commit":
			commitChunks++
			if s, ok := p["sha"].(string); ok {
				shas[s] = true
			}
		}
	}
	if commitChunks != 2 || !shas["cccc333"] || !shas["dddd444"] {
		t.Fatalf("commit chunks=%d shas=%v", commitChunks, shas)
	}

	// Repeat sync: commit chunks unchanged -> zero upserts (DoD).
	rep2, err := ix.Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rep2.Upserted != 0 || rep2.Commits != 0 {
		t.Fatalf("repeat sync must be free: %+v", rep2)
	}

	// Broken repo must not fail the sync, and its stale chunks are pruned.
	git.fail = true
	rep3, err := ix.Sync(context.Background())
	if err != nil {
		t.Fatalf("broken repo must not fail sync: %v", err)
	}
	if rep3.Commits != 0 {
		t.Fatalf("no commits expected: %+v", rep3)
	}
	n := 0
	for range st.points {
		n++
	}
	if n != 1 {
		t.Fatalf("stale commit chunks must be pruned, left %d points", n)
	}
}

// --- git enrichment ---

func TestGitCommits(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("checkout", "-q", "-b", "main")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one"), 0o644)
	run("add", ".")
	run("commit", "-q", "-m", "fix cache (card #44)")
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("two"), 0o644)
	run("add", ".")
	run("commit", "-q", "-m", "unrelated")
	os.WriteFile(filepath.Join(dir, "c.txt"), []byte("three"), 0o644)
	run("add", ".")
	run("commit", "-q", "-m", "docs(adr): memory (card #44)")

	g := &GitClient{Repo: dir}
	commits, err := g.Commits(context.Background(), 44)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 2 {
		t.Fatalf("want 2 commits for #44, got %d", len(commits))
	}
	for _, c := range commits {
		if len(c.Files) == 0 {
			t.Errorf("commit %s has no files", c.SHA)
		}
	}
	// id 4 must not match #44
	commits, err = g.Commits(context.Background(), 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 0 {
		t.Fatalf("id 4 must not match #44, got %d", len(commits))
	}
}

func TestTaskGrepPattern(t *testing.T) {
	cases := map[string]bool{
		"fix (card #44)":     true,
		"fix (card 44)":      true,
		"#444":               false,
		"card #445":          false,
		"144 things":         false,
		"card #44, task #55": true,
	}
	for msg, want := range cases {
		if got := matchAll(taskGrepPattern(44), msg); got != want {
			t.Errorf("pattern(%q) = %v, want %v", msg, got, want)
		}
	}
}

func matchAll(pattern, s string) bool {
	return regexp.MustCompile(pattern).MatchString(s)
}

// --- qdrant + ollama clients over httptest ---

func TestQdrantClientRoundTrip(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/collections/viktor_tasks", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			fmt.Fprint(w, `{"result":{"status":"green","points_count":7,"config":{"params":{"vectors":{"size":1024}}}}}`)
		case http.MethodPut:
			fmt.Fprint(w, `{"result":{"operation_id":1,"status":"completed"}}`)
		}
	})
	mux.HandleFunc("/collections/viktor_tasks/points", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"result":{"status":"completed"}}`)
	})
	mux.HandleFunc("/collections/viktor_tasks/points/delete", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"result":{"status":"completed"}}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	q := NewQdrant(srv.URL)
	ctx := context.Background()
	if ok, _ := q.CollectionExists(ctx, "viktor_tasks"); !ok {
		t.Fatal("collection must exist")
	}
	if n, _ := q.Count(ctx, "viktor_tasks"); n != 7 {
		t.Fatalf("count = %d", n)
	}
	if dim, ok, _ := q.CollectionDim(ctx, "viktor_tasks"); !ok || dim != 1024 {
		t.Fatalf("dim = %d ok=%v", dim, ok)
	}
	if err := q.Upsert(ctx, "viktor_tasks", []Point{{ID: 1, Vector: []float32{1}, Payload: map[string]any{"task_id": 1}}}); err != nil {
		t.Fatal(err)
	}
	if err := q.DeletePoints(ctx, "viktor_tasks", []uint64{1}); err != nil {
		t.Fatal(err)
	}
}

func TestOllamaClientEmbed(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/embed", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"embeddings":[[0.1,0.2],[0.3,0.4]]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	vecs, err := NewOllama(srv.URL).Embed(context.Background(), "bge-m3", []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 2 || len(vecs[0]) != 2 {
		t.Fatalf("bad vectors: %v", vecs)
	}
}

// --- shadow fallback ---

func TestShadowFallbackOnUnreachable(t *testing.T) {
	// A source whose error is classified as unreachable must fall back
	// to the mirror; we simulate via a dead API endpoint.
	root := t.TempDir()
	st, err := shadow.NewStore(root, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveTask(8, "test", "t-1", map[string]any{
		"id": int64(77), "title": "mirrored", "description": "from mirror", "done": false,
		"labels": []map[string]any{{"id": int64(1), "title": "memory"}},
	}); err != nil {
		t.Fatal(err)
	}

	dead := client.New("http://127.0.0.1:1", "tok", time.Second)
	src := NewSource(dead, st)
	tasks, err := src.AllTasks(context.Background())
	if err != nil {
		t.Fatalf("fallback must absorb unreachable: %v", err)
	}
	found := false
	for _, tk := range tasks {
		if tk.Title == "mirrored" {
			found = true
		}
	}
	if !found {
		t.Fatalf("mirrored task not found: %+v", tasks)
	}
}
