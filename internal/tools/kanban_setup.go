package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func findKanbanView(views []map[string]any, title string) (int, map[string]any, bool) {
	for _, v := range views {
		if kind, _ := v["view_kind"].(string); kind != "kanban" {
			continue
		}
		vt, _ := v["title"].(string)
		if title == "" || vt == title {
			return asInt(v["id"]), v, true
		}
	}
	return 0, nil, false
}

func handleKanbanSetup(r *Registry) mcp.ToolHandlerFor[KanbanSetupIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in KanbanSetupIn) (*mcp.CallToolResult, any, error) {
		if in.ProjectID == 0 && in.ProjectTitle == "" {
			return nil, nil, fmt.Errorf("project_id or project_title is required")
		}
		if in.ViewTitle == "" {
			in.ViewTitle = "Kanban"
		}
		if len(in.Columns) == 0 {
			// Explicit columns > repo/global board profile > builtin
			// default (card #845).
			in.Columns = r.boardProfile()
		}
		deleteDefault := true
		if in.DeleteDefaultBuckets != nil {
			deleteDefault = *in.DeleteDefaultBuckets
		}

		projectCreated := false
		if in.ProjectID == 0 {
			var proj map[string]any
			if err := r.api().CreateProject(ctx, map[string]any{"title": in.ProjectTitle}, &proj); err != nil {
				return nil, nil, fmt.Errorf("create project: %w", err)
			}
			in.ProjectID = asInt(proj["id"])
			projectCreated = true
		}

		out, err := setupKanbanCore(ctx, r, in.ProjectID, in.ViewTitle, in.Columns, deleteDefault)
		if err != nil {
			return nil, nil, err
		}
		out["project_created"] = projectCreated
		return nil, out, nil
	}
}
