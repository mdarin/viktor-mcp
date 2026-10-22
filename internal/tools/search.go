package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/sync/errgroup"

	"viktor/internal/client"
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

func registerSearch(r *Registry) {
	add[SearchAllIn](r, "search_all", "Search tasks and projects across instances", handleSearchAll(r))
	add[SearchAllTasksIn](r, "search_all_tasks", "List tasks across instances with filters", handleSearchAllTasks(r))
}
