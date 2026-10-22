package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"viktor/internal/client"
)

func resolveLabels(ctx context.Context, r *Registry, names []string, createMissing bool) (map[string]int, error) {
	byName := map[string]int{}
	var existing []map[string]any
	if err := r.api().ListLabels(ctx, client.ListParams{}, &existing); err != nil {
		return nil, err
	}
	for _, l := range existing {
		byName[strings.ToLower(l["title"].(string))] = asInt(l["id"])
	}
	for _, name := range names {
		key := strings.ToLower(name)
		if _, ok := byName[key]; ok {
			continue
		}
		if !createMissing {
			return nil, fmt.Errorf("label %q not found (set create_missing_labels to create)", name)
		}
		var created map[string]any
		if err := r.api().CreateLabel(ctx, map[string]any{"title": name}, &created); err != nil {
			return nil, fmt.Errorf("create label %q: %w", name, err)
		}
		byName[key] = asInt(created["id"])
		time.Sleep(100 * time.Millisecond)
	}
	return byName, nil
}

func findKanbanViewID(ctx context.Context, r *Registry, projectID int) (int, error) {
	var views []map[string]any
	if err := r.api().ListViews(ctx, projectID, &views); err != nil {
		return 0, err
	}
	for _, v := range views {
		if strings.EqualFold(v["view_kind"].(string), "kanban") {
			return asInt(v["id"]), nil
		}
	}
	return 0, fmt.Errorf("no kanban view in project %d", projectID)
}

func resolveBucketByName(ctx context.Context, r *Registry, projectID int, title string) (viewID, bucketID int, err error) {
	viewID, err = findKanbanViewID(ctx, r, projectID)
	if err != nil {
		return 0, 0, err
	}
	var buckets []map[string]any
	if err := r.api().ListBuckets(ctx, projectID, viewID, &buckets); err != nil {
		return 0, 0, err
	}
	for _, b := range buckets {
		if strings.EqualFold(b["title"].(string), title) {
			return viewID, asInt(b["id"]), nil
		}
	}
	return 0, 0, fmt.Errorf("bucket %q not found in project %d", title, projectID)
}

func moveTaskToBucket(ctx context.Context, r *Registry, projectID, viewID, bucketID, taskID int) error {
	var out any
	return r.api().MoveTaskToBucket(ctx, projectID, viewID, bucketID, taskID, &out)
}

// maxBucketPositions returns the current max task position per bucket id,
// read from the board read model (card #845). A move without a position
// lands the task at position 0 — the top of the column in the UI.
func maxBucketPositions(ctx context.Context, r *Registry, projectID, viewID int) (map[int]float64, error) {
	var board []map[string]any
	if err := r.api().ViewTasks(ctx, projectID, viewID, &board); err != nil {
		return nil, err
	}
	maxPos := map[int]float64{}
	for _, b := range board {
		bid := asInt(b["id"])
		tasks, _ := b["tasks"].([]any)
		for _, t := range tasks {
			tm, ok := t.(map[string]any)
			if !ok {
				continue
			}
			if p, ok := numAny(tm["position"]); ok && p > maxPos[bid] {
				maxPos[bid] = p
			}
		}
	}
	return maxPos, nil
}

// placeAtBucketEnd assigns the task the next free position in its bucket so
// it appears at the bottom of the column, matching a UI drag (card #845).
// best-effort: failures do not fail the move itself.
func placeAtBucketEnd(ctx context.Context, r *Registry, viewID, bucketID, taskID int, maxPos map[int]float64) error {
	if maxPos == nil {
		return nil
	}
	next := maxPos[bucketID] + 1
	maxPos[bucketID] = next
	var out any
	if err := r.api().SetTaskPosition(ctx, taskID, map[string]any{"project_view_id": viewID, "position": next}, &out); err != nil {
		return err
	}
	return nil
}

// moveAndPlace moves a task into a bucket and puts it at the end of that
// column (UI-visible semantics); maxPos may be nil to skip placement.
func moveAndPlace(ctx context.Context, r *Registry, projectID, viewID, bucketID, taskID int, maxPos map[int]float64) error {
	if err := moveTaskToBucket(ctx, r, projectID, viewID, bucketID, taskID); err != nil {
		return err
	}
	if err := placeAtBucketEnd(ctx, r, viewID, bucketID, taskID, maxPos); err != nil {
		return fmt.Errorf("position: %w", err)
	}
	return nil
}

