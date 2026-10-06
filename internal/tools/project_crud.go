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

func trimProject(m map[string]any) map[string]any {
	keys := []string{"id", "title", "description", "is_favorite", "is_archived", "hex_color", "parent_project_id"}
	out := make(map[string]any, len(keys))
	for _, k := range keys {
		if v, ok := m[k]; ok {
			out[k] = v
		}
	}
	return out
}

func handleProjectList(r *Registry) mcp.ToolHandlerFor[ProjectListIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in ProjectListIn) (*mcp.CallToolResult, any, error) {
		c, err := r.clientFor(in.Instance)
		if err != nil {
			return nil, nil, err
		}
		var projects []map[string]any
		if err := c.ListProjects(ctx, client.ListParams{}, &projects); err != nil {
			return nil, nil, err
		}
		trimmed := make([]map[string]any, len(projects))
		for i, p := range projects {
			trimmed[i] = trimProject(p)
		}
		return nil, map[string]any{"count": len(trimmed), "projects": trimmed}, nil
	}
}

func handleProjectListAll(r *Registry) mcp.ToolHandlerFor[ProjectListAllIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, _ ProjectListAllIn) (*mcp.CallToolResult, any, error) {
		active, clients := r.allClients()
		type instanceRow struct {
			Instance string           `json:"instance"`
			Count    int              `json:"count"`
			Active   bool             `json:"active"`
			Error    string           `json:"error,omitempty"`
			Projects []map[string]any `json:"projects,omitempty"`
		}
		rows := make([]instanceRow, 0, len(clients))
		names := make([]string, 0, len(clients))
		for name := range clients {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			row := instanceRow{Instance: name, Active: name == active}
			var projects []map[string]any
			if err := clients[name].ListProjects(ctx, client.ListParams{}, &projects); err != nil {
				row.Error = err.Error()
			} else {
				row.Count = len(projects)
				trimmed := make([]map[string]any, len(projects))
				for i, p := range projects {
					trimmed[i] = trimProject(p)
				}
				row.Projects = trimmed
			}
			rows = append(rows, row)
		}
		return nil, map[string]any{"instances": rows}, nil
	}
}

func handleProjectGet(r *Registry) mcp.ToolHandlerFor[ProjectGetIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in ProjectGetIn) (*mcp.CallToolResult, any, error) {
		if in.ProjectID == 0 {
			if r.ctxProj != 0 {
				in.ProjectID = r.ctxProj
			} else {
				return nil, nil, fmt.Errorf("project_id is required (or set ctx_set)")
			}
		}
		var p map[string]any
		if err := r.api().GetProject(ctx, in.ProjectID, &p); err != nil {
			return nil, nil, err
		}
		return nil, trimProject(p), nil
	}
}

func handleProjectCreate(r *Registry) mcp.ToolHandlerFor[ProjectCreateIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in ProjectCreateIn) (*mcp.CallToolResult, any, error) {
		if in.Title == "" {
			return nil, nil, fmt.Errorf("title is required")
		}
		body := map[string]any{"title": in.Title}
		if in.Description != "" {
			body["description"] = in.Description
		}
		var created map[string]any
		if err := r.api().CreateProject(ctx, body, &created); err != nil {
			return nil, nil, err
		}
		return nil, trimProject(created), nil
	}
}

func handleProjectUpdate(r *Registry) mcp.ToolHandlerFor[ProjectUpdateIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in ProjectUpdateIn) (*mcp.CallToolResult, any, error) {
		if in.Title == "" && in.Description == "" {
			return nil, nil, fmt.Errorf("nothing to update: provide title or description")
		}
		var current map[string]any
		if err := r.api().GetProject(ctx, in.ProjectID, &current); err != nil {
			return nil, nil, err
		}
		body := map[string]any{"title": in.Title}
		if body["title"] == "" {
			body["title"] = current["title"]
		}
		if in.Description != "" {
			body["description"] = in.Description
		}
		var updated map[string]any
		if err := r.api().UpdateProject(ctx, in.ProjectID, body, &updated); err != nil {
			return nil, nil, err
		}
		return nil, trimProject(updated), nil
	}
}

