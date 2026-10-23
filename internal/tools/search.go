package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/sync/errgroup"

	"viktor/internal/client"
	"viktor/internal/memory"
)

type SearchAllIn struct {
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
}

type SearchAllTasksIn struct {
	Query            string `json:"query"`
	ProjectID        int    `json:"project_id,omitempty"`
	IncludeCompleted bool   `json:"include_completed,omitempty"`
	Limit            int    `json:"limit,omitempty"`
}

func searchLimit(limit int) int {
	if limit <= 0 {
		return 10
	}
	if limit > 50 {
		return 50
	}
	return limit
}

func handleSearchAll(r *Registry) mcp.ToolHandlerFor[SearchAllIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in SearchAllIn) (*mcp.CallToolResult, any, error) {
		if strings.TrimSpace(in.Query) == "" {
			return nil, nil, fmt.Errorf("query is required (empty search returns everything)")
		}
		limit := searchLimit(in.Limit)

		var projects, tasks, labels, teams []map[string]any
		var pErr, tErr, lErr, teamErr error
		g, gctx := errgroup.WithContext(ctx)
		g.Go(func() error {
			pErr = r.api().ListProjects(gctx, client.ListParams{Search: in.Query}, &projects)
			return nil
		})
		g.Go(func() error {
			tErr = r.api().ListTasks(gctx, client.ListParams{Search: in.Query}, &tasks)
			return nil
		})
		g.Go(func() error {
			lErr = r.api().ListLabels(gctx, client.ListParams{Search: in.Query}, &labels)
			return nil
		})
		g.Go(func() error {
			teamErr = r.api().ListTeams(gctx, in.Query, &teams)
			return nil
		})
		if err := g.Wait(); err != nil {
			return nil, nil, err
		}
		failures := []map[string]any{}
		for name, err := range map[string]error{"projects": pErr, "tasks": tErr, "labels": lErr, "teams": teamErr} {
			if err != nil {
				failures = append(failures, map[string]any{"section": name, "error": err.Error()})
			}
		}
		if projects == nil {
			projects = []map[string]any{}
		}
		if tasks == nil {
			tasks = []map[string]any{}
		}
		if labels == nil {
			labels = []map[string]any{}
		}
		if teams == nil {
			teams = []map[string]any{}
		}
		projOut := []map[string]any{}
		for i, p := range projects {
			if i >= limit {
				break
			}
			projOut = append(projOut, map[string]any{"id": p["id"], "title": p["title"], "description": p["description"]})
		}
		taskOut := []map[string]any{}
		for i, t := range tasks {
			if i >= limit {
				break
			}
			taskOut = append(taskOut, map[string]any{"id": t["id"], "title": t["title"], "project_id": t["project_id"], "done": t["done"], "due_date": t["due_date"]})
		}
		labelOut := []map[string]any{}
		for i, l := range labels {
			if i >= limit {
				break
			}
			labelOut = append(labelOut, map[string]any{"id": l["id"], "title": l["title"]})
		}
		teamOut := []map[string]any{}
		for i, tm := range teams {
			if i >= limit {
				break
			}
			teamOut = append(teamOut, map[string]any{"id": tm["id"], "name": tm["name"]})
		}
		return nil, map[string]any{
			"query":    in.Query,
			"projects": projOut,
			"tasks":    taskOut,
			"labels":   labelOut,
			"teams":    teamOut,
			"counts": map[string]any{
				"projects": len(projOut),
				"tasks":    len(taskOut),
				"labels":   len(labelOut),
				"teams":    len(teamOut),
			},
			"errors": failures,
		}, nil
	}
}

func handleSearchAllTasks(r *Registry) mcp.ToolHandlerFor[SearchAllTasksIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in SearchAllTasksIn) (*mcp.CallToolResult, any, error) {
		if strings.TrimSpace(in.Query) == "" {
			return nil, nil, fmt.Errorf("query is required (empty search returns everything)")
		}
		p := client.ListParams{Search: in.Query}
		if in.ProjectID != 0 {
			p.Filter = fmt.Sprintf("project_id = %d", in.ProjectID)
		} else if !in.IncludeCompleted {
			p.Filter = "done = false"
		}
		p.PerPage = searchLimit(in.Limit)
		var tasks []map[string]any
		if err := r.api().ListTasks(ctx, p, &tasks); err != nil {
			return nil, nil, err
		}
		if tasks == nil {
			tasks = []map[string]any{}
		}
		out := []map[string]any{}
		for _, t := range tasks {
			out = append(out, trimTask(t))
		}
		return nil, map[string]any{"query": in.Query, "tasks": out, "count": len(out)}, nil
	}
}

// SearchHybridIn drives the hybrid search (card #824): exact list
// filtering (like search_all_tasks) merged with top-K semantic hits
// from the memory index (source=memory). Memory problems degrade to a
// note, never to an error — the exact half must survive.
type SearchHybridIn struct {
	Query            string `json:"query"`
	ProjectID        int    `json:"project_id,omitempty"`
	IncludeCompleted bool   `json:"include_completed,omitempty"`
	Limit            int    `json:"limit,omitempty"`      // total rows, default 10, max 50
	SemanticK        int    `json:"semantic_k,omitempty"` // memory top-K, default 5, max 10
	Instance         string `json:"instance,omitempty"`
}

