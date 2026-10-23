package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"viktor/internal/memory"
	"viktor/internal/shadow"
)

// Memory tools (ADR 0003): read-only semantic projection of the tracker
// and repo history. The agent may search memory, never mutate it — the
// only writer is `viktor memory sync`. When no memory section is
// configured for the instance, tools answer {supported:false} instead
// of failing; the same shape is used when Qdrant/Ollama are down.

type MemorySearchIn struct {
	Query     string `json:"query"`
	ProjectID int    `json:"project_id,omitempty"`
	Labels    string `json:"labels,omitempty"` // comma-separated, all must match
	Repo      string `json:"repo,omitempty"`   // chunk source repo stamp (card #843)
	K         int    `json:"k,omitempty"`
	Instance  string `json:"instance,omitempty"`
}

type MemoryStatusIn struct {
	Instance string `json:"instance,omitempty"`
}

func registerMemory(r *Registry) {
	add[MemorySearchIn](r, "memory_search", "Semantic search over project memory (Vikunja tasks, comments, repo commits). Use for 'why/зачем' questions and historical context; returns task_id/title/snippet/status/commits/chunk_type/repo; optional repo narrows to one indexed repo", handleMemorySearch(r))
	add[MemoryStatusIn](r, "memory_status", "Semantic memory index status: points, cursor, embed model", handleMemoryStatus(r))
}

// notConfigured is the degradation row when the instance has no memory
// section (config lands with the config card; env/CLI config in M1).
func notConfigured(instance string) map[string]any {
	return map[string]any{
		"supported": false,
		"message":   fmt.Sprintf("memory not configured for instance %q: add the memory section to the config or run `viktor memory sync` with VIKTOR_MEMORY_* env", instance),
	}
}

func (r *Registry) memoryFor(instance string) (memory.Config, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.memoryCfgs == nil {
		return memory.Config{}, false
	}
	if instance == "" {
		instance = r.active
	}
	cfg, ok := r.memoryCfgs[instance]
	return cfg, ok
}

// SetMemoryConfigs installs per-instance memory settings for the
// memory_search/memory_status tools.
func (r *Registry) SetMemoryConfigs(cfgs map[string]memory.Config) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.memoryCfgs = cfgs
}

func degrade(err error) map[string]any {
	var un *memory.ErrMemoryUnavailable
	if errors.As(memory.ClassifyUnavailable(err), &un) {
		return map[string]any{"supported": false, "message": un.Error()}
	}
	return nil
}

func handleMemorySearch(r *Registry) mcp.ToolHandlerFor[MemorySearchIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in MemorySearchIn) (*mcp.CallToolResult, any, error) {
		cfg, ok := r.memoryFor(in.Instance)
		if !ok {
			return nil, notConfigured(in.Instance), nil
		}
		searcher, err := memory.NewSearcher(cfg.WithDefaults(), memory.NewOllama(cfg.OllamaURL), memory.NewQdrant(cfg.QdrantURL, cfg.QdrantAPIKey))
		if err != nil {
			return nil, nil, err
		}
		opts := memory.SearchOpts{ProjectID: int64(in.ProjectID), Repo: in.Repo, K: in.K}
		opts.Labels = append(opts.Labels, splitCSV(in.Labels)...)
		hits, err := searcher.Search(ctx, in.Query, opts)
		if err != nil {
			if row := degrade(err); row != nil {
				return nil, row, nil
			}
			return nil, nil, fmt.Errorf("memory search: %w", err)
		}
		return nil, map[string]any{
			"supported": true,
			"query":     in.Query,
			"hits":      hits,
			"count":     len(hits),
		}, nil
	}
}

func handleMemoryStatus(r *Registry) mcp.ToolHandlerFor[MemoryStatusIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in MemoryStatusIn) (*mcp.CallToolResult, any, error) {
		cfg, ok := r.memoryFor(in.Instance)
		if !ok {
			return nil, notConfigured(in.Instance), nil
		}
		cfg = cfg.WithDefaults()
		instance := cfg.Instance
		if instance == "" {
			instance = "default"
		}
		// Doctor (ghost sweep) inside Status needs live task ids: a
		// nopSource reports zero live tasks and flags every point as an
		// orphan (QA follow-up, memory_status wave). Wire the real
		// client + shadow store like the memory CLI does; fall back to
		// nopSource only when no client is registered for the instance.
		var src memory.TaskSource = nopSource{}
		if api, aerr := r.clientFor(in.Instance); aerr == nil && api != nil {
			if store, serr := shadow.NewStore("", instance); serr == nil {
				src = memory.NewSource(api, store)
			}
		}
		ix, err := memory.NewIndexer(cfg, src, memory.NewOllama(cfg.OllamaURL), memory.NewQdrant(cfg.QdrantURL, cfg.QdrantAPIKey), nil, memory.DefaultStatePath(instance))
		if err != nil {
			return nil, nil, err
		}
		st, err := ix.Status(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("memory status: %w", err)
		}
		return nil, map[string]any{
			"supported":       true,
			"collection":      st.Collection,
			"points":          st.Points,
			"cursor":          st.Cursor,
			"embed_model":     st.EmbedModel,
			"state":           st.State,
			"collection_dim":  st.CollectionDim,
			"dim_verdict":     st.DimVerdict,
			"last_full_sync":  st.LastFullSync,
			"full_sync_age":   st.FullSyncAge,
			"full_sync_stale": st.FullSyncStale,
			"ghosts":          st.Ghosts,
		}, nil
	}
}

// nopSource exists only to satisfy NewIndexer for read-only status.
type nopSource struct{}

func (nopSource) AllTasks(context.Context, string) (memory.TaskBatch, error) {
	return memory.TaskBatch{Complete: true}, nil
}
func (nopSource) Comments(context.Context, int64) ([]memory.Comment, error) {
	return nil, nil
}
func (nopSource) Name() string { return "none" }

func splitCSV(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
