package memory

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// SearchOpts narrows a semantic search.
type SearchOpts struct {
	ProjectID int64    // 0 = all projects
	Labels    []string // all listed labels must match (payload.labels)
	K         int      // top-k, default 5
}

// Hit is one search result row (tool output shape).
type Hit struct {
	TaskID    int64    `json:"task_id,omitempty"`
	ChunkType string   `json:"chunk_type"`
	Score     float64  `json:"score"`
	Title     string   `json:"title"`
	Snippet   string   `json:"snippet,omitempty"`
	Status    string   `json:"status,omitempty"`
	Labels    []string `json:"labels,omitempty"`
	Commits   []string `json:"commits,omitempty"` // task chunks: commits touching the task
	SHA       string   `json:"sha,omitempty"`     // commit chunks
	TaskIDs   []int64  `json:"task_ids,omitempty"`
	ProjectID int64    `json:"project_id,omitempty"`
}

// Searcher answers semantic queries against the index. It is read-only:
// the agent may search memory, never mutate it (ADR 0003).
type Searcher struct {
	cfg      Config
	embedder Embedder
	store    VectorSearcher
}

// VectorSearcher is the read side of the vector store.
type VectorSearcher interface {
	SearchPoints(ctx context.Context, name string, vector []float32, k int, filter *searchFilter) ([]SearchHit, error)
}

// NewSearcher wires a read-only search path.
func NewSearcher(cfg Config, embedder Embedder, store VectorSearcher) (*Searcher, error) {
	cfg.normalize()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Searcher{cfg: cfg, embedder: embedder, store: store}, nil
}

// Search embeds the query and returns the top-k chunks.
func (s *Searcher) Search(ctx context.Context, query string, opts SearchOpts) ([]Hit, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("memory: query is required")
	}
	if opts.K <= 0 {
		opts.K = 5
	}
	vectors, err := s.embedder.Embed(ctx, s.cfg.EmbedModel, []string{query})
	if err != nil {
		return nil, fmt.Errorf("memory: embed query: %w", err)
	}
	if len(vectors) == 0 {
		return nil, fmt.Errorf("memory: embedder returned no vector")
	}
	var filter *searchFilter
	if opts.ProjectID != 0 || len(opts.Labels) > 0 {
		filter = &searchFilter{}
		if opts.ProjectID != 0 {
			filter.Must = append(filter.Must, map[string]any{
				"key": "project_id", "match": map[string]any{"value": opts.ProjectID},
			})
		}
		for _, l := range opts.Labels {
			if l == "" {
				continue
			}
			filter.Must = append(filter.Must, map[string]any{
				"key": "labels", "match": map[string]any{"any": []string{l}},
			})
		}
	}
	raw, err := s.store.SearchPoints(ctx, s.cfg.Collection, vectors[0], opts.K, filter)
	if err != nil {
		return nil, err
	}
	hits := make([]Hit, 0, len(raw))
	for _, h := range raw {
		hits = append(hits, toHit(h))
	}
	return hits, nil
}

func toHit(h SearchHit) Hit {
	out := Hit{Score: h.Score, ChunkType: "task"}
	getInt := func(key string) int64 {
		switch v := h.Payload[key].(type) {
		case float64:
			return int64(v)
		case int64:
			return v
		case int:
			return int64(v)
		}
		return 0
	}
	getStr := func(key string) string {
		if v, ok := h.Payload[key].(string); ok {
			return v
		}
		return ""
	}
	out.TaskID = getInt("task_id")
	if ct, ok := h.Payload["chunk_type"].(string); ok && ct != "" {
		out.ChunkType = ct
	}
	out.Title = getStr("title")
	out.Snippet = getStr("snippet")
	out.Status = getStr("status")
	out.SHA = getStr("sha")
	out.ProjectID = getInt("project_id")
	if labels, ok := h.Payload["labels"].([]any); ok {
		for _, l := range labels {
			if s, ok := l.(string); ok {
				out.Labels = append(out.Labels, s)
			}
		}
	}
	if commits, ok := h.Payload["commits"].([]any); ok {
		for _, c := range commits {
			if s, ok := c.(string); ok {
				out.Commits = append(out.Commits, s)
			}
		}
	}
	if ids, ok := h.Payload["task_ids"].([]any); ok {
		for _, v := range ids {
			if f, ok := v.(float64); ok {
				out.TaskIDs = append(out.TaskIDs, int64(f))
			}
		}
	}
	return out
}

// ErrMemoryUnavailable reports that the memory backend (Qdrant/Ollama)
// is down: tools degrade to {supported:false} instead of failing (ADR
// 0003 graceful degradation, same shape as comment_recent).
type ErrMemoryUnavailable struct{ Cause string }

func (e *ErrMemoryUnavailable) Error() string { return "memory unavailable: " + e.Cause }

// ClassifyUnavailable maps transport-level failures to the degradation
// signal; HTTP-level errors from Qdrant also count as "backend down"
// for the tool contract.
func ClassifyUnavailable(err error) error {
	if err == nil {
		return nil
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return &ErrMemoryUnavailable{Cause: urlErr.Error()}
	}
	msg := err.Error()
	if strings.Contains(msg, "memory: qdrant") || strings.Contains(msg, "memory: embed") {
		return &ErrMemoryUnavailable{Cause: msg}
	}
	return err
}
