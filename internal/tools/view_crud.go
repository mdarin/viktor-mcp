package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func trimView(v map[string]any) map[string]any {
	keys := []string{"id", "title", "project_id", "view_kind", "position", "bucket_configuration_mode", "default_bucket_id", "done_bucket_id", "filter"}
	out := make(map[string]any, len(keys))
	for _, k := range keys {
		if val, ok := v[k]; ok {
			out[k] = val
		}
	}
	return out
}

func handleViewList(r *Registry) mcp.ToolHandlerFor[ViewListIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in ViewListIn) (*mcp.CallToolResult, any, error) {
		var views []map[string]any
		if err := r.api().ListViews(ctx, in.ProjectID, &views); err != nil {
			return nil, nil, err
		}
		trimmed := make([]map[string]any, len(views))
		for i, v := range views {
			trimmed[i] = trimView(v)
		}
		return nil, map[string]any{"count": len(trimmed), "views": trimmed}, nil
	}
}

func handleViewCreate(r *Registry) mcp.ToolHandlerFor[ViewCreateIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in ViewCreateIn) (*mcp.CallToolResult, any, error) {
		if in.Title == "" {
			return nil, nil, fmt.Errorf("title is required")
		}
		if in.ViewKind == "" {
			return nil, nil, fmt.Errorf("view_kind is required (list, gantt, table, kanban)")
		}
		body := map[string]any{
			"title":     in.Title,
			"view_kind": in.ViewKind,
		}
		if in.FilterQuery != "" {
			body["filter"] = map[string]any{"filter": in.FilterQuery}
		}
		var created map[string]any
		if err := r.api().CreateView(ctx, in.ProjectID, body, &created); err != nil {
			return nil, nil, err
		}
		return nil, trimView(created), nil
	}
}

func handleViewUpdate(r *Registry) mcp.ToolHandlerFor[ViewUpdateIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in ViewUpdateIn) (*mcp.CallToolResult, any, error) {
		if in.Title == "" && in.FilterQuery == "" {
			return nil, nil, fmt.Errorf("nothing to update: provide title or filter_query")
		}
		var views []map[string]any
		if err := r.api().ListViews(ctx, in.ProjectID, &views); err != nil {
			return nil, nil, fmt.Errorf("list views: %w", err)
		}
		var current map[string]any
		for _, v := range views {
			if asInt(v["id"]) == in.ViewID {
				current = v
				break
			}
		}
		if current == nil {
			return nil, nil, fmt.Errorf("view %d not found in project %d", in.ViewID, in.ProjectID)
		}
		if in.Title != "" {
			current["title"] = in.Title
		}
		if in.FilterQuery != "" {
			filter, _ := current["filter"].(map[string]any)
			if filter == nil {
				filter = map[string]any{}
			}
			filter["filter"] = in.FilterQuery
			current["filter"] = filter
		}
		var updated map[string]any
		if err := r.api().UpdateView(ctx, in.ProjectID, in.ViewID, current, &updated); err != nil {
			return nil, nil, err
		}
		return nil, trimView(updated), nil
	}
}

func handleViewDelete(r *Registry) mcp.ToolHandlerFor[ViewDeleteIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in ViewDeleteIn) (*mcp.CallToolResult, any, error) {
		if err := r.api().DeleteView(ctx, in.ProjectID, in.ViewID); err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"deleted": true, "view_id": in.ViewID}, nil
	}
}

func handleViewGetTasks(r *Registry) mcp.ToolHandlerFor[ViewGetTasksIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in ViewGetTasksIn) (*mcp.CallToolResult, any, error) {
		var views []map[string]any
		if err := r.api().ListViews(ctx, in.ProjectID, &views); err != nil {
			return nil, nil, fmt.Errorf("list views: %w", err)
		}
		kind := ""
		for _, v := range views {
			if asInt(v["id"]) == in.ViewID {
				kind, _ = v["view_kind"].(string)
				break
			}
		}
		if kind == "kanban" {
			return handleKanbanBoard(ctx, r, in.ProjectID, in.ViewID)
		}
		var tasks []map[string]any
		if err := r.api().ViewTasks(ctx, in.ProjectID, in.ViewID, &tasks); err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"count": len(tasks), "tasks": tasks}, nil
	}
}

func handleViewSetPosition(r *Registry) mcp.ToolHandlerFor[ViewSetPositionIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in ViewSetPositionIn) (*mcp.CallToolResult, any, error) {
		body := map[string]any{
			"task_id":         in.TaskID,
			"project_view_id": in.ViewID,
			"position":        in.Position,
		}
		var pos map[string]any
		if err := r.api().SetTaskPosition(ctx, in.TaskID, body, &pos); err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{
			"task_id":  in.TaskID,
			"view_id":  in.ViewID,
			"position": pos["position"],
		}, nil
	}
}
