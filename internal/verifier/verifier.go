// Package verifier implements the Semantic Verifier (card #57, Phase 3):
// it anchors a task's semantic requirements as vectors in Qdrant, then
// compares the git diff of the submitted work against those anchors
// (hybrid: embedding similarity + exact keyword matches). A drifting diff
// spawns a Shadow Task "correction" in the offline journal instead of
// blocking the review. Vikunja and the git repo stay the source of truth.
package verifier

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"

	"viktor/internal/memory"
	"viktor/internal/shadow"
)

// Config carries the verifier settings; they default to the memory
// section (ADR 0003) plus verifier-specific thresholds.
type Config struct {
	Instance     string `json:"instance"`
	Repo         string `json:"repo,omitempty"`
	Collection   string `json:"collection,omitempty"`
	EmbedModel   string `json:"embed_model,omitempty"`
	QdrantURL    string `json:"qdrant_url,omitempty"`
	OllamaURL    string `json:"ollama_url,omitempty"`
	QdrantAPIKey string `json:"qdrant_api_key,omitempty"`
	// Semantic: a diff is on-topic when its best similarity to a task
	// anchor is >= DriftThreshold OR >= ExactThreshold of the anchor
	// keywords appear verbatim in the diff. Below both → drift.
	DriftThreshold float64 `json:"drift_threshold,omitempty"`
	ExactThreshold float64 `json:"exact_threshold,omitempty"`
}

// WithDefaults fills zero fields with built-in defaults.
func (c Config) WithDefaults() Config {
	if c.Collection == "" {
		c.Collection = "viktor_tasks"
	}
	if c.EmbedModel == "" {
		c.EmbedModel = "bge-m3"
	}
	if c.DriftThreshold == 0 {
		// Calibrated on live bge-m3: unrelated technical texts still
		// score ~0.45-0.55 cosine, on-topic diffs score 0.7+.
		c.DriftThreshold = 0.6
	}
	if c.ExactThreshold == 0 {
		c.ExactThreshold = 0.1
	}
	return c
}

// Embedder is the embedding backend (Ollama).
type Embedder interface {
	Embed(ctx context.Context, model string, texts []string) ([][]float32, error)
}

// VectorStore is the Qdrant slice the verifier needs.
type VectorStore interface {
	CollectionExists(ctx context.Context, name string) (bool, error)
	CreateCollection(ctx context.Context, name string, dim int) error
	Upsert(ctx context.Context, name string, points []memory.Point) error
	DeletePoints(ctx context.Context, name string, ids []uint64) error
	SearchPoints(ctx context.Context, name string, vector []float32, k int, filter *memory.SearchFilter) ([]memory.SearchHit, error)
	// ScrollAll lists every point in the collection; the verifier uses
	// it best-effort to spot task chunks whose text already covers an
	// anchor (card #838).
	ScrollAll(ctx context.Context, name string) ([]memory.Point, error)
}

// TaskReader fetches the task to anchor.
type TaskReader interface {
	GetTask(ctx context.Context, taskID int, out any) error
}

// DiffSource produces the diff text for a repo (git under the hood).
type DiffSource func(ctx context.Context, repo string) (string, error)

// Verdict is the outcome of one verification pass.
type Verdict struct {
	TaskID    int     `json:"task_id"`
	Drift     bool    `json:"drift"`
	Semantic  float64 `json:"semantic"`
	Exact     float64 `json:"exact"`
	TopAnchor string  `json:"top_anchor,omitempty"`
	Reason    string  `json:"reason"`
	Anchors   int     `json:"anchors"`
	ShadowID  string  `json:"shadow_id,omitempty"`
	// ChangeRisk is the optional repowise digest (card #823); nil when
	// the repowise section is absent — the output is then byte-identical
	// to pre-#823.
	ChangeRisk *RiskDigest `json:"change_risk,omitempty"`
}

// Verifier checks submitted work against task anchors.
type Verifier struct {
	Cfg      Config
	Tasks    TaskReader
	Embedder Embedder
	Store    VectorStore
	Diff     DiffSource
	Shadow   *shadow.Store
	// Risk is the optional change-risk runner (card #823); nil when the
	// repowise config section is absent — feature off, output unchanged.
	Risk ChangeRisker
	// Now is overridable in tests.
	Now func() time.Time
}

