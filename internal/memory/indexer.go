package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Report summarizes one sync run.
type Report struct {
	Source       string `json:"source"`
	Mode         string `json:"mode"` // "full" or "incremental" (card #821)
	TasksScanned int    `json:"tasks_scanned"`
	ChunksTotal  int    `json:"chunks_total"`
	Upserted     int    `json:"upserted"`
	Deleted      int    `json:"deleted"`
	Skipped      int    `json:"skipped"` // chunks unchanged since last sync
	Commits      int    `json:"commits"` // commit chunks upserted this run
	Cursor       string `json:"cursor"`
	Enriched     int    `json:"enriched"` // tasks with git commit enrichment
}

// Indexer syncs tracker tasks into the vector store.
type Indexer struct {
	cfg       Config
	source    TaskSource
	embedder  Embedder
	store     VectorStore
	git       GitEnricher
	state     *syncState
	statePath string
}

// NewIndexer wires the indexer. statePath may be empty to disable
// persistence (used by tests).
func NewIndexer(cfg Config, source TaskSource, embedder Embedder, store VectorStore, git GitEnricher, statePath string) (*Indexer, error) {
	cfg.normalize()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if statePath == "" {
		return nil, fmt.Errorf("memory: state path is required")
	}
	st, err := loadState(statePath)
	if err != nil {
		return nil, fmt.Errorf("memory: load state: %w", err)
	}
	return &Indexer{
		cfg:       cfg,
		source:    source,
		embedder:  embedder,
		store:     store,
		git:       git,
		state:     st,
		statePath: statePath,
	}, nil
}

const embedBatch = 32

// Sync performs one incremental pass: fetch the tasks updated since the
// stored cursor (full scan on first run), chunk, embed only changed
// chunks, upsert, delete points of vanished entities, persist state. The
// repository history (when a git enricher is wired) is indexed as commit
// chunks alongside the board (ADR 0003: task, comment and commit chunks
// in one collection). A repeat sync with no changes performs zero
// upserts (DoD).
func (ix *Indexer) Sync(ctx context.Context) (*Report, error) {
	return ix.sync(ctx, false)
}

// SyncFull forces an unfiltered rescan of the whole board. Use it to
// clean ghost points: a delta scan cannot see deleted tasks, so their
// points are only removed on complete scans (card #821).
func (ix *Indexer) SyncFull(ctx context.Context) (*Report, error) {
	return ix.sync(ctx, true)
}

