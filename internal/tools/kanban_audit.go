package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type BoardAuditIn struct {
	ProjectID int `json:"project_id"`
	ViewID    int `json:"view_id,omitempty"`
	// Profile overrides the configured board profile (card #845).
	Profile []string `json:"profile,omitempty"`
}

type boardAuditBucket struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
	Count int    `json:"count"`
}

func handleBoardAudit(r *Registry) mcp.ToolHandlerFor[BoardAuditIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in BoardAuditIn) (*mcp.CallToolResult, any, error) {
		if in.ProjectID == 0 {
			return nil, nil, fmt.Errorf("project_id is required")
		}
		viewID := in.ViewID
		if viewID == 0 {
			vid, err := findKanbanViewID(ctx, r, in.ProjectID)
			if err != nil {
				return nil, nil, err
			}
			viewID = vid
		}
		var board []map[string]any
		if err := r.api().ViewTasks(ctx, in.ProjectID, viewID, &board); err != nil {
			return nil, nil, fmt.Errorf("read board: %w", err)
		}
		profile := in.Profile
		if len(profile) == 0 {
			profile = r.boardProfile()
		}
		want := map[string]bool{}
		for _, t := range profile {
			want[strings.ToLower(t)] = true
		}
		have := map[string]bool{}
		buckets := []boardAuditBucket{}
		for _, b := range board {
			title, _ := b["title"].(string)
			have[strings.ToLower(title)] = true
			tasks, _ := b["tasks"].([]any)
			count := len(tasks)
			// The board endpoint caps embedded tasks (default 50) but
			// reports the real total in `count` — prefer it, otherwise
			// done-buckets over 50 look drained (QA #848 wave).
			if n, ok := b["count"].(float64); ok {
				count = int(n)
			}
			buckets = append(buckets, boardAuditBucket{ID: asInt(b["id"]), Title: title, Count: count})
		}
		missing := []string{}
		for _, t := range profile {
			if !have[strings.ToLower(t)] {
				missing = append(missing, t)
			}
		}
		extra := []string{}
		for _, b := range board {
			title, _ := b["title"].(string)
			if !want[strings.ToLower(title)] {
				extra = append(extra, title)
			}
		}
		doneBucketID := 0
		var views []map[string]any
		if err := r.api().ListViews(ctx, in.ProjectID, &views); err == nil {
			for _, v := range views {
				if asInt(v["id"]) == viewID {
					doneBucketID = asInt(v["done_bucket_id"])
				}
			}
		}
		ok := len(missing) == 0 && len(extra) == 0 && doneBucketID != 0
		return nil, map[string]any{
			"ok":             ok,
			"project_id":     in.ProjectID,
			"view_id":        viewID,
			"profile":        profile,
			"missing":        missing,
			"extra":          extra,
			"done_bucket_id": doneBucketID,
			"buckets":        buckets,
		}, nil
	}
}
