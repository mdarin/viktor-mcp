package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"viktor/internal/client"
)

const queueProjectTitle = "assignment-queue"

func findQueueProject(ctx context.Context, c *client.Client) (int, string, error) {
	var projects []map[string]any
	if err := c.ListProjects(ctx, client.ListParams{}, &projects); err != nil {
		return 0, "", err
	}
	for _, p := range projects {
		title, _ := p["title"].(string)
		if strings.EqualFold(strings.TrimSpace(title), queueProjectTitle) {
			return asInt(p["id"]), title, nil
		}
	}
	return 0, "", fmt.Errorf("no project titled %q on this instance (create it first)", queueProjectTitle)
}

func queueView(ctx context.Context, c *client.Client, projectID int) (int, error) {
	var views []map[string]any
	if err := c.ListViews(ctx, projectID, &views); err != nil {
		return 0, err
	}
	viewID, _, ok := pickKanbanView(views)
	if !ok {
		return 0, fmt.Errorf("no kanban view in queue project %d", projectID)
	}
	return viewID, nil
}

func handleAssignQueue(r *Registry) mcp.ToolHandlerFor[AssignQueueIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in AssignQueueIn) (*mcp.CallToolResult, any, error) {
		c, err := r.clientFor(in.Instance)
		if err != nil {
			return nil, nil, err
		}
		projectID, title, err := findQueueProject(ctx, c)
		if err != nil {
			return nil, nil, err
		}
		viewID, err := queueView(ctx, c, projectID)
		if err != nil {
			return nil, nil, err
		}
		var buckets []map[string]any
		if err := c.ViewTasks(ctx, projectID, viewID, &buckets); err != nil {
			return nil, nil, err
		}
		items := []map[string]any{}
		bucketNames := []string{}
		for _, b := range buckets {
			bucketName, _ := b["title"].(string)
			bucketNames = append(bucketNames, bucketName)
			tasks, _ := b["tasks"].([]any)
			for _, t := range tasks {
				tm, ok := t.(map[string]any)
				if !ok {
					continue
				}
				row := map[string]any{
					"task_id": tm["id"],
					"title":   tm["title"],
					"bucket":  bucketName,
				}
				if p := tm["priority"]; p != nil && asInt(p) != 0 {
					row["priority"] = p
				}
				if d := tm["due_date"]; d != nil && fmt.Sprint(d) != "0001-01-01T00:00:00Z" {
					row["due_date"] = d
				}
				items = append(items, row)
			}
		}
		return nil, map[string]any{
			"instance":   instanceName(r, in.Instance),
			"project_id": projectID,
			"project":    title,
			"view_id":    viewID,
			"buckets":    bucketNames,
			"items":      items,
			"count":      len(items),
		}, nil
	}
}

func instanceName(r *Registry, requested string) string {
	if requested != "" {
		return requested
	}
	active, _ := r.allClients()
	return active
}

func handleAssignApply(r *Registry) mcp.ToolHandlerFor[AssignApplyIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in AssignApplyIn) (*mcp.CallToolResult, any, error) {
		if strings.TrimSpace(in.Destination) == "" {
			return nil, nil, fmt.Errorf("destination bucket name is required (e.g. claim, review, filed)")
		}
		c, err := r.clientFor(in.Instance)
		if err != nil {
			return nil, nil, err
		}
		projectID, title, err := findQueueProject(ctx, c)
		if err != nil {
			return nil, nil, err
		}
		var task map[string]any
		if err := c.GetTask(ctx, in.TaskID, &task); err != nil {
			return nil, nil, fmt.Errorf("task %d: %w", in.TaskID, err)
		}
		if asInt(task["project_id"]) != projectID {
			return nil, nil, fmt.Errorf("task %d is not in the queue project %q (it lives in project %d)", in.TaskID, title, asInt(task["project_id"]))
		}
		viewID, err := queueView(ctx, c, projectID)
		if err != nil {
			return nil, nil, err
		}
		var buckets []map[string]any
		if err := c.ListBuckets(ctx, projectID, viewID, &buckets); err != nil {
			return nil, nil, err
		}
		var dest map[string]any
		for _, b := range buckets {
			if bt, _ := b["title"].(string); strings.EqualFold(strings.TrimSpace(bt), strings.TrimSpace(in.Destination)) {
				dest = b
				break
			}
		}
		if dest == nil {
			names := make([]string, 0, len(buckets))
			for _, b := range buckets {
				names = append(names, fmt.Sprint(b["title"]))
			}
			return nil, nil, fmt.Errorf("bucket %q not found in queue; available: %v", in.Destination, names)
		}
		var out any
		if err := c.MoveTaskToBucket(ctx, projectID, viewID, asInt(dest["id"]), in.TaskID, &out); err != nil {
			return nil, nil, fmt.Errorf("move to %q: %w", in.Destination, err)
		}
		commentAdded := false
		if strings.TrimSpace(in.Notes) != "" {
			var comment map[string]any
			if err := c.AddComment(ctx, in.TaskID, map[string]any{"comment": in.Notes}, &comment); err != nil {
				return nil, nil, fmt.Errorf("add notes: %w", err)
			}
			commentAdded = true
		}
		return nil, map[string]any{
			"applied":       true,
			"instance":      instanceName(r, in.Instance),
			"task_id":       in.TaskID,
			"project_id":    projectID,
			"bucket_id":     dest["id"],
			"bucket":        dest["title"],
			"comment_added": commentAdded,
		}, nil
	}
}

func handleCalAddEvent(r *Registry) mcp.ToolHandlerFor[CalAddEventIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in CalAddEventIn) (*mcp.CallToolResult, any, error) {
		labelName := in.LabelName
		if labelName == "" {
			labelName = "calendar"
		}
		body := map[string]any{"title": in.Title, "due_date": in.DueDate}
		if in.Description != "" {
			body["description"] = in.Description
		}
		if in.StartDate != "" {
			body["start_date"] = in.StartDate
		}
		if in.EndDate != "" {
			body["end_date"] = in.EndDate
		}
		var task map[string]any
		if err := r.api().CreateTask(ctx, in.ProjectID, body, &task); err != nil {
			return nil, nil, fmt.Errorf("create event: %w", err)
		}
		taskID := asInt(task["id"])
		labels, _ := task["labels"].([]any)
		hasLabel := false
		for _, l := range labels {
			if lm, ok := l.(map[string]any); ok && strings.EqualFold(fmt.Sprint(lm["title"]), labelName) {
				hasLabel = true
				break
			}
		}
		labelID := 0
		if !hasLabel {
			var all []map[string]any
			if err := r.api().ListLabels(ctx, client.ListParams{}, &all); err != nil {
				return nil, nil, err
			}
			for _, l := range all {
				if strings.EqualFold(fmt.Sprint(l["title"]), labelName) {
					labelID = asInt(l["id"])
					break
				}
			}
			if labelID == 0 {
				var created map[string]any
				if err := r.api().CreateLabel(ctx, map[string]any{"title": labelName}, &created); err != nil {
					return nil, nil, fmt.Errorf("create label %q: %w", labelName, err)
				}
				labelID = asInt(created["id"])
			}
			var linked map[string]any
			if err := r.api().AddTaskLabel(ctx, taskID, labelID, &linked); err != nil {
				return nil, nil, fmt.Errorf("attach label %q: %w", labelName, err)
			}
		}
		return nil, map[string]any{
			"task_id":    taskID,
			"title":      task["title"],
			"due_date":   task["due_date"],
			"start_date": task["start_date"],
			"end_date":   task["end_date"],
			"label":      labelName,
			"project_id": in.ProjectID,
		}, nil
	}
}
