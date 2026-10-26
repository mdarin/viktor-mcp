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

// asFloat converts a JSON number (decoded as float64) to float64.
func asFloat(v any) float64 {
	f, _ := v.(float64)
	return f
}

const zeroDate = "0001-01-01T00:00:00Z"

func trimTask(m map[string]any) map[string]any {
	keys := []string{"id", "title", "description", "done", "due_date", "start_date", "priority", "project_id", "bucket_id", "bucket_title", "labels", "assignees", "reminders", "hex_color"}
	out := make(map[string]any, len(keys))
	for _, k := range keys {
		if v, ok := m[k]; ok {
			out[k] = v
		}
	}
	return out
}

// enrichTaskBucket fills in the real kanban position when the task
// endpoint reports bucket_id 0 (Vikunja does not mirror the position in
// /tasks/{id}), so position checks don't require a kanban_get (card #895).
// Best-effort: any lookup failure leaves the response untouched.
func enrichTaskBucket(ctx context.Context, r *Registry, task map[string]any) {
	if asInt(task["bucket_id"]) != 0 {
		return
	}
	projectID := asInt(task["project_id"])
	if projectID == 0 {
		return
	}
	viewID, err := findKanbanViewID(ctx, r, projectID)
	if err != nil {
		return
	}
	var board []map[string]any
	if err := r.api().ViewTasks(ctx, projectID, viewID, &board); err != nil {
		return
	}
	for _, b := range board {
		ts, _ := b["tasks"].([]any)
		for _, t := range ts {
			if tm, ok := t.(map[string]any); ok && asInt(tm["id"]) == asInt(task["id"]) {
				task["bucket_id"] = asInt(b["id"])
				if title, _ := b["title"].(string); title != "" {
					task["bucket_title"] = title
				}
				return
			}
		}
	}
}

func handleTaskGet(r *Registry) mcp.ToolHandlerFor[TaskGetIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in TaskGetIn) (*mcp.CallToolResult, any, error) {
		var task map[string]any
		if err := r.api().GetTask(ctx, in.TaskID, &task); err != nil {
			return nil, nil, err
		}
		var related map[string]any
		if err := r.api().Get(ctx, fmt.Sprintf("/tasks/%d?related=true", in.TaskID), nil, &related); err == nil {
			if rt, ok := related["related_tasks"].(map[string]any); ok && len(rt) > 0 {
				task["related_tasks"] = rt
			}
		}
		enrichTaskBucket(ctx, r, task)
		return nil, task, nil
	}
}

// placeNewTask puts a freshly created task into the first non-done bucket
// of the project's kanban view. Server-side Vikunja 2.7.1 surfaces a
// bucketless task in the view's done bucket, which hides fresh work as
// finished (card #893). Projects without a kanban view keep the old
// behavior.
func placeNewTask(ctx context.Context, r *Registry, projectID, taskID int) (int, string, error) {
	viewID, err := findKanbanViewID(ctx, r, projectID)
	if err != nil {
		return 0, "", err
	}
	var buckets []map[string]any
	if err := r.api().ListBuckets(ctx, projectID, viewID, &buckets); err != nil {
		return 0, "", err
	}
	// ListBuckets order is not guaranteed to be positional; pick the
	// non-done bucket with the smallest position.
	sort.SliceStable(buckets, func(i, j int) bool {
		return asFloat(buckets[i]["position"]) < asFloat(buckets[j]["position"])
	})
	target := map[string]any(nil)
	for _, b := range buckets {
		if strings.EqualFold(b["title"].(string), "done") {
			continue
		}
		target = b
		break
	}
	if target == nil && len(buckets) > 0 {
		target = buckets[0]
	}
	if target == nil {
		return 0, "", fmt.Errorf("project %d has no buckets", projectID)
	}
	if err := moveTaskToBucket(ctx, r, projectID, viewID, asInt(target["id"]), taskID); err != nil {
		return 0, "", err
	}
	title, _ := target["title"].(string)
	return asInt(target["id"]), title, nil
}

func handleTaskCreate(r *Registry) mcp.ToolHandlerFor[TaskCreateIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in TaskCreateIn) (*mcp.CallToolResult, any, error) {
		if in.Title == "" {
			return nil, nil, fmt.Errorf("title is required")
		}
		body := map[string]any{"title": in.Title}
		if in.Description != "" {
			body["description"] = in.Description
		}
		if in.Priority != 0 {
			body["priority"] = in.Priority
		}
		for key, t := range map[string]time.Time{
			"due_date": in.DueDate, "start_date": in.StartDate, "end_date": in.EndDate,
		} {
			if !t.IsZero() {
				body[key] = t.UTC().Format(time.RFC3339)
			}
		}
		if in.RepeatAfter != 0 {
			body["repeat_after"] = in.RepeatAfter
		}
		if in.RepeatMode != 0 {
			body["repeat_mode"] = in.RepeatMode
		}
		if in.HexColor != "" {
			if !validHexColor(in.HexColor) {
				return nil, nil, fmt.Errorf("hex_color: need #RGB or #RRGGBB, got %q", in.HexColor)
			}
			body["hex_color"] = in.HexColor
		}
		var created map[string]any
		if err := r.api().CreateTask(ctx, in.ProjectID, body, &created); err != nil {
			return nil, nil, err
		}
		// Fresh work must not surface as finished: park the task in the
		// first non-done bucket instead of the server default (card #893).
		if bucketID, bucketTitle, err := placeNewTask(ctx, r, in.ProjectID, asInt(created["id"])); err == nil {
			created["bucket_id"] = bucketID
			created["bucket_title"] = bucketTitle
		}
		if len(in.Labels) > 0 {
			labelIDs, err := resolveLabels(ctx, r, in.Labels, false)
			if err != nil {
				return nil, nil, err
			}
			taskID := asInt(created["id"])
			for _, l := range in.Labels {
				if err := attachLabelIfMissing(ctx, r, taskID, labelIDs[strings.ToLower(l)], l); err != nil {
					return nil, nil, err
				}
			}
			var refreshed map[string]any
			if err := r.api().GetTask(ctx, taskID, &refreshed); err == nil {
				created = refreshed
			}
		}
		return nil, trimTask(created), nil
	}
}

