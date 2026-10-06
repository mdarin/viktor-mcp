package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type LabelListIn struct{}

type LabelCreateIn struct {
	Title    string `json:"title"`
	HexColor string `json:"hex_color,omitempty"`
}

type LabelDeleteIn struct {
	LabelID int `json:"label_id"`
}

func taskHasLabel(task map[string]any, labelID int) bool {
	labels, _ := task["labels"].([]any)
	for _, l := range labels {
		if m, ok := l.(map[string]any); ok && asInt(m["id"]) == labelID {
			return true
		}
	}
	return false
}

func handleLabelList(r *Registry) mcp.ToolHandlerFor[LabelListIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, _ LabelListIn) (*mcp.CallToolResult, any, error) {
		var labels []map[string]any
		if err := r.api().ListLabels(ctx, &labels); err != nil {
			return nil, nil, err
		}
		if labels == nil {
			labels = []map[string]any{}
		}
		return nil, map[string]any{"labels": labels, "count": len(labels)}, nil
	}
}

func handleLabelCreate(r *Registry) mcp.ToolHandlerFor[LabelCreateIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in LabelCreateIn) (*mcp.CallToolResult, any, error) {
		if in.Title == "" {
			return nil, nil, fmt.Errorf("title is required")
		}
		body := map[string]any{"title": in.Title}
		if in.HexColor != "" {
			body["hex_color"] = in.HexColor
		}
		var created map[string]any
		if err := r.api().CreateLabel(ctx, body, &created); err != nil {
			return nil, nil, err
		}
		return nil, created, nil
	}
}

func handleLabelDelete(r *Registry) mcp.ToolHandlerFor[LabelDeleteIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in LabelDeleteIn) (*mcp.CallToolResult, any, error) {
		if err := r.api().DeleteLabel(ctx, in.LabelID); err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"deleted": true, "label_id": in.LabelID}, nil
	}
}

func handleTaskAddLabel(r *Registry) mcp.ToolHandlerFor[TaskAddLabelIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in TaskAddLabelIn) (*mcp.CallToolResult, any, error) {
		var task map[string]any
		if err := r.api().GetTask(ctx, in.TaskID, &task); err != nil {
			return nil, nil, fmt.Errorf("get task: %w", err)
		}
		out := map[string]any{"task_id": in.TaskID, "label_id": in.LabelID, "attached": true}
		if taskHasLabel(task, in.LabelID) {
			out["already"] = true
			return nil, out, nil
		}
		if err := r.api().AddTaskLabel(ctx, in.TaskID, in.LabelID, nil); err != nil {
			return nil, nil, err
		}
		var check map[string]any
		if err := r.api().GetTask(ctx, in.TaskID, &check); err != nil {
			return nil, nil, fmt.Errorf("verify attach: %w", err)
		}
		if !taskHasLabel(check, in.LabelID) {
			return nil, nil, fmt.Errorf("label %d did not stick to task %d", in.LabelID, in.TaskID)
		}
		return nil, out, nil
	}
}

func registerLabel(r *Registry) {
	add[LabelListIn](r, "label_list", "List all labels", handleLabelList(r))
	add[LabelCreateIn](r, "label_create", "Create a label", handleLabelCreate(r))
	add[LabelDeleteIn](r, "label_delete", "Delete a label", handleLabelDelete(r))
}