func projectTasksWithLabel(ctx context.Context, r *Registry, projectID int, labelFilter string) ([]map[string]any, error) {
	var tasks []map[string]any
	if err := r.api().ProjectTasks(ctx, projectID, client.ListParams{Filter: "done = false"}, &tasks); err != nil {
		return nil, err
	}
	if labelFilter == "" {
		return tasks, nil
	}
	var out []map[string]any
	for _, t := range tasks {
		labels, _ := t["labels"].([]any)
		for _, l := range labels {
			if m, ok := l.(map[string]any); ok && strings.EqualFold(m["title"].(string), labelFilter) {
				out = append(out, t)
				break
			}
		}
	}
	return out, nil
}

func handleBatchCreateTasks(r *Registry) mcp.ToolHandlerFor[BatchCreateTasksIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in BatchCreateTasksIn) (*mcp.CallToolResult, any, error) {
		if in.ProjectID == 0 {
			return nil, nil, fmt.Errorf("project_id is required")
		}
		if len(in.Tasks) == 0 {
			return nil, nil, fmt.Errorf("tasks must not be empty")
		}
		for i, spec := range in.Tasks {
			if spec.Title == "" {
				return nil, nil, fmt.Errorf("tasks[%d].title is required", i)
			}
		}
		labelNames := map[string]bool{}
		for _, spec := range in.Tasks {
			for _, l := range spec.Labels {
				labelNames[l] = true
			}
		}
		var labelIDs map[string]int
		if len(labelNames) > 0 {
			var err error
			labelIDs, err = resolveLabels(ctx, r, sortedKeys(labelNames), in.CreateMissingLabels)
			if err != nil {
				return nil, nil, err
			}
		}
		created := []map[string]any{}
		failures := []map[string]any{}
		refs := map[string]int{}
		relations := []struct{ from, kind, to string }{}
		var maxPos map[int]float64
		anyBucket := false
		for _, spec := range in.Tasks {
			if spec.Bucket != "" {
				anyBucket = true
				break
			}
		}
		if anyBucket {
			viewID, err := findKanbanViewID(ctx, r, in.ProjectID)
			if err == nil {
				maxPos, _ = maxBucketPositions(ctx, r, in.ProjectID, viewID)
			}
		}
		for i, spec := range in.Tasks {
			body := map[string]any{"title": spec.Title}
			if spec.Description != "" {
				body["description"] = spec.Description
			}
			if spec.Priority != 0 {
				body["priority"] = spec.Priority
			}
			if spec.DueDate != "" {
				ts, err := time.Parse(time.RFC3339, spec.DueDate)
				if err != nil {
					failures = append(failures, map[string]any{"index": float64(i), "ref": spec.Ref, "error": fmt.Sprintf("due_date: %v", err)})
					continue
				}
				body["due_date"] = ts.UTC().Format(time.RFC3339)
			}
			var task map[string]any
			if err := r.api().CreateTask(ctx, in.ProjectID, body, &task); err != nil {
				failures = append(failures, map[string]any{"index": float64(i), "ref": spec.Ref, "error": err.Error()})
				continue
			}
			id := asInt(task["id"])
			if spec.Ref != "" {
				refs[spec.Ref] = id
			}
			for _, l := range spec.Labels {
				if err := r.api().AddTaskLabel(ctx, id, labelIDs[strings.ToLower(l)], nil); err != nil {
					failures = append(failures, map[string]any{"index": float64(i), "ref": spec.Ref, "error": fmt.Sprintf("label %q: %v", l, err)})
				}
			}
			if spec.Bucket != "" {
				viewID, bucketID, err := resolveBucketByName(ctx, r, in.ProjectID, spec.Bucket)
				if err != nil {
					failures = append(failures, map[string]any{"index": float64(i), "ref": spec.Ref, "error": fmt.Sprintf("bucket: %v", err)})
				} else if err := moveAndPlace(ctx, r, in.ProjectID, viewID, bucketID, id, maxPos); err != nil {
					failures = append(failures, map[string]any{"index": float64(i), "ref": spec.Ref, "error": fmt.Sprintf("bucket move: %v", err)})
				}
			}
			for _, ref := range spec.Blocks {
				relations = append(relations, struct{ from, kind, to string }{spec.Ref, "blocking", ref})
			}
			for _, ref := range spec.BlockedBy {
				relations = append(relations, struct{ from, kind, to string }{spec.Ref, "blocked", ref})
			}
			created = append(created, trimTask(task))
			time.Sleep(300 * time.Millisecond)
		}
		relationErrors := []map[string]any{}
		for _, rel := range relations {
			fromID, ok1 := refs[rel.from]
			toID, ok2 := refs[rel.to]
			if !ok1 || !ok2 {
				relationErrors = append(relationErrors, map[string]any{"from": rel.from, "to": rel.to, "error": "unknown ref (refs must be defined in this batch)"})
				continue
			}
			var out any
			if err := r.api().CreateTaskRelation(ctx, fromID, rel.kind, toID, &out); err != nil {
				relationErrors = append(relationErrors, map[string]any{"from": rel.from, "to": rel.to, "error": err.Error()})
			}
		}
		return nil, map[string]any{
			"created":        created,
			"count":          len(created),
			"errors":         failures,
			"relation_error": relationErrors,
		}, nil
	}
}

