package verifier

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"viktor/internal/memory"
	"viktor/internal/shadow"
)

type fakeEmbedder struct {
	dim  int
	fail bool
	// vectors keyed by text substring; default topic/drift vectors below.
	vec map[string][]float32
}

func (f *fakeEmbedder) Embed(_ context.Context, _ string, texts []string) ([][]float32, error) {
	if f.fail {
		return nil, errors.New("embedder down")
	}
	out := make([][]float32, 0, len(texts))
	for _, t := range texts {
		if v, ok := f.vecKey(t); ok {
			out = append(out, v)
			continue
		}
		out = append(out, f.defaultVec(t))
	}
	return out, nil
}

func (f *fakeEmbedder) vecKey(t string) ([]float32, bool) {
	for k, v := range f.vec {
		if strings.Contains(t, k) {
			return v, true
		}
	}
	return nil, false
}

func (f *fakeEmbedder) defaultVec(text string) []float32 {
	v := make([]float32, f.dim)
	// Deterministic pseudo-vector: hash chars into dims.
	sum := 0
	for _, r := range text {
		sum += int(r)
	}
	for i := range v {
		v[i] = float32((sum*(i+7))%97) / 97
	}
	return v
}

// sim sets the cosine similarity of two vectors explicitly by making
// them identical (sim 1) or orthogonal.
func constVec(dim int, val float32, ones int) []float32 {
	v := make([]float32, dim)
	for i := 0; i < ones && i < dim; i++ {
		v[i] = val
	}
	return v
}

type fakeStore struct {
	exists  bool
	points  map[uint64]memory.Point
	failUps bool
}

func (s *fakeStore) CollectionExists(_ context.Context, _ string) (bool, error) { return s.exists, nil }
func (s *fakeStore) CreateCollection(_ context.Context, _ string, _ int) error {
	s.exists = true
	return nil
}
func (s *fakeStore) Upsert(_ context.Context, _ string, points []memory.Point) error {
	if s.failUps {
		return errors.New("upsert failed")
	}
	if s.points == nil {
		s.points = map[uint64]memory.Point{}
	}
	for _, p := range points {
		s.points[p.ID] = p
	}
	return nil
}
func (s *fakeStore) DeletePoints(_ context.Context, _ string, ids []uint64) error {
	if s.points == nil {
		s.points = map[uint64]memory.Point{}
	}
	for _, id := range ids {
		delete(s.points, id)
	}
	return nil
}
func (s *fakeStore) SearchPoints(_ context.Context, _ string, vector []float32, k int, _ *memory.SearchFilter) ([]memory.SearchHit, error) {
	type scored struct {
		id    uint64
		score float64
	}
	var hits []scored
	for id, p := range s.points {
		hits = append(hits, scored{id: id, score: cosine(vector, p.Vector)})
	}
	// sort desc
	for i := 0; i < len(hits); i++ {
		for j := i + 1; j < len(hits); j++ {
			if hits[j].score > hits[i].score {
				hits[i], hits[j] = hits[j], hits[i]
			}
		}
	}
	out := []memory.SearchHit{}
	for i, h := range hits {
		if i >= k {
			break
		}
		out = append(out, memory.SearchHit{Score: h.score, Payload: s.points[h.id].Payload})
	}
	return out, nil
}

func cosine(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (sqrt(na) * sqrt(nb))
}

func sqrt(x float64) float64 {
	if x <= 0 {
		return 0
	}
	g := x
	for i := 0; i < 40; i++ {
		g = (g + x/g) / 2
	}
	return g
}

type fakeTasks struct{ tasks map[int]map[string]any }

func (f *fakeTasks) GetTask(_ context.Context, id int, out any) error {
	t, ok := f.tasks[id]
	if !ok {
		return fmt.Errorf("404: task %d", id)
	}
	*(out.(*map[string]any)) = t
	return nil
}

func newTestVerifier(t *testing.T, embedder Embedder, store *fakeStore, diff string) (*Verifier, *shadow.Store) {
	t.Helper()
	dir := t.TempDir()
	sh, err := shadow.NewStore(dir, "default")
	if err != nil {
		t.Fatal(err)
	}
	tasks := &fakeTasks{tasks: map[int]map[string]any{
		501: {"id": float64(501), "title": "Implement retry policy for HTTP client", "description": "- exponential backoff\n- jitter support\n- respect Retry-After header", "project_id": float64(8)},
	}}
	cfg := Config{Instance: "default", Repo: dir}.WithDefaults()
	v := New(cfg, tasks, embedder, store, func(context.Context, string) (string, error) { return diff, nil }, sh)
	return v, sh
}

func TestAnchorsSplitsRequirements(t *testing.T) {
	got := Anchors("Fix the login", "- validate email\n- reject empty password\n")
	if len(got) != 3 || got[0] != "Fix the login" || got[1] != "validate email" {
		t.Fatalf("anchors = %q", got)
	}
}

