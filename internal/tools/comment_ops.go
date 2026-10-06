package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func commentRow(c map[string]any, taskID int) map[string]any {
	row := map[string]any{
		"id":   c["id"],
		"text": c["comment"],
	}
	if v, ok := c["task_id"]; ok && v != nil {
		row["task_id"] = v
	} else {
		row["task_id"] = taskID
	}
	if a, ok := c["author"].(map[string]any); ok && a["username"] != nil {
		row["author"] = a["username"]
	}
	return row
}

func handleCommentList(r *Registry) mcp.ToolHandlerFor[CommentListIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in CommentListIn) (*mcp.CallToolResult, any, error) {
		var cs []map[string]any
		if err := r.api().TaskComments(ctx, in.TaskID, &cs); err != nil {
			return nil, nil, fmt.Errorf("list comments: %w", err)
		}
		rows := make([]map[string]any, 0, len(cs))
		for _, c := range cs {
			rows = append(rows, commentRow(c, in.TaskID))
		}
		return nil, map[string]any{"task_id": in.TaskID, "comments": rows, "count": len(rows)}, nil
	}
}

func handleCommentAdd(r *Registry) mcp.ToolHandlerFor[CommentAddIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in CommentAddIn) (*mcp.CallToolResult, any, error) {
		if strings.TrimSpace(in.Text) == "" {
			return nil, nil, fmt.Errorf("text is required")
		}
		var out map[string]any
		if err := r.api().AddComment(ctx, in.TaskID, map[string]any{"comment": in.Text}, &out); err != nil {
			return nil, nil, fmt.Errorf("add comment: %w", err)
		}
		return nil, commentRow(out, in.TaskID), nil
	}
}

func handleCommentUpdate(r *Registry) mcp.ToolHandlerFor[CommentUpdateIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in CommentUpdateIn) (*mcp.CallToolResult, any, error) {
		if strings.TrimSpace(in.Text) == "" {
			return nil, nil, fmt.Errorf("text is required")
		}
		var out map[string]any
		if err := r.api().UpdateComment(ctx, in.TaskID, in.CommentID, map[string]any{"comment": in.Text}, &out); err != nil {
			return nil, nil, fmt.Errorf("update comment: %w", err)
		}
		return nil, commentRow(out, in.TaskID), nil
	}
}

func handleCommentDelete(r *Registry) mcp.ToolHandlerFor[CommentDeleteIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in CommentDeleteIn) (*mcp.CallToolResult, any, error) {
		if err := r.api().DeleteComment(ctx, in.TaskID, in.CommentID); err != nil {
			return nil, nil, fmt.Errorf("delete comment: %w", err)
		}
		return nil, map[string]any{"task_id": in.TaskID, "comment_id": in.CommentID, "deleted": true}, nil
	}
}

func handleCommentRecent(_ *Registry) mcp.ToolHandlerFor[CommentRecentIn, any] {
	return func(_ context.Context, _ *mcp.CallToolRequest, _ CommentRecentIn) (*mcp.CallToolResult, any, error) {
		return nil, map[string]any{
			"comments":  []map[string]any{},
			"count":     0,
			"supported": false,
			"message":   "not supported by Vikunja v2.6.0: GET /comments/all returns 404; use comment_list per task instead",
		}, nil
	}
}
