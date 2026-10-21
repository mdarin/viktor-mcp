package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func pickKanbanView(views []map[string]any) (int, map[string]any, bool) {
	return findKanbanView(views, "")
}

func handleKanbanBoard(ctx context.Context, r *Registry, projectID, viewID int) (*mcp.CallToolResult, any, error) {
	if viewID == 0 {
		var views []map[string]any
		if err := r.api().ListViews(ctx, projectID, &views); err != nil {
			return nil, nil, fmt.Errorf("list views: %w", err)
		}
		var view map[string]any
		viewID, view, _ = pickKanbanView(views)
		if view == nil {
			return nil, nil, fmt.Errorf("no kanban view in project %d", projectID)
		}
	}
	var buckets []map[string]any
	if err := r.api().ViewTasks(ctx, projectID, viewID, &buckets); err != nil {
		return nil, nil, err
	}
	out := make([]map[string]any, len(buckets))
	for i, b := range buckets {
		tasks, _ := b["tasks"].([]any)
		if tasks == nil {
			tasks = []any{}
		}
		for _, t := range tasks {
			if tm, ok := t.(map[string]any); ok {
				delete(tm, "kanban_groups")
			}
		}
		out[i] = map[string]any{
			"id":       b["id"],
			"title":    b["title"],
			"position": b["position"],
			"limit":    b["limit"],
			"count":    b["count"],
			"tasks":    tasks,
		}
	}
	return nil, map[string]any{
		"project_id": projectID,
		"view_id":    viewID,
		"buckets":    out,
	}, nil
}

func handleKanbanGet(r *Registry) mcp.ToolHandlerFor[KanbanGetIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in KanbanGetIn) (*mcp.CallToolResult, any, error) {
		return handleKanbanBoard(ctx, r, in.ProjectID, 0)
	}
}

func handleKanbanListBuckets(r *Registry) mcp.ToolHandlerFor[KanbanListBucketsIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in KanbanListBucketsIn) (*mcp.CallToolResult, any, error) {
		var buckets []map[string]any
		if err := r.api().ListBuckets(ctx, in.ProjectID, in.ViewID, &buckets); err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"count": len(buckets), "buckets": buckets}, nil
	}
}

func handleKanbanCreateBucket(r *Registry) mcp.ToolHandlerFor[KanbanCreateBucketIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in KanbanCreateBucketIn) (*mcp.CallToolResult, any, error) {
		if in.Title == "" {
			return nil, nil, fmt.Errorf("title is required")
		}
		body := map[string]any{"title": in.Title}
		if in.Position != 0 {
			body["position"] = in.Position
		}
		if in.Limit != 0 {
			body["limit"] = in.Limit
		}
		var created map[string]any
		if err := r.api().CreateBucket(ctx, in.ProjectID, in.ViewID, body, &created); err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{
			"id":       created["id"],
			"title":    created["title"],
			"position": created["position"],
			"limit":    created["limit"],
		}, nil
	}
}

func handleKanbanDeleteBucket(r *Registry) mcp.ToolHandlerFor[KanbanDeleteBucketIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in KanbanDeleteBucketIn) (*mcp.CallToolResult, any, error) {
		if err := r.api().DeleteBucket(ctx, in.ProjectID, in.ViewID, in.BucketID); err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"deleted": true, "bucket_id": in.BucketID}, nil
	}
}

func handleKanbanSortBucket(r *Registry) mcp.ToolHandlerFor[KanbanSortBucketIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in KanbanSortBucketIn) (*mcp.CallToolResult, any, error) {
		if in.SortBy == "" {
			return nil, nil, fmt.Errorf("sort_by is required (title, due_date, priority, created, updated, ...)")
		}
		var board []map[string]any
		if err := r.api().ViewTasks(ctx, in.ProjectID, in.ViewID, &board); err != nil {
			return nil, nil, fmt.Errorf("read board: %w", err)
		}
		var bucket map[string]any
		var tasks []any
		for _, b := range board {
			if asInt(b["id"]) == in.BucketID {
				bucket = b
				tasks, _ = b["tasks"].([]any)
				break
			}
		}
		if bucket == nil {
			return nil, nil, fmt.Errorf("bucket %d not found in view %d", in.BucketID, in.ViewID)
		}
		keys := []string{in.SortBy}
		if in.ThenBy != "" {
			keys = append(keys, in.ThenBy)
		}
		sorted := sortTaskRefs(tasks, keys)
		moved := 0
		for i, t := range sorted {
			tm := t.(map[string]any)
			body := map[string]any{
				"project_view_id": in.ViewID,
				"position":        float64(i+1) * 1000,
			}
			if err := r.api().SetTaskPosition(ctx, asInt(tm["id"]), body, nil); err != nil {
				return nil, nil, fmt.Errorf("reposition task %d: %w", asInt(tm["id"]), err)
			}
			moved++
		}
		return nil, map[string]any{
			"bucket_id":   in.BucketID,
			"sort_by":     keys,
			"tasks_moved": moved,
		}, nil
	}
}

func sortTaskRefs(tasks []any, keys []string) []any {
	out := append([]any{}, tasks...)
	sort.SliceStable(out, func(i, j int) bool {
		a, _ := out[i].(map[string]any)
		b, _ := out[j].(map[string]any)
		for _, k := range keys {
			av, bv := a[k], b[k]
			af, aok := av.(float64)
			bf, bok := bv.(float64)
			if aok && bok {
				if af != bf {
					return af < bf
				}
				continue
			}
			as, _ := av.(string)
			bs, _ := bv.(string)
			if as != bs {
				return strings.ToLower(as) < strings.ToLower(bs)
			}
		}
		return false
	})
	return out
}

func handleKanbanTasksByBucket(r *Registry) mcp.ToolHandlerFor[KanbanTasksByBucketIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in KanbanTasksByBucketIn) (*mcp.CallToolResult, any, error) {
		res, data, err := handleKanbanBoard(ctx, r, in.ProjectID, in.ViewID)
		if err != nil {
			return res, data, err
		}
		board := data.(map[string]any)
		grouped := map[string]any{}
		for _, b := range board["buckets"].([]map[string]any) {
			title := fmt.Sprint(b["title"])
			grouped[title] = b["tasks"]
		}
		return nil, map[string]any{
			"project_id": in.ProjectID,
			"view_id":    board["view_id"],
			"by_bucket":  grouped,
		}, nil
	}
}