// New builds a verifier from a config and its backends.
func New(cfg Config, tasks TaskReader, embedder Embedder, store VectorStore, diff DiffSource, sh *shadow.Store) *Verifier {
	return &Verifier{Cfg: cfg.WithDefaults(), Tasks: tasks, Embedder: embedder, Store: store, Diff: diff, Shadow: sh, Now: time.Now}
}

const (
	maxAnchors    = 24
	maxAnchorLen  = 300
	diffHeadLimit = 4000
	diffTailLimit = 2000
)

// Anchors splits a task into semantic requirement chunks: one per
// description line/bullet plus the title itself.
func Anchors(title, description string) []string {
	var out []string
	if t := strings.TrimSpace(title); t != "" {
		out = append(out, t)
	}
	for _, line := range strings.Split(description, "\n") {
		line = strings.TrimRight(strings.TrimSpace(line), "`*_")
		line = strings.TrimSpace(strings.TrimLeft(line, "-*•> "))
		if line == "" {
			continue
		}
		if len([]rune(line)) > maxAnchorLen {
			line = string([]rune(line)[:maxAnchorLen])
		}
		out = append(out, line)
		if len(out) >= maxAnchors {
			break
		}
	}
	return out
}

// taskChunkTitles collects normalized titles of the task chunks
// already indexed for the task. Any error yields an empty set, keeping
// all anchors (the duplicate is cosmetic, a missing anchor is not).
func (v *Verifier) taskChunkTitles(ctx context.Context, taskID int) map[string]bool {
	out := map[string]bool{}
	pts, err := v.Store.ScrollAll(ctx, v.Cfg.Collection)
	if err != nil {
		return out
	}
	for _, p := range pts {
		if id, ok := payloadTaskID(p.Payload["task_id"]); !ok || id != taskID {
			continue
		}
		if ct, _ := p.Payload["chunk_type"].(string); ct != "task" {
			continue
		}
		if title, ok := p.Payload["title"].(string); ok {
			out[normAnchorText(title)] = true
		}
	}
	return out
}

// normAnchorText folds case, whitespace and border punctuation so a
// title and its indexed copy compare equal despite markdown noise.
func normAnchorText(s string) string {
	s = strings.ToLower(strings.Join(strings.Fields(s), " "))
	return strings.TrimFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r)
	})
}

func payloadTaskID(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case float64:
		return int(n), true
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return int(i), true
		}
	}
	return 0, false
}

// taskAnchors fetches the task and splits it into anchor texts.
func (v *Verifier) taskAnchors(ctx context.Context, taskID int) ([]string, error) {
	var task map[string]any
	if err := v.Tasks.GetTask(ctx, taskID, &task); err != nil {
		return nil, fmt.Errorf("verifier: fetch task %d: %w", taskID, err)
	}
	title, _ := task["title"].(string)
	desc, _ := task["description"].(string)
	anchors := Anchors(title, desc)
	if len(anchors) == 0 {
		return nil, fmt.Errorf("verifier: task %d has no title to anchor", taskID)
	}
	return anchors, nil
}

// EnsureAnchors (re)generates the anchor points of a task in Qdrant and
// returns how many were stored. Idempotent: existing anchor points for
// the task are replaced.
func (v *Verifier) EnsureAnchors(ctx context.Context, taskID int) (int, error) {
	anchors, err := v.taskAnchors(ctx, taskID)
	if err != nil {
		return 0, err
	}
	return v.ensureAnchorsVecs(ctx, taskID, anchors)
}