func handleProjectDelete(r *Registry) mcp.ToolHandlerFor[ProjectDeleteIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in ProjectDeleteIn) (*mcp.CallToolResult, any, error) {
		if !in.Confirm {
			return nil, nil, fmt.Errorf("refused: project_delete removes the project AND all its tasks permanently; pass confirm=true to proceed")
		}
		if in.ProjectID == 0 {
			return nil, nil, fmt.Errorf("project_id is required")
		}
		if err := r.api().DeleteProject(ctx, in.ProjectID); err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"deleted": true, "project_id": in.ProjectID}, nil
	}
}

func handleProjectSetup(r *Registry) mcp.ToolHandlerFor[ProjectSetupIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in ProjectSetupIn) (*mcp.CallToolResult, any, error) {
		if in.ProjectID == 0 {
			return nil, nil, fmt.Errorf("project_id is required")
		}
		columns := in.Buckets
		if len(columns) == 0 {
			columns = []string{"todo", "in-progress", "done"}
		}
		out, err := setupKanbanCore(ctx, r, in.ProjectID, "Kanban", columns, true)
		if err != nil {
			return nil, nil, err
		}
		return nil, out, nil
	}
}

var templateModes = map[string][]string{
	"default": {"todo", "in-progress", "done"},
	"dev":     {"backlog", "todo", "in-progress", "review", "done"},
	"simple":  {"todo", "done"},
	"empty":   {},
}

func handleProjectCreateFromTemplate(r *Registry) mcp.ToolHandlerFor[ProjectCreateFromTemplateIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in ProjectCreateFromTemplateIn) (*mcp.CallToolResult, any, error) {
		if in.Title == "" {
			return nil, nil, fmt.Errorf("title is required")
		}
		mode := in.Mode
		if mode == "" {
			mode = "default"
		}
		columns, ok := templateModes[mode]
		if !ok {
			return nil, nil, fmt.Errorf("unknown mode %q; valid: empty, simple, default, dev", mode)
		}
		var created map[string]any
		if err := r.api().CreateProject(ctx, map[string]any{"title": in.Title}, &created); err != nil {
			return nil, nil, fmt.Errorf("create project: %w", err)
		}
		pid := asInt(created["id"])
		out := map[string]any{"project_id": pid, "title": in.Title, "mode": mode}
		if len(columns) > 0 {
			buckets, err := setupKanbanCore(ctx, r, pid, "Kanban", columns, true)
			if err != nil {
				return nil, nil, err
			}
			for k, v := range buckets {
				out[k] = v
			}
		}
		return nil, out, nil
	}
}

func handleProjectAnalyze(r *Registry) mcp.ToolHandlerFor[ProjectAnalyzeIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in ProjectAnalyzeIn) (*mcp.CallToolResult, any, error) {
		if in.ProjectID == 0 {
			if r.ctxProj != 0 {
				in.ProjectID = r.ctxProj
			} else {
				return nil, nil, fmt.Errorf("project_id is required (or set ctx_set)")
			}
		}
		var tasks []map[string]any
		if err := r.api().ProjectTasks(ctx, in.ProjectID, client.ListParams{}, &tasks); err != nil {
			return nil, nil, err
		}
		open, done, overdue := 0, 0, 0
		byPriority := map[string]int{}
		byBucket := map[string]int{}
		byLabel := map[string]int{}
		for _, t := range tasks {
			if d, _ := t["done"].(bool); d {
				done++
			} else {
				open++
			}
			byPriority[fmt.Sprint(asInt(t["priority"]))]++
			if asInt(t["bucket_id"]) != 0 {
				byBucket[fmt.Sprint(asInt(t["bucket_id"]))]++
			}
			for _, l := range toLabelList(t["labels"]) {
				if lt, _ := l["title"].(string); lt != "" {
					byLabel[lt]++
				}
			}
			if dd, ok := t["due_date"].(string); ok && dd != "" && dd < todayUTC() {
				if d, _ := t["done"].(bool); !d {
					overdue++
				}
			}
		}
		if len(byPriority) == 0 {
			byPriority = nil
		}
		if len(byBucket) == 0 {
			byBucket = nil
		}
		if len(byLabel) == 0 {
			byLabel = nil
		}
		return nil, map[string]any{
			"project_id":  in.ProjectID,
			"total":       len(tasks),
			"open":        open,
			"done":        done,
			"overdue":     overdue,
			"by_priority": byPriority,
			"by_bucket":   byBucket,
			"by_label":    byLabel,
			"suggestions": analyzeSuggestions(open, done, overdue, byLabel, len(tasks)),
		}, nil
	}
}

