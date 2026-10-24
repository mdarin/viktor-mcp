package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"viktor/internal/history"
)

// SearchHistoryIn is the search_history input (card #867): a lexical
// BM25 query over task/comment history in ParadeDB. Use it when the
// semantic memory_search is too fuzzy — exact error codes, identifiers,
// variable names, task ids.
type SearchHistoryIn struct {
	// Query is the required search text (e.g. an error code or a
	// symbol name).
	Query string `json:"query"`
	// ProjectID restricts results to one project.
	ProjectID int64 `json:"project_id,omitempty"`
	// TaskID restricts results to one task's docs.
	TaskID int64 `json:"task_id,omitempty"`
	// Kind filters the doc kind: "task" (title+description) or
	// "comment".
	Kind string `json:"kind,omitempty"`
	// Limit caps the hits (default 20, max 100).
	Limit int `json:"limit,omitempty"`
	// Instance selects the tracker instance.
	Instance string `json:"instance,omitempty"`
}

// registerHistory installs the search_history tool (card #867, Phase 2
// of the "Очереди 2" concept): BM25 lexical search over the tracker
// history indexed into ParadeDB by `viktor history sync`. Without a
// configured section, or with the database down, the tool answers
// {supported:false} — the same degradation contract as reflection and
// memory.
func registerHistory(r *Registry) {
	add[SearchHistoryIn](r, "search_history", "Lexical (BM25) search over tracker history (task titles/descriptions and comments) indexed into ParadeDB by `viktor history sync`. Use for exact matches semantic memory_search is too fuzzy for: error codes, identifiers, variable names, task ids. Degrades to {supported:false} when no history DSN is configured or the database is down", handleSearchHistory(r))
}

// historyFor resolves the history store for an instance: an injected
// store (tests) first, then a cached PG store built from the config.
func (r *Registry) historyFor(instance string) (history.Store, bool) {
	r.mu.RLock()
	cfgs := r.histCfgs
	stores := r.histStores
	active := r.active
	r.mu.RUnlock()
	if instance == "" {
		instance = active
	}
	if s, ok := stores[instance]; ok && s != nil {
		return s, true
	}
	cfg, ok := cfgs[instance]
	if !ok || cfg.PGDSN == "" {
		return nil, false
	}
	s, err := history.NewPG(cfg)
	if err != nil {
		return nil, false
	}
	r.mu.Lock()
	if r.histStores == nil {
		r.histStores = map[string]history.Store{}
	}
	r.histStores[instance] = s
	r.mu.Unlock()
	return s, true
}

// SetHistoryConfigs installs per-instance history settings
// (search_history, card #867).
func (r *Registry) SetHistoryConfigs(cfgs map[string]history.Config) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.histCfgs = cfgs
}

// SetHistoryStore injects a store directly (tests); it overrides the
// config-derived PG store for that instance.
func (r *Registry) SetHistoryStore(instance string, s history.Store) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.histStores == nil {
		r.histStores = map[string]history.Store{}
	}
	r.histStores[instance] = s
}

func degradeHistory(err error) map[string]any {
	var un *history.ErrUnavailable
	if errors.As(history.ClassifyUnavailable(err), &un) {
		return map[string]any{"supported": false, "message": un.Error()}
	}
	return nil
}

func handleSearchHistory(r *Registry) mcp.ToolHandlerFor[SearchHistoryIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in SearchHistoryIn) (*mcp.CallToolResult, any, error) {
		if strings.TrimSpace(in.Query) == "" {
			return nil, nil, fmt.Errorf("search history: query is required")
		}
		in.Kind = strings.TrimSpace(in.Kind)
		if in.Kind != "" && in.Kind != history.KindTask && in.Kind != history.KindComment {
			return nil, nil, fmt.Errorf("search history: kind must be %q or %q", history.KindTask, history.KindComment)
		}
		store, ok := r.historyFor(in.Instance)
		if !ok {
			return nil, notConfigured(in.Instance), nil
		}
		hits, mode, err := store.Search(ctx, history.Query{
			Text:      strings.TrimSpace(in.Query),
			Instance:  in.Instance,
			ProjectID: in.ProjectID,
			TaskID:    in.TaskID,
			Kind:      in.Kind,
			Limit:     in.Limit,
		})
		if err != nil {
			if row := degradeHistory(err); row != nil {
				return nil, row, nil
			}
			return nil, nil, fmt.Errorf("search history: %w", err)
		}
		out := map[string]any{
			"supported": true,
			"query":     strings.TrimSpace(in.Query),
			"mode":      mode,
			"hits":      hits,
			"count":     len(hits),
		}
		if mode == "ilike" {
			out["note"] = "pg_search extension unavailable: degraded to ILIKE substring matching"
		}
		return nil, out, nil
	}
}