func handleBatchUpdateTasks(r *Registry) mcp.ToolHandlerFor[BatchUpdateTasksIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in BatchUpdateTasksIn) (*mcp.CallToolResult, any, error) {
		if len(in.Updates) == 0 {
			return nil, nil, fmt.Errorf("updates must not be empty")
		}
		updated := []map[string]any{}
		failures := []map[string]any{}
		for i, u := range in.Updates {
			if u.TaskID == 0 {
				failures = append(failures, map[string]any{"index": float64(i), "task_id": float64(u.TaskID), "error": "task_id is required"})
				continue
			}
			var current map[string]any
			if err := r.api().GetTask(ctx, u.TaskID, &current); err != nil {
				failures = append(failures, map[string]any{"index": float64(i), "task_id": float64(u.TaskID), "error": err.Error()})
				continue
			}
			body := map[string]any{}
			for k, v := range current {
				body[k] = v
			}
			changed := false
			if u.Title != "" {
				body["title"] = u.Title
				changed = true
			}
			if u.Description != "" {
				body["description"] = u.Description
				changed = true
			}
			if u.DueDate != "" {
				ts, err := time.Parse(time.RFC3339, u.DueDate)
				if err != nil {
					failures = append(failures, map[string]any{"index": float64(i), "task_id": float64(u.TaskID), "error": fmt.Sprintf("due_date: %v", err)})
					continue
				}
				body["due_date"] = ts.UTC().Format(time.RFC3339)
				changed = true
			}
			if u.Priority != 0 {
				body["priority"] = u.Priority
				changed = true
			}
			if !changed {
				failures = append(failures, map[string]any{"index": float64(i), "task_id": float64(u.TaskID), "error": "no fields to update (empty strings are treated as unchanged)"})
				continue
			}
			var saved map[string]any
			if err := r.api().UpdateTask(ctx, u.TaskID, body, &saved); err != nil {
				failures = append(failures, map[string]any{"index": float64(i), "task_id": float64(u.TaskID), "error": err.Error()})
				continue
			}
			updated = append(updated, trimTask(saved))
			time.Sleep(300 * time.Millisecond)
		}
		return nil, map[string]any{"updated": updated, "count": len(updated), "errors": failures}, nil
	}
}