func (ix *Indexer) sync(ctx context.Context, full bool) (*Report, error) {
	if err := ix.checkDimension(ctx); err != nil {
		return nil, err
	}
	since := ""
	if !full {
		since = TrimCursor(ix.state.Cursor)
	}
	batch, err := ix.source.AllTasks(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("memory: fetch tasks: %w", err)
	}
	tasks := batch.Tasks

	mode := "incremental"
	if since == "" || batch.Complete {
		mode = "full"
	}
	report := &Report{Source: ix.source.Name(), Mode: mode, TasksScanned: len(tasks)}

	type pending struct {
		chunk Chunk
		id    uint64
	}
	var pendingUpserts []pending
	seenKeys := map[string]bool{}

	// Cursor advances monotonically: an incremental scan that returns
	// nothing must keep the stored cursor, otherwise the next sync would
	// silently degrade to a full scan.
	cursor := TrimCursor(ix.state.Cursor)
	for _, t := range tasks {
		if t.Updated > cursor {
			cursor = t.Updated
		}

		var commits []CommitInfo
		if ix.git != nil && ix.cfg.GitRepo != "" {
			cs, err := ix.git.Commits(ctx, t.ID)
			if err != nil {
				// Enrichment is best-effort; a broken repo must not stop sync.
				commits = nil
			} else {
				commits = cs
			}
		}

		comments, err := ix.source.Comments(ctx, t.ID)
		if err != nil {
			// Comments are best-effort as well: index the task body regardless.
			comments = nil
		}

		for _, chunk := range ChunkTask(t, comments, shas(commits)) {
			seenKeys[chunk.Key] = true
			if chunk.Type == ChunkTypeTask && len(commits) > 0 {
				chunk.Payload["files"] = touchedFiles(commits)
				report.Enriched++
			}
			h := hashText(chunk.Text)
			if ix.state.Hashes[chunk.Key] == h {
				report.Skipped++
				continue
			}
			pendingUpserts = append(pendingUpserts, pending{chunk: chunk, id: pointID(ix.cfg.Instance, chunk.Key)})
		}
	}

	// Commit chunks: every commit of the repo becomes a chunk, so search
	// can answer "why" questions from commit messages, not only the board.
	// Commits are immutable, hence hash-compare makes repeat syncs free.
	if cs, ok := ix.git.(CommitSource); ok && cs != nil {
		commits, err := cs.AllCommits(ctx)
		if err != nil {
			// History indexing is best-effort: the board projection must
			// not fail because of a broken repo.
			commits = nil
		}
		for _, c := range commits {
			chunk := ChunkCommit(c)
			seenKeys[chunk.Key] = true
			h := hashText(chunk.Text)
			if ix.state.Hashes[chunk.Key] == h {
				report.Skipped++
				continue
			}
			report.Commits++
			pendingUpserts = append(pendingUpserts, pending{chunk: chunk, id: pointID(ix.cfg.Instance, chunk.Key)})
		}
	}

	// Points of tasks that disappeared from the scan are removed so the
	// index never outlives the tracker (deleted tasks leave no ghosts).
	// Only complete scans may delete: on a delta scan an absent task is
	// "unchanged", not "deleted" — its point would be a false ghost.
	if batch.Complete {
		var doomed []uint64
		for key := range ix.state.Hashes {
			if !seenKeys[key] {
				doomed = append(doomed, pointID(ix.cfg.Instance, key))
				delete(ix.state.Hashes, key)
			}
		}
		if len(doomed) > 0 {
			if err := ix.store.DeletePoints(ctx, ix.cfg.Collection, doomed); err != nil {
				return nil, fmt.Errorf("memory: delete stale points: %w", err)
			}
			report.Deleted = len(doomed)
		}
	}

	if len(pendingUpserts) > 0 {
		if err := ix.ensureCollection(ctx, pendingUpserts[0].chunk.Text); err != nil {
			return nil, err
		}
		for start := 0; start < len(pendingUpserts); start += embedBatch {
			end := start + embedBatch
			if end > len(pendingUpserts) {
				end = len(pendingUpserts)
			}
			batch := pendingUpserts[start:end]
			texts := make([]string, len(batch))
			for i, p := range batch {
				texts[i] = p.chunk.Text
			}
			vectors, err := ix.embedder.Embed(ctx, ix.cfg.EmbedModel, texts)
			if err != nil {
				return nil, fmt.Errorf("memory: embed: %w", err)
			}
			points := make([]Point, len(batch))
			for i, p := range batch {
				points[i] = Point{ID: p.id, Vector: vectors[i], Payload: p.chunk.Payload}
			}
			if err := ix.store.Upsert(ctx, ix.cfg.Collection, points); err != nil {
				return nil, fmt.Errorf("memory: upsert: %w", err)
			}
			report.Upserted += len(points)
			for _, p := range batch {
				ix.state.Hashes[p.chunk.Key] = hashText(p.chunk.Text)
			}
		}
	}

	report.ChunksTotal = len(seenKeys)
	report.Cursor = cursor
	ix.state.Cursor = cursor

	// Only persist state after every remote write succeeded, so a failed
	// sync retries the same upserts next run.
	if err := ix.state.save(ix.statePath); err != nil {
		return nil, fmt.Errorf("memory: save state: %w", err)
	}
	return report, nil
}

// probeText is the fixed sample embedded once per sync to learn the
// model's output dimension (card #822).
const probeText = "memory dimension probe"

