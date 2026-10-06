package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type TaskCompleteIn struct {
	TaskID int `json:"task_id"`
}

func findBucketByTitle(buckets []map[string]any, title string) (int, bool) {
	for _, b := range buckets {
		bt, _ := b["title"].(string)
		if bt == title {
			id, _ := b["id"].(float64)
			return int(id), true
		}
	}
	return 0, false
}

func findTaskBucket(buckets []map[string]any, taskID int) (int, bool) {
	for _, b := range buckets {
		bid, _ := b["id"].(float64)
		tasks, _ := b["tasks"].([]any)
		for _, t := range tasks {
			tm, _ := t.(map[string]any)
			if tm == nil {
				continue
			}
			if tid, _ := tm["id"].(float64); int(tid) == taskID {
				return int(bid), true
			}
		}
	}
	return 0, false
}

func asInt(v any) int {
	f, _ := v.(float64)
	return int(f)
}

func ensureDoneBucket(ctx context.Context, r *Registry, projectID int, view map[string]any) (int, error) {
	if bid := asInt(view["done_bucket_id"]); bid > 0 {
		return bid, nil
	}
	var buckets []map[string]any
	if err := r.api().ListBuckets(ctx, projectID, asInt(view["id"]), &buckets); err != nil {
		return 0, fmt.Errorf("list buckets: %w", err)
	}
	doneID, ok := findBucketByTitle(buckets, "done")
	if !ok {
		var created map[string]any
		if err := r.api().CreateBucket(ctx, projectID, asInt(view["id"]), map[string]any{"title": "done"}, &created); err != nil {
			return 0, fmt.Errorf("create done bucket: %w", err)
		}
		doneID = asInt(created["id"])
	}
	view["done_bucket_id"] = doneID
	if err := r.api().UpdateView(ctx, projectID, asInt(view["id"]), view, nil); err != nil {
		return 0, fmt.Errorf("configure done_bucket_id: %w", err)
	}
	return doneID, nil
}

func verifyBucket(ctx context.Context, r *Registry, projectID, viewID, taskID int) (int, bool) {
	var buckets []map[string]any
	if err := r.api().ViewTasks(ctx, projectID, viewID, &buckets); err != nil {
		return 0, false
	}
	return findTaskBucket(buckets, taskID)
}

func handleTaskComplete(r *Registry) mcp.ToolHandlerFor[TaskCompleteIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in TaskCompleteIn) (*mcp.CallToolResult, any, error) {
		var current map[string]any
		if err := r.api().GetTask(ctx, in.TaskID, &current); err != nil {
			return nil, nil, err
		}
		projectID := asInt(current["project_id"])

		var views []map[string]any
		out := map[string]any{
			"task_id":           in.TaskID,
			"done":              true,
			"bucket_id":         0,
			"view_id":           0,
			"fallback_used":     false,
			"bucket_configured": false,
		}
		if err := r.api().ListViews(ctx, projectID, &views); err == nil {
			if viewID, view, ok := pickKanbanView(views); ok {
				doneBucket, err := ensureDoneBucket(ctx, r, projectID, view)
				if err != nil {
					return nil, nil, err
				}
				out["view_id"] = viewID
				out["bucket_configured"] = asInt(view["done_bucket_id"]) == doneBucket && doneBucket > 0

				current["done"] = true
				current["bucket_id"] = doneBucket
				if err := r.api().UpdateTask(ctx, in.TaskID, current, nil); err != nil {
					return nil, nil, err
				}

				got, ok := verifyBucket(ctx, r, projectID, viewID, in.TaskID)
				if !ok || got != doneBucket {
					pos := map[string]any{
						"project_view_id": viewID,
						"task_id":         in.TaskID,
						"bucket_id":       doneBucket,
					}
					if err := r.api().SetTaskPosition(ctx, in.TaskID, pos, nil); err != nil {
						return nil, nil, fmt.Errorf("bucket fallback: %w", err)
					}
					out["fallback_used"] = true
					if got, ok = verifyBucket(ctx, r, projectID, viewID, in.TaskID); !ok || got != doneBucket {
						return nil, nil, fmt.Errorf("task %d did not land in done bucket %d after fallback (got %d)", in.TaskID, doneBucket, got)
					}
				}
				out["bucket_id"] = doneBucket
			} else {
				current["done"] = true
				if err := r.api().UpdateTask(ctx, in.TaskID, current, nil); err != nil {
					return nil, nil, err
				}
			}
		} else {
			current["done"] = true
			if err := r.api().UpdateTask(ctx, in.TaskID, current, nil); err != nil {
				return nil, nil, err
			}
		}
		return nil, out, nil
	}
}
