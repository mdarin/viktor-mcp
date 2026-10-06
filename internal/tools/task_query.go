package tools

import (
	"context"
	"fmt"
	"sort"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"viktor/internal/client"
)

var queryModes = map[string]string{
	"overdue":       "due_date < now && done = false",
	"due-today":     "due_date < now+1d && done = false",
	"due-week":      "due_date < now+7d && done = false",
	"high-priority": "priority >= 4 && done = false",
	"urgent":        "priority >= 5 || due_date < now",
}

func isUnscheduled(t map[string]any) bool {
	dd, ok := t["due_date"].(string)
	return !ok || dd == "" || dd == zeroDate
}

func fetchUnscheduled(ctx context.Context, r *Registry, projectID int) ([]map[string]any, error) {
	filter := "done = false"
	if projectID != 0 {
		filter = fmt.Sprintf("project_id = %d && %s", projectID, filter)
	}
	tasks, err := fetchFiltered(ctx, r, filter, client.ListParams{PerPage: 200})
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, t := range tasks {
		if isUnscheduled(t) {
			out = append(out, t)
		}
	}
	return out, nil
}

func validModes() string {
	modes := make([]string, 0, len(queryModes)+2)
	for m := range queryModes {
		modes = append(modes, m)
	}
	modes = append(modes, "focus", "summary")
	sort.Strings(modes)
	out := ""
	for i, m := range modes {
		if i > 0 {
			out += ", "
		}
		out += m
	}
	return out
}

func sortTasksByPriority(tasks []map[string]any) {
	sort.SliceStable(tasks, func(i, j int) bool {
		pi, pj := asInt(tasks[i]["priority"]), asInt(tasks[j]["priority"])
		if pi != pj {
			return pi > pj
		}
		return fmt.Sprint(tasks[i]["due_date"]) < fmt.Sprint(tasks[j]["due_date"])
	})
}

func fetchFiltered(ctx context.Context, r *Registry, filter string, p client.ListParams) ([]map[string]any, error) {
	p.Filter = filter
	var tasks []map[string]any
	if err := r.api().ListTasks(ctx, p, &tasks); err != nil {
		return nil, err
	}
	return tasks, nil
}

func handleTaskQuery(r *Registry) mcp.ToolHandlerFor[TaskQueryIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in TaskQueryIn) (*mcp.CallToolResult, any, error) {
		limit := in.Limit
		if limit <= 0 {
			limit = 25
		}
		p := client.ListParams{PerPage: limit}
		if in.ProjectID != 0 {
			p.Filter = fmt.Sprintf("project_id = %d && ", in.ProjectID)
		}
		filter, ok := queryModes[in.Query]
		if !ok {
			switch in.Query {
			case "unscheduled":
				tasks, err := fetchUnscheduled(ctx, r, in.ProjectID)
				if err != nil {
					return nil, nil, err
				}
				sortTasksByPriority(tasks)
				if len(tasks) > limit {
					tasks = tasks[:limit]
				}
				return nil, modeOut("unscheduled", tasks, nil), nil
			case "focus":
				base := p.Filter
				filter = base + queryModes["due-today"]
				p.Filter = ""
				var tasks []map[string]any
				var err error
				if tasks, err = fetchFiltered(ctx, r, filter, p); err != nil {
					return nil, nil, err
				}
				tasks = dropDone(tasks)
				sortTasksByPriority(tasks)
				if len(tasks) > limit {
					tasks = tasks[:limit]
				}
				return nil, modeOut("focus", tasks, nil), nil
			case "summary":
				return taskSummary(ctx, r, in)
			default:
				return nil, nil, fmt.Errorf("unknown mode %q; valid: %s", in.Query, validModes())
			}
		}
		full := p.Filter + filter
		var tasks []map[string]any
		var err error
		if tasks, err = fetchFiltered(ctx, r, full, p); err != nil {
			return nil, nil, err
		}
		tasks = dropDone(tasks)
		sortTasksByPriority(tasks)
		if len(tasks) > limit {
			tasks = tasks[:limit]
		}
		return nil, modeOut(in.Query, tasks, nil), nil
	}
}

func dropDone(tasks []map[string]any) []map[string]any {
	out := tasks[:0]
	for _, t := range tasks {
		if d, _ := t["done"].(bool); !d {
			out = append(out, t)
		}
	}
	return out
}

func modeOut(mode string, tasks []map[string]any, summary map[string]any) map[string]any {
	trimmed := make([]map[string]any, len(tasks))
	for i, t := range tasks {
		trimmed[i] = trimTask(t)
	}
	out := map[string]any{"mode": mode, "count": len(trimmed), "tasks": trimmed}
	for k, v := range summary {
		out[k] = v
	}
	return out
}

func countFiltered(ctx context.Context, r *Registry, filter string, projectID int) (int, error) {
	p := client.ListParams{PerPage: 1}
	if projectID != 0 {
		filter = fmt.Sprintf("project_id = %d && %s", projectID, filter)
	}
	tasks, err := fetchFiltered(ctx, r, filter, p)
	if err != nil {
		return 0, err
	}
	return len(dropDone(tasks)), nil
}

func taskSummary(ctx context.Context, r *Registry, in TaskQueryIn) (*mcp.CallToolResult, any, error) {
	counts := map[string]int{}
	for _, mode := range []string{"overdue", "due-today", "due-week", "high-priority"} {
		n, err := countFiltered(ctx, r, queryModes[mode], in.ProjectID)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", mode, err)
		}
		counts[mode] = n
	}
	unsched, err := fetchUnscheduled(ctx, r, in.ProjectID)
	if err != nil {
		return nil, nil, fmt.Errorf("unscheduled: %w", err)
	}
	counts["unscheduled"] = len(unsched)
	p := client.ListParams{PerPage: 50}
	filter := queryModes["due-week"]
	if in.ProjectID != 0 {
		filter = fmt.Sprintf("project_id = %d && %s", in.ProjectID, filter)
	}
	tasks, err := fetchFiltered(ctx, r, filter, p)
	if err != nil {
		return nil, nil, err
	}
	tasks = dropDone(tasks)
	sortTasksByPriority(tasks)
	if len(tasks) > 5 {
		tasks = tasks[:5]
	}
	return nil, modeOut("summary", tasks, map[string]any{
		"project_id":    in.ProjectID,
		"overdue":       counts["overdue"],
		"due_today":     counts["due-today"],
		"due_week":      counts["due-week"],
		"high_priority": counts["high-priority"],
		"unscheduled":   counts["unscheduled"],
	}), nil
}