// checkDimension fails fast when the embed model's output size does not
// match the existing collection (card #822). Without the probe the
// mismatch surfaced mid-run as an opaque Qdrant 400 "Vector dimension
// error", after comments were fetched and partial work possibly done.
// A missing collection is fine: ensureCollection creates it with the
// probed dimension.
func (ix *Indexer) checkDimension(ctx context.Context) error {
	dim, found, err := ix.store.CollectionDim(ctx, ix.cfg.Collection)
	if err != nil {
		return fmt.Errorf("memory: read collection dim: %w", err)
	}
	if !found {
		return nil
	}
	vectors, err := ix.embedder.Embed(ctx, ix.cfg.EmbedModel, []string{probeText})
	if err != nil {
		return fmt.Errorf("memory: embed probe: %w", err)
	}
	if len(vectors) == 0 || len(vectors[0]) == 0 {
		return fmt.Errorf("memory: embedder returned empty vector")
	}
	modelDim := len(vectors[0])
	if modelDim != dim {
		return fmt.Errorf(
			"memory: embed model %q produces dim %d, collection %q has dim %d — switch the embed model (VIKTOR_MEMORY_EMBED_MODEL / config memory.embed_model) or recreate the collection (drop it, then run `viktor memory sync -full`)",
			ix.cfg.EmbedModel, modelDim, ix.cfg.Collection, dim)
	}
	return nil
}

// ensureCollection creates the collection on first upsert, deriving the
// vector dimension from the embedder instead of hardcoding it.
func (ix *Indexer) ensureCollection(ctx context.Context, sampleText string) error {
	exists, err := ix.store.CollectionExists(ctx, ix.cfg.Collection)
	if err != nil {
		return fmt.Errorf("memory: check collection: %w", err)
	}
	if exists {
		return nil
	}
	vectors, err := ix.embedder.Embed(ctx, ix.cfg.EmbedModel, []string{sampleText})
	if err != nil {
		return fmt.Errorf("memory: probe dimension: %w", err)
	}
	if len(vectors) == 0 || len(vectors[0]) == 0 {
		return fmt.Errorf("memory: embedder returned empty vector")
	}
	if err := ix.store.CreateCollection(ctx, ix.cfg.Collection, len(vectors[0])); err != nil {
		return fmt.Errorf("memory: create collection: %w", err)
	}
	return nil
}

// Status returns index freshness info for memory_status.
type Status struct {
	Collection    string `json:"collection"`
	Points        int64  `json:"points"`
	Cursor        string `json:"cursor"`
	EmbedModel    string `json:"embed_model"`
	State         string `json:"state"`
	CollectionDim int    `json:"collection_dim,omitempty"` // 0 = no collection
	DimVerdict    string `json:"dim_verdict,omitempty"`    // ok | mismatch | unknown
}

// Status reads the current index state. When the collection exists, it
// also reports the dimension verdict: the configured embed model is
// probed once and compared against the collection's vector size, so a
// model/collection mismatch is visible before the next sync fails on it
// (card #822).
func (ix *Indexer) Status(ctx context.Context) (*Status, error) {
	st := &Status{
		Collection: ix.cfg.Collection,
		Cursor:     ix.state.Cursor,
		EmbedModel: ix.cfg.EmbedModel,
	}
	count, err := ix.store.Count(ctx, ix.cfg.Collection)
	if err != nil {
		st.State = "unavailable: " + err.Error()
	} else {
		st.Points = count
		st.State = "ok"
	}
	if st.Cursor == "" && len(ix.state.Hashes) == 0 {
		st.State = "not synced yet"
	}
	dim, found, err := ix.store.CollectionDim(ctx, ix.cfg.Collection)
	if err != nil || !found {
		return st, nil
	}
	st.CollectionDim = dim
	vectors, err := ix.embedder.Embed(ctx, ix.cfg.EmbedModel, []string{probeText})
	switch {
	case err != nil:
		st.DimVerdict = "unknown"
	case len(vectors) == 0 || len(vectors[0]) == 0:
		st.DimVerdict = "unknown"
	case len(vectors[0]) == dim:
		st.DimVerdict = "ok"
	default:
		st.DimVerdict = "mismatch"
	}
	return st, nil
}

