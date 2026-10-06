package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/sync/errgroup"
)

type BatchGetIn struct {
	TaskIDs     []int `json:"task_ids"`
	Concurrency int   `json:"concurrency,omitempty"`
}

func handleBatchGet(r *Registry) mcp.ToolHandlerFor[BatchGetIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in BatchGetIn) (*mcp.CallToolResult, any, error) {
		if len(in.TaskIDs) == 0 {
			return nil, nil, fmt.Errorf("task_ids must not be empty")
		}
		limit := in.Concurrency
		if limit <= 0 {
			limit = 4
		}
		if limit > 8 {
			limit = 8
		}

		tasks := make([]map[string]any, len(in.TaskIDs))
		errs := make([]error, len(in.TaskIDs))
		g, ctx := errgroup.WithContext(ctx)
		g.SetLimit(limit)
		for i, id := range in.TaskIDs {
			g.Go(func() error {
				var t map[string]any
				if err := r.api().GetTask(ctx, id, &t); err != nil {
					errs[i] = err
					return nil
				}
				tasks[i] = t
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			return nil, nil, err
		}

		found := []map[string]any{}
		failures := []map[string]any{}
		for i, t := range tasks {
			if t != nil {
				found = append(found, t)
				continue
			}
			failures = append(failures, map[string]any{
				"index":   float64(i),
				"task_id": float64(in.TaskIDs[i]),
				"error":   errText(errs[i]),
			})
		}
		return nil, map[string]any{
			"tasks":  found,
			"errors": failures,
		}, nil
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