func TestVerifyOnTopicSemantic(t *testing.T) {
	dim := 8
	e := &fakeEmbedder{dim: dim, vec: map[string][]float32{
		"retry policy": constVec(dim, 1, dim), // anchor matches title keyword
	}}
	s := &fakeStore{}
	// Diff vector identical to the title-anchor vector: semantic sim 1.
	diffVec := constVec(dim, 1, dim)
	e.vec["diff about retry policy and backoff"] = diffVec
	v, _ := newTestVerifier(t, e, s, "diff about retry policy and backoff")

	verdict, err := v.Verify(context.Background(), 501, "diff about retry policy and backoff")
	if err != nil {
		t.Fatal(err)
	}
	if verdict.Drift {
		t.Fatalf("verdict = %+v, want on-topic", verdict)
	}
	if verdict.Anchors != 4 {
		t.Fatalf("anchors = %d, want 4 (title + 3 lines)", verdict.Anchors)
	}
	if verdict.Semantic < 0.99 {
		t.Fatalf("semantic = %v, want ~1", verdict.Semantic)
	}
	if verdict.ShadowID != "" {
		t.Fatalf("on-topic verdict queued shadow task %q", verdict.ShadowID)
	}
}

func TestVerifyDriftQueuesShadowTask(t *testing.T) {
	dim := 8
	e := &fakeEmbedder{dim: dim, vec: map[string][]float32{}}
	s := &fakeStore{}
	// Diff about something entirely different: low similarity, no keyword hits.
	v, sh := newTestVerifier(t, e, s, "refactor the garden irrigation schedule completely unrelated words")
	e.vec["irrigation"] = constVec(dim, 0, 0) // orthogonal to everything

	verdict, err := v.Verify(context.Background(), 501, "refactor the garden irrigation schedule completely unrelated words")
	if err != nil {
		t.Fatal(err)
	}
	if !verdict.Drift {
		t.Fatalf("verdict = %+v, want drift", verdict)
	}
	if verdict.ShadowID == "" {
		t.Fatalf("drift verdict did not queue a shadow task: %+v", verdict)
	}
	entries, _ := sh.ReadAll()
	found := false
	for _, en := range entries {
		if en.Tool == "verify_review" && en.Op == "create" && strings.Contains(string(en.Payload), "Semantic drift on task 501") {
			found = true
		}
	}
	if !found {
		t.Fatalf("shadow journal missing correction entry: %+v", entries)
	}
}

func TestVerifyExactLegSavesOnTopic(t *testing.T) {
	dim := 8
	e := &fakeEmbedder{dim: dim, vec: map[string][]float32{
		"irrigation": constVec(dim, 0, 0), // semantic leg says drift
	}}
	s := &fakeStore{}
	// Diff mentions enough title keywords verbatim → exact leg rescues.
	diff := "implement retry policy for HTTP client: exponential backoff"
	v, _ := newTestVerifier(t, e, s, diff)

	verdict, err := v.Verify(context.Background(), 501, diff)
	if err != nil {
		t.Fatal(err)
	}
	if verdict.Drift {
		t.Fatalf("verdict = %+v, want on-topic via exact leg", verdict)
	}
	if verdict.Exact <= 0 {
		t.Fatalf("exact ratio = %v, want > 0", verdict.Exact)
	}
}

func TestEnsureAnchorsIdempotent(t *testing.T) {
	dim := 8
	v, _ := newTestVerifier(t, &fakeEmbedder{dim: dim}, &fakeStore{}, "")
	n1, err := v.EnsureAnchors(context.Background(), 501)
	if err != nil {
		t.Fatal(err)
	}
	n2, err := v.EnsureAnchors(context.Background(), 501)
	if err != nil {
		t.Fatal(err)
	}
	if n1 != n2 {
		t.Fatalf("anchor counts differ: %d vs %d", n1, n2)
	}
}

func TestGracefulDegradationEmbedderDown(t *testing.T) {
	v, _ := newTestVerifier(t, &fakeEmbedder{fail: true}, &fakeStore{}, "diff")
	if _, err := v.Verify(context.Background(), 501, "diff"); err == nil {
		t.Fatal("expected error when embedder is down")
	}
}

func TestEmptyDiffNoVerdict(t *testing.T) {
	v, _ := newTestVerifier(t, &fakeEmbedder{dim: 8}, &fakeStore{}, "   ")
	verdict, err := v.Verify(context.Background(), 501, "   ")
	if err != nil {
		t.Fatal(err)
	}
	if verdict.Drift {
		t.Fatal("empty diff must not be judged as drift")
	}
}

func TestGitDiffUncommittedThenCommit(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		out, err := execGit(dir, args...)
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	writeFile := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeFile("a.txt", "hello\n")
	run("add", ".")
	run("commit", "-qm", "init")

	// Clean tree → falls back to the last commit patch.
	d, err := GitDiff(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d, "hello") {
		t.Fatalf("fallback diff = %q", d)
	}

	// Dirty tree → uncommitted (staged) diff.
	writeFile("b.txt", "world\n")
	run("add", ".")
	d, err = GitDiff(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d, "world") {
		t.Fatalf("uncommitted diff = %q", d)
	}
}

func TestVerdictJSONShape(t *testing.T) {
	v := Verdict{TaskID: 7, Drift: true, Semantic: 0.1, Exact: 0.0, Reason: "drift", Anchors: 3, ShadowID: "sh-1"}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"task_id":7`, `"drift":true`, `"shadow_id":"sh-1"`} {
		if !strings.Contains(string(b), key) {
			t.Fatalf("json missing %s: %s", key, b)
		}
	}
}
