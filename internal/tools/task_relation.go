package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var relationMirror = map[string]string{
	"subtask":             "parenttask",
	"parenttask":          "subtask",
	"blocking":            "blocked",
	"blocked":             "blocking",
	"proxypartof":         "proxycontributessto",
	"proxycontributessto": "proxypartof",
	"migratedfrom":        "migratedto",
	"migratedto":          "migratedfrom",
	"related":             "related",
	"duplicate":           "duplicate",
}

func handleTaskCreateRelation(r *Registry) mcp.ToolHandlerFor[TaskCreateRelationIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in TaskCreateRelationIn) (*mcp.CallToolResult, any, error) {
		if in.RelationKind == "" {
			return nil, nil, fmt.Errorf("relation_kind is required (e.g. subtask, parenttask, blocking, blocked, related, duplicate)")
		}
		if in.OtherTaskID == 0 {
			return nil, nil, fmt.Errorf("other_task_id is required")
		}
		if in.OtherTaskID == in.TaskID {
			return nil, nil, fmt.Errorf("cannot relate a task with itself (server rejects with 400/4010)")
		}
		mirror, ok := relationMirror[in.RelationKind]
		if !ok {
			return nil, nil, fmt.Errorf("unknown relation_kind %q; valid: subtask, parenttask, blocking, blocked, proxypartof, proxycontributessto, migratedfrom, migratedto, related, duplicate", in.RelationKind)
		}
		var created map[string]any
		if err := r.api().CreateTaskRelation(ctx, in.TaskID, in.RelationKind, in.OtherTaskID, &created); err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{
			"task_id":       in.TaskID,
			"relation_kind": in.RelationKind,
			"other_task_id": in.OtherTaskID,
			"mirror_kind":   mirror,
			"created":       true,
		}, nil
	}
}

func handleTaskListRelations(r *Registry) mcp.ToolHandlerFor[TaskListRelationsIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in TaskListRelationsIn) (*mcp.CallToolResult, any, error) {
		var task map[string]any
		if err := r.api().Get(ctx, fmt.Sprintf("/tasks/%d?related=true", in.TaskID), nil, &task); err != nil {
			return nil, nil, err
		}
		out := map[string]any{"task_id": in.TaskID, "relations": map[string]any{}, "count": 0}
		relations := map[string]any{}
		count := 0
		if raw, ok := task["related_tasks"].(map[string]any); ok {
			for kind, list := range raw {
				tasks := []map[string]any{}
				if items, ok := list.([]any); ok {
					for _, it := range items {
						if m, ok := it.(map[string]any); ok {
							tasks = append(tasks, map[string]any{
								"id":         m["id"],
								"title":      m["title"],
								"done":       m["done"],
								"project_id": m["project_id"],
							})
							count++
						}
					}
				}
				if len(tasks) > 0 {
					relations[kind] = tasks
				}
			}
		}
		out["relations"] = relations
		out["count"] = count
		return nil, out, nil
	}
}