func analyzeSuggestions(open, done, overdue int, byLabel map[string]int, total int) []string {
	var out []string
	if open == 0 && total > 0 {
		out = append(out, "all tasks are done")
	}
	if overdue > 0 {
		out = append(out, fmt.Sprintf("%d overdue tasks — group by due_date first", overdue))
	}
	if len(byLabel) > 0 {
		out = append(out, fmt.Sprintf("labels present (%d distinct) — groupable by label", len(byLabel)))
	}
	if open > 0 && done == 0 && total > 3 {
		out = append(out, "nothing closed yet — consider triage")
	}
	if out == nil {
		out = []string{}
	}
	return out
}

func todayUTC() string {
	return time.Now().UTC().Format("2006-01-02")
}

func setupKanbanCore(ctx context.Context, r *Registry, projectID int, viewTitle string, columns []string, deleteDefault bool) (map[string]any, error) {
	var views []map[string]any
	if err := r.api().ListViews(ctx, projectID, &views); err != nil {
		return nil, fmt.Errorf("list views: %w", err)
	}
	viewID, view, viewExisted := findKanbanView(views, viewTitle)
	if !viewExisted {
		view = map[string]any{
			"title":                     viewTitle,
			"view_kind":                 "kanban",
			"bucket_configuration_mode": "manual",
		}
		var createdView map[string]any
		if err := r.api().CreateView(ctx, projectID, view, &createdView); err != nil {
			return nil, fmt.Errorf("create view: %w", err)
		}
		viewID = asInt(createdView["id"])
		view = createdView
	}
	var buckets []map[string]any
	if err := r.api().ListBuckets(ctx, projectID, viewID, &buckets); err != nil {
		return nil, fmt.Errorf("list buckets: %w", err)
	}
	byTitle := map[string]int{}
	for _, b := range buckets {
		byTitle[b["title"].(string)] = asInt(b["id"])
	}
	var final []map[string]any
	createdCount := 0
	for i, name := range columns {
		id, exists := byTitle[name]
		title := name
		if !exists {
			for bt, bid := range byTitle {
				if strings.EqualFold(bt, name) {
					id, title, exists = bid, bt, true
					break
				}
			}
		}
		if !exists {
			var created map[string]any
			body := map[string]any{"title": name, "position": i * 1000}
			if err := r.api().CreateBucket(ctx, projectID, viewID, body, &created); err != nil {
				return nil, fmt.Errorf("create bucket %q: %w", name, err)
			}
			id = asInt(created["id"])
			createdCount++
			byTitle[title] = id
		}
		final = append(final, map[string]any{"id": float64(id), "title": title})
	}
	deletedCount := 0
	if deleteDefault {
		kept := map[string]bool{}
		for _, b := range final {
			kept[b["title"].(string)] = true
		}
		for _, b := range buckets {
			title, _ := b["title"].(string)
			if !kept[title] {
				if err := r.api().DeleteBucket(ctx, projectID, viewID, asInt(b["id"])); err != nil {
					return nil, fmt.Errorf("delete bucket %q: %w", title, err)
				}
				deletedCount++
			}
		}
	}
	doneBucketID := 0
	for _, b := range final {
		if strings.EqualFold(b["title"].(string), "done") {
			doneBucketID = asInt(b["id"])
			break
		}
	}
	if doneBucketID == 0 && len(final) > 0 {
		doneBucketID = asInt(final[len(final)-1]["id"])
	}
	if asInt(view["done_bucket_id"]) != doneBucketID {
		view["done_bucket_id"] = doneBucketID
		if err := r.api().UpdateView(ctx, projectID, viewID, view, nil); err != nil {
			return nil, fmt.Errorf("configure done_bucket_id: %w", err)
		}
	}
	return map[string]any{
		"project_id":      projectID,
		"view_id":         viewID,
		"view_existed":    viewExisted,
		"buckets":         final,
		"buckets_created": createdCount,
		"buckets_deleted": deletedCount,
		"done_bucket_id":  doneBucketID,
	}, nil
}
