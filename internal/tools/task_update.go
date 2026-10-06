package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"viktor/internal/client"
)

type TaskUpdateIn struct {
	TaskID      int     `json:"task_id"`
	Title       *string `json:"title,omitempty"`
	Description *string `json:"description,omitempty"`
	DueDate     *string `json:"due_date,omitempty"`
	StartDate   *string `json:"start_date,omitempty"`
	EndDate     *string `json:"end_date,omitempty"`
	Priority    *int    `json:"priority,omitempty"`
}

func parseNullableDate(name, raw string) (client.NullTime, error) {
	if raw == "" {
		return client.NullTime{}, nil
	}
	ts, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return client.NullTime{}, fmt.Errorf("%s: need RFC3339 (e.g. 2026-10-05T12:00:00Z), got %q", name, raw)
	}
	return client.NullTime{Time: ts, Valid: true}, nil
}

func mergeTaskUpdate(current map[string]any, patch TaskUpdateIn) (map[string]any, error) {
	merged := make(map[string]any, len(current)+1)
	for k, v := range current {
		merged[k] = v
	}
	setDate := func(key string, raw *string) error {
		if raw == nil {
			return nil
		}
		nt, err := parseNullableDate(key, *raw)
		if err != nil {
			return err
		}
		merged[key] = nt
		return nil
	}
	if patch.Title != nil {
		merged["title"] = *patch.Title
	}
	if patch.Description != nil {
		merged["description"] = *patch.Description
	}
	for key, raw := range map[string]*string{
		"due_date":   patch.DueDate,
		"start_date": patch.StartDate,
		"end_date":   patch.EndDate,
	} {
		if err := setDate(key, raw); err != nil {
			return nil, err
		}
	}
	if patch.Priority != nil {
		merged["priority"] = *patch.Priority
	}
	return merged, nil
}

func handleTaskUpdate(r *Registry) mcp.ToolHandlerFor[TaskUpdateIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in TaskUpdateIn) (*mcp.CallToolResult, any, error) {
		var current map[string]any
		if err := r.api().GetTask(ctx, in.TaskID, &current); err != nil {
			return nil, nil, err
		}
		merged, err := mergeTaskUpdate(current, in)
		if err != nil {
			return nil, nil, err
		}
		var updated map[string]any
		if err := r.api().UpdateTask(ctx, in.TaskID, merged, &updated); err != nil {
			return nil, nil, err
		}
		return nil, updated, nil
	}
}