// TrimCursor normalizes a Vikunja timestamp for comparisons; empty or
// zero-value dates are treated as "".
func TrimCursor(s string) string {
	if strings.HasPrefix(s, "0001-01-01") {
		return ""
	}
	return strings.TrimSpace(s)
}

// DoctorReport is the result of one memory doctor run (card #837).
type DoctorReport struct {
	DryRun       bool             `json:"dry_run"`
	TotalPoints  int              `json:"total_points"`
	OrphanPoints int              `json:"orphan_points"`
	ByChunkType  map[string]int   `json:"by_chunk_type"`        // chunk_type -> orphan count
	OrphanIDs    []uint64         `json:"orphan_ids,omitempty"` // all orphan point ids
	Samples      []map[string]any `json:"samples,omitempty"`    // up to 5 orphan payloads
	Deleted      int              `json:"deleted"`              // points removed (prune only)
	Cursor       string           `json:"cursor"`               // sync cursor age reference
}

// maxDoctorSamples caps the payload previews in the doctor report.
const maxDoctorSamples = 5

// Doctor sweeps the collection for orphan points: task/comment chunks
// whose task_id no longer exists on the tracker and commit chunks whose
// entire task_ids list is gone. With prune the orphans are deleted;
// without it the report is read-only (dry run).
func (ix *Indexer) Doctor(ctx context.Context, prune bool) (*DoctorReport, error) {
	rep := &DoctorReport{DryRun: !prune, ByChunkType: map[string]int{}, Cursor: ix.state.Cursor}

	// Live task ids from the source (full scan, no cursor).
	live := map[int64]bool{}
	batch, err := ix.source.AllTasks(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("doctor: fetch live tasks: %w", err)
	}
	for _, t := range batch.Tasks {
		live[t.ID] = true
	}
	// Shadow-mirror fallback batches may page; take what we got — the
	// doctor compares against what the source reports right now.

	points, err := ix.store.ScrollAll(ctx, ix.cfg.Collection)
	if err != nil {
		return nil, fmt.Errorf("doctor: scroll collection: %w", err)
	}
	rep.TotalPoints = len(points)

	var orphanIDs []uint64
	for _, p := range points {
		if !isOrphan(p.Payload, live) {
			continue
		}
		orphanIDs = append(orphanIDs, p.ID)
		ct, _ := p.Payload["chunk_type"].(string)
		if ct == "" {
			ct = "unknown"
		}
		rep.ByChunkType[ct]++
		if len(rep.Samples) < maxDoctorSamples {
			rep.Samples = append(rep.Samples, p.Payload)
		}
	}
	rep.OrphanPoints = len(orphanIDs)
	rep.OrphanIDs = orphanIDs

	if prune && len(orphanIDs) > 0 {
		if err := ix.store.DeletePoints(ctx, ix.cfg.Collection, orphanIDs); err != nil {
			return nil, fmt.Errorf("doctor: delete orphans: %w", err)
		}
		rep.Deleted = len(orphanIDs)
	}
	return rep, nil
}

// isOrphan decides for one point payload whether every task it is tied
// to has disappeared from the tracker.
func isOrphan(payload map[string]any, live map[int64]bool) bool {
	if v, ok := payload["task_id"]; ok {
		id, ok := toInt64(v)
		return ok && !live[id]
	}
	if v, ok := payload["task_ids"].([]any); ok {
		if len(v) == 0 {
			return false
		}
		for _, e := range v {
			if id, ok := toInt64(e); ok && live[id] {
				return false
			}
		}
		return true
	}
	// No join key (e.g. commit with no task references) — keep it.
	return false
}

// toInt64 coerces a JSON-decoded number (float64) or int to int64.
func toInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case int:
		return int64(n), true
	case int64:
		return n, true
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	default:
		return 0, false
	}
}