// ensureAnchorsVecs embeds the given anchor texts and (re)stores them.
func (v *Verifier) ensureAnchorsVecs(ctx context.Context, taskID int, anchors []string) (int, error) {
	vecs, err := v.Embedder.Embed(ctx, v.Cfg.EmbedModel, anchors)
	if err != nil {
		return 0, fmt.Errorf("verifier: embed anchors: %w", err)
	}
	if len(vecs) != len(anchors) {
		return 0, fmt.Errorf("verifier: embedder returned %d vectors for %d anchors", len(vecs), len(anchors))
	}

	// Card #838: a title anchor whose text is already covered by the
	// memory task chunk shows up twice in the search output with close
	// scores — skip it. Best effort: if the collection can't be read,
	// every anchor is stored as before.
	dup := v.taskChunkTitles(ctx, taskID)

	ok, err := v.Store.CollectionExists(ctx, v.Cfg.Collection)
	if err != nil {
		return 0, fmt.Errorf("verifier: qdrant: %w", err)
	}
	if !ok {
		if err := v.Store.CreateCollection(ctx, v.Cfg.Collection, len(vecs[0])); err != nil {
			return 0, fmt.Errorf("verifier: create collection: %w", err)
		}
	}
	ids := anchorIDs(v.Cfg.Instance, taskID, len(anchors))
	// Delete the full deterministic id range, not just len(anchors): a
	// previous run may have stored more anchors (comment count changed,
	// cap 24), leaving orphans beyond the current count.
	if err := v.Store.DeletePoints(ctx, v.Cfg.Collection, anchorIDs(v.Cfg.Instance, taskID, maxAnchors)); err != nil {
		return 0, fmt.Errorf("verifier: delete stale anchors: %w", err)
	}
	points := make([]memory.Point, 0, len(anchors))
	for i, vec := range vecs {
		if dup[normAnchorText(anchors[i])] {
			continue // textual duplicate of an indexed task chunk
		}
		points = append(points, memory.Point{
			ID:      ids[i],
			Vector:  vec,
			Payload: map[string]any{"task_id": taskID, "chunk_type": "anchor", "snippet": memory.Snippet(anchors[i])},
		})
	}
	if err := v.Store.Upsert(ctx, v.Cfg.Collection, points); err != nil {
		return 0, fmt.Errorf("verifier: upsert anchors: %w", err)
	}
	return len(points), nil
}

// Verify runs the hybrid check: embed the diff, search it against the
// task's anchors (semantic), count exact keyword overlaps, and decide.
// On drift a Shadow Task correction is queued when a shadow store is set.
func (v *Verifier) Verify(ctx context.Context, taskID int, diff string) (Verdict, error) {
	anchors, err := v.taskAnchors(ctx, taskID)
	if err != nil {
		return Verdict{TaskID: taskID}, err
	}
	verdict := Verdict{TaskID: taskID, Anchors: len(anchors)}
	if _, err := v.ensureAnchorsVecs(ctx, taskID, anchors); err != nil {
		return verdict, err
	}
	diff = trimDiff(diff)
	if strings.TrimSpace(diff) == "" {
		verdict.Reason = "empty diff: nothing to verify"
		return verdict, nil
	}

	// Semantic leg: similarity of the diff to the nearest anchor.
	dvec, err := v.Embedder.Embed(ctx, v.Cfg.EmbedModel, []string{diff})
	if err != nil {
		return verdict, fmt.Errorf("verifier: embed diff: %w", err)
	}
	filter := &memory.SearchFilter{Must: []map[string]any{
		{"key": "task_id", "match": map[string]any{"value": taskID}},
		{"key": "chunk_type", "match": map[string]any{"value": "anchor"}},
	}}
	hits, err := v.Store.SearchPoints(ctx, v.Cfg.Collection, dvec[0], 3, filter)
	if err != nil {
		return verdict, fmt.Errorf("verifier: search anchors: %w", err)
	}
	if len(hits) > 0 {
		verdict.Semantic = hits[0].Score
		verdict.TopAnchor, _ = hits[0].Payload["snippet"].(string)
	}

	// Exact leg: how many anchor keywords appear verbatim in the diff.
	verdict.Exact = exactRatio(anchors, diff)

	onTopic := verdict.Semantic >= v.Cfg.DriftThreshold || verdict.Exact >= v.Cfg.ExactThreshold
	verdict.Drift = !onTopic
	switch {
	case verdict.Semantic >= v.Cfg.DriftThreshold:
		verdict.Reason = fmt.Sprintf("on-topic: semantic similarity %.2f >= %.2f", verdict.Semantic, v.Cfg.DriftThreshold)
	case verdict.Exact >= v.Cfg.ExactThreshold:
		verdict.Reason = fmt.Sprintf("on-topic: exact keyword ratio %.2f >= %.2f", verdict.Exact, v.Cfg.ExactThreshold)
	default:
		verdict.Reason = fmt.Sprintf("drift: semantic %.2f < %.2f and exact %.2f < %.2f", verdict.Semantic, v.Cfg.DriftThreshold, verdict.Exact, v.Cfg.ExactThreshold)
	}

	if verdict.Drift && v.Shadow != nil {
		var task map[string]any
		_ = v.Tasks.GetTask(ctx, taskID, &task)
		title, _ := task["title"].(string)
		payload, _ := json.Marshal(map[string]any{
			"project_id": task["project_id"],
			"title":      fmt.Sprintf("Semantic drift on task %d: %s", taskID, title),
			"description": fmt.Sprintf("Semantic Verifier flagged the submitted diff.\n\n%s\n\nsemantic=%.2f exact=%.2f top_anchor=%q",
				verdict.Reason, verdict.Semantic, verdict.Exact, verdict.TopAnchor),
		})
		entry := &shadow.Entry{Tool: "verify_review", Op: "create", ProjectID: int64(asInt(task["project_id"])), Payload: payload}
		if err := v.Shadow.Append(entry); err != nil {
			return verdict, fmt.Errorf("verifier: shadow task: %w", err)
		}
		verdict.ShadowID = entry.ShadowID
	}
	return verdict, nil
}