func handleBatchRelabel(r *Registry) mcp.ToolHandlerFor[BatchRelabelIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in BatchRelabelIn) (*mcp.CallToolResult, any, error) {
		if in.ProjectID == 0 {
			return nil, nil, fmt.Errorf("project_id is required")
		}
		if len(in.TaskIDs) == 0 {
			return nil, nil, fmt.Errorf("task_ids must not be empty")
		}
		if len(in.AddLabels) == 0 && len(in.RemoveLabels) == 0 && len(in.SetLabels) == 0 {
			return nil, nil, fmt.Errorf("one of add_labels/remove_labels/set_labels is required")
		}
		names := append(append([]string{}, in.AddLabels...), in.RemoveLabels...)
		if len(in.SetLabels) > 0 {
			names = append(names, in.SetLabels...)
		}
		labelIDs, err := resolveLabels(ctx, r, names, false)
		if err != nil {
			return nil, nil, err
		}
		changed := []int{}
		failures := []map[string]any{}
		for _, taskID := range in.TaskIDs {
			var task map[string]any
			if err := r.api().GetTask(ctx, taskID, &task); err != nil {
				failures = append(failures, map[string]any{"task_id": float64(taskID), "error": err.Error()})
				continue
			}
			if len(in.SetLabels) > 0 {
				target := map[string]bool{}
				for _, l := range in.SetLabels {
					target[strings.ToLower(l)] = true
				}
				labels, _ := task["labels"].([]any)
				for _, l := range labels {
					m, ok := l.(map[string]any)
					if !ok {
						continue
					}
					lid := asInt(m["id"])
					if !target[strings.ToLower(m["title"].(string))] {
						if err := r.api().RemoveTaskLabel(ctx, taskID, lid); err != nil {
							failures = append(failures, map[string]any{"task_id": float64(taskID), "error": fmt.Sprintf("remove %q: %v", m["title"], err)})
						}
					}
				}
				for _, l := range in.SetLabels {
					if err := attachLabelIfMissing(ctx, r, taskID, labelIDs[strings.ToLower(l)], l); err != nil {
						failures = append(failures, map[string]any{"task_id": float64(taskID), "error": err.Error()})
					}
				}
			}
			for _, l := range in.AddLabels {
				if err := attachLabelIfMissing(ctx, r, taskID, labelIDs[strings.ToLower(l)], l); err != nil {
					failures = append(failures, map[string]any{"task_id": float64(taskID), "error": err.Error()})
				}
			}
			for _, l := range in.RemoveLabels {
				lid := labelIDs[strings.ToLower(l)]
				labels, _ := task["labels"].([]any)
				for _, lb := range labels {
					if m, ok := lb.(map[string]any); ok && asInt(m["id"]) == lid {
						if err := r.api().RemoveTaskLabel(ctx, taskID, lid); err != nil {
							failures = append(failures, map[string]any{"task_id": float64(taskID), "error": fmt.Sprintf("remove %q: %v", l, err)})
						}
						break
					}
				}
			}
			changed = append(changed, taskID)
			time.Sleep(300 * time.Millisecond)
		}
		return nil, map[string]any{"changed": toFloats(changed), "count": len(changed), "errors": failures}, nil
	}
}

func attachLabelIfMissing(ctx context.Context, r *Registry, taskID, labelID int, title string) error {
	var task map[string]any
	if err := r.api().GetTask(ctx, taskID, &task); err != nil {
		return err
	}
	if taskHasLabel(task, labelID) {
		return nil
	}
	if err := r.api().AddTaskLabel(ctx, taskID, labelID, nil); err != nil {
		return fmt.Errorf("add %q: %w", title, err)
	}
	return nil
}

func handleBatchMoveByLabel(r *Registry) mcp.ToolHandlerFor[BatchMoveByLabelIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in BatchMoveByLabelIn) (*mcp.CallToolResult, any, error) {
		if in.ProjectID == 0 || (in.ViewID == 0 && in.Bucket == "") || (in.BucketID == 0 && in.Bucket == "") {
			return nil, nil, fmt.Errorf("project_id and a target bucket (bucket_id or bucket name) are required")
		}
		if in.LabelFilter == "" {
			return nil, nil, fmt.Errorf("label_filter is required (safety guard against moving everything)")
		}
		tasks, err := projectTasksWithLabel(ctx, r, in.ProjectID, in.LabelFilter)
		if err != nil {
			return nil, nil, err
		}
		bucketID := in.BucketID
		if bucketID == 0 && in.Bucket != "" {
			viewID, bid, err := resolveBucketByName(ctx, r, in.ProjectID, in.Bucket)
			if err != nil {
				return nil, nil, err
			}
			bucketID = bid
			if in.ViewID == 0 {
				in.ViewID = viewID
			}
		}
		if in.ProjectID == 0 || in.ViewID == 0 || bucketID == 0 {
			return nil, nil, fmt.Errorf("bucket required: set bucket_id or bucket (name)")
		}
		maxPos, _ := maxBucketPositions(ctx, r, in.ProjectID, in.ViewID)
		moved := []int{}
		failures := []map[string]any{}
		for _, t := range tasks {
			id := asInt(t["id"])
			if err := moveAndPlace(ctx, r, in.ProjectID, in.ViewID, bucketID, id, maxPos); err != nil {
				failures = append(failures, map[string]any{"task_id": float64(id), "error": err.Error()})
				continue
			}
			moved = append(moved, id)
			time.Sleep(300 * time.Millisecond)
		}
		return nil, map[string]any{"moved": toFloats(moved), "count": len(moved), "errors": failures}, nil
	}
}