func semanticK(k int) int {
	if k <= 0 {
		return 5
	}
	if k > 10 {
		return 10
	}
	return k
}

func handleSearchHybrid(r *Registry) mcp.ToolHandlerFor[SearchHybridIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in SearchHybridIn) (*mcp.CallToolResult, any, error) {
		if strings.TrimSpace(in.Query) == "" {
			return nil, nil, fmt.Errorf("query is required (empty search returns everything)")
		}
		limit := searchLimit(in.Limit)
		k := semanticK(in.SemanticK)

		// Exact half: the same list query search_all_tasks runs, sliced
		// to the total limit client-side (Vikunja's per_page is a hint,
		// the fuzzy search may span many pages).
		p := client.ListParams{Search: in.Query}
		if in.ProjectID != 0 {
			p.Filter = fmt.Sprintf("project_id = %d", in.ProjectID)
		} else if !in.IncludeCompleted {
			p.Filter = "done = false"
		}
		p.PerPage = limit
		var tasks []map[string]any
		if err := r.api().ListTasks(ctx, p, &tasks); err != nil {
			return nil, nil, err
		}
		if tasks == nil {
			tasks = []map[string]any{}
		}

		exactIDs := map[float64]bool{}
		for _, t := range tasks {
			if id, ok := t["id"].(float64); ok {
				exactIDs[id] = true
			}
		}

		// Semantic half: best effort (card #824) — anything short of a
		// healthy index degrades to a note. Memory-only hits get their
		// slots reserved first: the tracker's fuzzy search routinely
		// returns 100+ loose matches, which would otherwise crowd the
		// semantic rows out of every broad query.
		semantic := map[string]any{"used": false, "k": k}
		memRows := []map[string]any{}
		cfg, ok := r.memoryFor(in.Instance)
		if !ok {
			semantic["note"] = "memory not configured; exact results only"
		} else {
			searcher, err := memory.NewSearcher(cfg.WithDefaults(), memory.NewOllama(cfg.OllamaURL), memory.NewQdrant(cfg.QdrantURL, cfg.QdrantAPIKey))
			if err != nil {
				semantic["note"] = "memory unavailable: " + err.Error()
			} else {
				hits, err := searcher.Search(ctx, in.Query, memory.SearchOpts{ProjectID: int64(in.ProjectID), K: k})
				if err != nil {
					semantic["note"] = "memory unavailable: " + err.Error()
				} else if len(hits) == 0 {
					semantic["note"] = "memory index has no hits; exact results only"
				} else {
					semantic["used"] = true
					for _, h := range hits {
						if h.TaskID == 0 || exactIDs[float64(h.TaskID)] || len(memRows) >= limit {
							continue // commit chunks and duplicates are dropped
						}
						exactIDs[float64(h.TaskID)] = true
						title := h.Title
						if title == "" {
							// anchor chunks (verifier) carry no title —
							// show the snippet head instead.
							title = h.Snippet
							if len(title) > 80 {
								title = title[:80] + "…"
							}
						}
						memRows = append(memRows, map[string]any{
							"id":         h.TaskID,
							"title":      title,
							"project_id": h.ProjectID,
							"status":     h.Status,
							"score":      h.Score,
							"snippet":    h.Snippet,
							"chunk_type": h.ChunkType,
							"source":     "memory",
						})
					}
				}
			}
		}

		rows := []map[string]any{}
		seen := map[float64]bool{}
		exactMax := limit - len(memRows)
		for i, t := range tasks {
			if len(rows) >= exactMax || i >= limit {
				break
			}
			id, _ := t["id"].(float64)
			if seen[id] {
				continue
			}
			seen[id] = true
			row := trimTask(t)
			row["source"] = "exact"
			rows = append(rows, row)
		}
		// Merge ordering (card #841): strict-slots (default) keeps the
		// exact half on top; memory-first promotes the semantic rows to
		// the head. Slot quota is identical in both policies; a
		// not-configured memory leaves the zero Config → strict.
		policy := cfg.HybridPolicyOrDefault()
		if policy == memory.HybridPolicyMemoryFirst {
			ordered := make([]map[string]any, 0, len(memRows)+len(rows))
			ordered = append(ordered, memRows...)
			ordered = append(ordered, rows...)
			rows = ordered
		} else {
			rows = append(rows, memRows...)
		}
		return nil, map[string]any{
			"query": in.Query,
			"tasks": rows,
			"count": len(rows),
			// sources (card #841): how many rows each half contributed
			// after dedupe/caps — merge quality becomes observable.
			"sources": map[string]int{
				"exact":  len(rows) - len(memRows),
				"memory": len(memRows),
				"merged": len(rows),
			},
			"semantic": semantic,
		}, nil
	}
}

func registerSearch(r *Registry) {
	add[SearchAllIn](r, "search_all", "Search tasks and projects across instances", handleSearchAll(r))
	add[SearchAllTasksIn](r, "search_all_tasks", "List tasks across instances with filters", handleSearchAllTasks(r))
	add[SearchHybridIn](r, "search_hybrid", "Hybrid search: exact task filtering plus top-K semantic hits from the memory index (source=memory); memory issues degrade to a note, never fail", handleSearchHybrid(r))
}