// CleanupAnchors removes the anchor points of a task (probe hygiene).
func (v *Verifier) CleanupAnchors(ctx context.Context, taskID int) error {
	// Anchors are regenerated per task from its current text; the upper
	// bound maxAnchors bounds the deterministic id range.
	return v.Store.DeletePoints(ctx, v.Cfg.Collection, anchorIDs(v.Cfg.Instance, taskID, maxAnchors))
}

// VerifyRepo diffs the repo (uncommitted changes first, then the last
// commit) and verifies it against the task. When a ChangeRisker is
// configured (repowise section, card #823), a non-blocking risk digest
// is appended to the verdict.
func (v *Verifier) VerifyRepo(ctx context.Context, taskID int, repo string) (Verdict, error) {
	if v.Diff == nil {
		return Verdict{TaskID: taskID}, fmt.Errorf("verifier: no diff source configured")
	}
	diff, err := v.Diff(ctx, repo)
	if err != nil {
		return Verdict{TaskID: taskID}, err
	}
	vd, err := v.Verify(ctx, taskID, diff)
	if err == nil && v.Risk != nil {
		// Empty revspec mirrors GitDiff semantics: uncommitted work
		// first, HEAD when the tree is clean — the CLI decides the same
		// way. The digest never alters the drift fields above.
		vd.ChangeRisk = v.Risk(ctx, repo, "")
	}
	return vd, err
}

func trimDiff(diff string) string {
	runes := []rune(diff)
	if len(runes) <= diffHeadLimit+diffTailLimit {
		return diff
	}
	return string(runes[:diffHeadLimit]) + "\n…\n" + string(runes[len(runes)-diffTailLimit:])
}

// exactRatio is the fraction of anchor keywords (words of 5+ chars)
// present verbatim in the diff.
func exactRatio(anchors []string, diff string) float64 {
	if len(anchors) == 0 {
		return 0
	}
	keywords := map[string]bool{}
	for _, a := range anchors {
		for _, w := range words(a) {
			keywords[strings.ToLower(w)] = true
		}
	}
	if len(keywords) == 0 {
		return 0
	}
	diffLower := strings.ToLower(diff)
	hit := 0
	for k := range keywords {
		if strings.Contains(diffLower, k) {
			hit++
		}
	}
	return float64(hit) / float64(len(keywords))
}

func words(s string) []string {
	var out []string
	var cur strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			cur.WriteRune(r)
			continue
		}
		if cur.Len() >= 5 {
			out = append(out, cur.String())
		}
		cur.Reset()
	}
	if cur.Len() >= 5 {
		out = append(out, cur.String())
	}
	return out
}

func anchorIDs(instance string, taskID, n int) []uint64 {
	ids := make([]uint64, 0, n)
	for i := 0; i < n; i++ {
		ids = append(ids, memory.FNV64a(fmt.Sprintf("%s-anchor-%d-%d", instance, taskID, i)))
	}
	return ids
}

func asInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}