func handleTaskList(r *Registry) mcp.ToolHandlerFor[TaskListIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in TaskListIn) (*mcp.CallToolResult, any, error) {
		if in.ProjectID == 0 {
			if r.ctxProj != 0 {
				in.ProjectID = r.ctxProj
			} else {
				return nil, nil, fmt.Errorf("project_id is required (or set ctx_set)")
			}
		}
		p := client.ListParams{}
		if !in.IncludeCompleted {
			p.Filter = "done = false"
		}
		var tasks []map[string]any
		if err := r.api().ProjectTasks(ctx, in.ProjectID, p, &tasks); err != nil {
			return nil, nil, err
		}
		if in.LabelFilter != "" {
			filtered := tasks[:0]
			for _, t := range tasks {
				for _, l := range toLabelList(t["labels"]) {
					if lt, _ := l["title"].(string); lt == in.LabelFilter {
						filtered = append(filtered, t)
						break
					}
				}
			}
			tasks = filtered
		}
		if tasks == nil {
			tasks = []map[string]any{}
		}
		trimmed := make([]map[string]any, len(tasks))
		for i, t := range tasks {
			trimmed[i] = trimTask(t)
		}
		return nil, map[string]any{"project_id": in.ProjectID, "count": len(trimmed), "tasks": trimmed}, nil
	}
}

func anyLen(v any) int {
	s, _ := v.([]any)
	return len(s)
}

func toLabelList(v any) []map[string]any {
	raw, _ := v.([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, l := range raw {
		if m, ok := l.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func handleTaskDelete(r *Registry) mcp.ToolHandlerFor[TaskDeleteIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in TaskDeleteIn) (*mcp.CallToolResult, any, error) {
		if err := r.api().DeleteTask(ctx, in.TaskID); err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"deleted": true, "task_id": in.TaskID}, nil
	}
}

func handleTaskMove(r *Registry) mcp.ToolHandlerFor[TaskMoveIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in TaskMoveIn) (*mcp.CallToolResult, any, error) {
		var moved map[string]any
		if err := r.api().MoveTaskToProject(ctx, in.TaskID, in.TargetProjectID, &moved); err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"task_id": in.TaskID, "project_id": in.TargetProjectID, "moved": true}, nil
	}
}

func handleTaskSetPosition(r *Registry) mcp.ToolHandlerFor[TaskSetPositionIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in TaskSetPositionIn) (*mcp.CallToolResult, any, error) {
		viewID, bucketID := in.ViewID, in.BucketID
		if in.Bucket != "" {
			if in.ProjectID == 0 {
				return nil, nil, fmt.Errorf("project_id is required when bucket (name) is used")
			}
			vid, bid, err := resolveBucketByName(ctx, r, in.ProjectID, in.Bucket)
			if err != nil {
				return nil, nil, err
			}
			if viewID == 0 {
				viewID = vid
			}
			bucketID = bid
		}
		if viewID == 0 || bucketID == 0 {
			return nil, nil, fmt.Errorf("view_id and bucket_id (or bucket name) are required")
		}
		maxPos, err := maxBucketPositions(ctx, r, in.ProjectID, viewID)
		if err != nil {
			return nil, nil, fmt.Errorf("read board: %w", err)
		}
		if err := moveAndPlace(ctx, r, in.ProjectID, viewID, bucketID, in.TaskID, maxPos); err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{
			"task_id":   in.TaskID,
			"view_id":   viewID,
			"bucket_id": bucketID,
			"position":  maxPos[bucketID],
			"moved":     true,
		}, nil
	}
}

func handleTaskSetReminders(r *Registry) mcp.ToolHandlerFor[TaskSetRemindersIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in TaskSetRemindersIn) (*mcp.CallToolResult, any, error) {
		reminders := make([]string, 0, len(in.Reminders))
		for _, t := range in.Reminders {
			reminders = append(reminders, t.UTC().Format(time.RFC3339))
		}
		if err := r.api().SetReminders(ctx, in.TaskID, reminders, nil); err != nil {
			return nil, nil, err
		}
		var check map[string]any
		if err := r.api().GetTask(ctx, in.TaskID, &check); err != nil {
			return nil, nil, fmt.Errorf("verify reminders: %w", err)
		}
		got := check["reminders"]
		n := anyLen(got)
		if n != len(reminders) {
			return nil, nil, fmt.Errorf("reminders did not stick: sent %d, task has %d", len(reminders), n)
		}
		return nil, map[string]any{"task_id": in.TaskID, "reminders": got, "count": n}, nil
	}
}

func handleTaskAssignUser(r *Registry) mcp.ToolHandlerFor[TaskAssignUserIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in TaskAssignUserIn) (*mcp.CallToolResult, any, error) {
		var out map[string]any
		if err := r.api().AssignUser(ctx, in.TaskID, in.UserID, &out); err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"task_id": in.TaskID, "user_id": in.UserID, "assigned": true}, nil
	}
}

func handleTaskUnassignUser(r *Registry) mcp.ToolHandlerFor[TaskUnassignUserIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in TaskUnassignUserIn) (*mcp.CallToolResult, any, error) {
		if err := r.api().UnassignUser(ctx, in.TaskID, in.UserID); err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"task_id": in.TaskID, "user_id": in.UserID, "unassigned": true}, nil
	}
}