func handleBatchCompleteByLabel(r *Registry) mcp.ToolHandlerFor[BatchCompleteByLabelIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in BatchCompleteByLabelIn) (*mcp.CallToolResult, any, error) {
		if in.ProjectID == 0 {
			return nil, nil, fmt.Errorf("project_id is required")
		}
		if in.LabelFilter == "" {
			return nil, nil, fmt.Errorf("label_filter is required (safety guard against completing everything)")
		}
		tasks, err := projectTasksWithLabel(ctx, r, in.ProjectID, in.LabelFilter)
		if err != nil {
			return nil, nil, err
		}
		completed := []int{}
		failures := []map[string]any{}
		for _, t := range tasks {
			id := asInt(t["id"])
			body := map[string]any{}
			for k, v := range t {
				body[k] = v
			}
			body["done"] = true
			var saved map[string]any
			if err := r.api().UpdateTask(ctx, id, body, &saved); err != nil {
				failures = append(failures, map[string]any{"task_id": float64(id), "error": err.Error()})
				continue
			}
			completed = append(completed, id)
			time.Sleep(300 * time.Millisecond)
		}
		return nil, map[string]any{"completed": toFloats(completed), "count": len(completed), "errors": failures}, nil
	}
}

func handleBatchCreateLabels(r *Registry) mcp.ToolHandlerFor[BatchCreateLabelsIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in BatchCreateLabelsIn) (*mcp.CallToolResult, any, error) {
		if len(in.Labels) == 0 {
			return nil, nil, fmt.Errorf("labels must not be empty")
		}
		var existing []map[string]any
		if err := r.api().ListLabels(ctx, client.ListParams{}, &existing); err != nil {
			return nil, nil, err
		}
		byName := map[string]bool{}
		for _, l := range existing {
			byName[strings.ToLower(l["title"].(string))] = true
		}
		created := []map[string]any{}
		skipped := []string{}
		failures := []map[string]any{}
		for i, spec := range in.Labels {
			if spec.Title == "" {
				failures = append(failures, map[string]any{"index": float64(i), "error": "title is required"})
				continue
			}
			if byName[strings.ToLower(spec.Title)] {
				skipped = append(skipped, spec.Title)
				continue
			}
			body := map[string]any{"title": spec.Title}
			if spec.HexColor != "" {
				body["hex_color"] = spec.HexColor
			}
			var out map[string]any
			if err := r.api().CreateLabel(ctx, body, &out); err != nil {
				failures = append(failures, map[string]any{"index": float64(i), "error": err.Error()})
				continue
			}
			byName[strings.ToLower(spec.Title)] = true
			created = append(created, out)
			time.Sleep(300 * time.Millisecond)
		}
		return nil, map[string]any{"created": created, "skipped": skipped, "count": len(created), "errors": failures}, nil
	}
}

func handleBatchAssignBuckets(r *Registry) mcp.ToolHandlerFor[BatchAssignBucketsIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in BatchAssignBucketsIn) (*mcp.CallToolResult, any, error) {
		if in.ProjectID == 0 || (in.ViewID == 0 && in.Bucket == "") {
			return nil, nil, fmt.Errorf("project_id and view_id (or bucket name) are required")
		}
		if len(in.Assignments) == 0 {
			return nil, nil, fmt.Errorf("assignments must not be empty")
		}
		assigned := []map[string]any{}
		failures := []map[string]any{}
		defaultBucketID := 0
		if in.Bucket != "" {
			viewID, bid, err := resolveBucketByName(ctx, r, in.ProjectID, in.Bucket)
			if err != nil {
				return nil, nil, err
			}
			defaultBucketID = bid
			if in.ViewID == 0 {
				in.ViewID = viewID
			}
		}
		maxPos, _ := maxBucketPositions(ctx, r, in.ProjectID, in.ViewID)
		for i, a := range in.Assignments {
			bucketID := a.BucketID
			if bucketID == 0 {
				bucketID = defaultBucketID
			}
			if a.TaskID == 0 || bucketID == 0 {
				failures = append(failures, map[string]any{"index": float64(i), "error": "task_id and bucket_id (or the bucket name) are required"})
				continue
			}
			if a.Position != 0 {
				if err := moveTaskToBucket(ctx, r, in.ProjectID, in.ViewID, bucketID, a.TaskID); err != nil {
					failures = append(failures, map[string]any{"index": float64(i), "task_id": float64(a.TaskID), "error": err.Error()})
					continue
				}
				var out any
				if err := r.api().SetTaskPosition(ctx, a.TaskID, map[string]any{"project_view_id": in.ViewID, "position": float64(a.Position)}, &out); err != nil {
					failures = append(failures, map[string]any{"index": float64(i), "task_id": float64(a.TaskID), "error": fmt.Sprintf("position: %v", err)})
				}
			} else if err := moveAndPlace(ctx, r, in.ProjectID, in.ViewID, bucketID, a.TaskID, maxPos); err != nil {
				failures = append(failures, map[string]any{"index": float64(i), "task_id": float64(a.TaskID), "error": err.Error()})
				continue
			}
			row := map[string]any{"task_id": float64(a.TaskID), "bucket_id": float64(bucketID)}
			if a.Position != 0 {
				row["position"] = float64(a.Position)
			}
			assigned = append(assigned, row)
			time.Sleep(300 * time.Millisecond)
		}
		return nil, map[string]any{"assigned": assigned, "count": len(assigned), "errors": failures}, nil
	}
}

func handleBatchLabelToBuckets(r *Registry) mcp.ToolHandlerFor[BatchLabelToBucketsIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in BatchLabelToBucketsIn) (*mcp.CallToolResult, any, error) {
		if in.ProjectID == 0 || in.ViewID == 0 {
			return nil, nil, fmt.Errorf("project_id and view_id are required")
		}
		if len(in.LabelToBucketMap) == 0 {
			return nil, nil, fmt.Errorf("label_to_bucket_map must not be empty")
		}
		moved := []map[string]any{}
		failures := []map[string]any{}
		maxPos, _ := maxBucketPositions(ctx, r, in.ProjectID, in.ViewID)
		for label, bucketID := range in.LabelToBucketMap {
			if bucketID == 0 {
				failures = append(failures, map[string]any{"label": label, "error": "bucket_id must not be 0"})
				continue
			}
			tasks, err := projectTasksWithLabel(ctx, r, in.ProjectID, label)
			if err != nil {
				failures = append(failures, map[string]any{"label": label, "error": err.Error()})
				continue
			}
			for _, t := range tasks {
				id := asInt(t["id"])
				if err := moveAndPlace(ctx, r, in.ProjectID, in.ViewID, bucketID, id, maxPos); err != nil {
					failures = append(failures, map[string]any{"label": label, "task_id": float64(id), "error": err.Error()})
					continue
				}
				moved = append(moved, map[string]any{"task_id": float64(id), "label": label, "bucket_id": float64(bucketID)})
				time.Sleep(300 * time.Millisecond)
			}
		}
		return nil, map[string]any{"moved": moved, "count": len(moved), "errors": failures}, nil
	}
}

func handleBatchReorderTasks(r *Registry) mcp.ToolHandlerFor[BatchReorderTasksIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in BatchReorderTasksIn) (*mcp.CallToolResult, any, error) {
		if in.ProjectID == 0 || in.ViewID == 0 {
			return nil, nil, fmt.Errorf("project_id and view_id are required")
		}
		if len(in.Positions) == 0 {
			return nil, nil, fmt.Errorf("positions must not be empty")
		}
		rows := []map[string]any{}
		failures := []map[string]any{}
		for i, p := range in.Positions {
			if p.TaskID == 0 {
				failures = append(failures, map[string]any{"index": float64(i), "error": "task_id is required"})
				continue
			}
			var out any
			if err := r.api().SetTaskPosition(ctx, p.TaskID, map[string]any{"project_view_id": in.ViewID, "position": float64(p.Position)}, &out); err != nil {
				failures = append(failures, map[string]any{"index": float64(i), "task_id": float64(p.TaskID), "error": err.Error()})
				continue
			}
			rows = append(rows, map[string]any{"task_id": float64(p.TaskID), "position": float64(p.Position)})
			time.Sleep(300 * time.Millisecond)
		}
		return nil, map[string]any{"ordered": rows, "count": len(rows), "errors": failures}, nil
	}
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func toFloats(ids []int) []any {
	out := make([]any, len(ids))
	for i, id := range ids {
		out[i] = float64(id)
	}
	return out
}
