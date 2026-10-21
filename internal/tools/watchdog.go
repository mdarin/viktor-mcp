package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"viktor/internal/watchdog"
)

type WatchdogScanIn struct {
	ThresholdHours float64 `json:"threshold_hours,omitempty" jsonschema:"idle threshold in hours before a task counts as stalled (default 24)"`
	Instance       string  `json:"instance,omitempty"`
}

// handleWatchdogScan runs one watchdog pass on demand: FSM-tracked
// in_progress tasks with no tracker activity beyond the threshold are
// moved to stalled (card #58).
func handleWatchdogScan(r *Registry) mcp.ToolHandlerFor[WatchdogScanIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in WatchdogScanIn) (*mcp.CallToolResult, any, error) {
		s, inst, err := r.fsmFor(in.Instance)
		if err != nil {
			return nil, nil, err
		}
		api, err := r.clientFor(in.Instance)
		if err != nil {
			return nil, nil, err
		}
		w := watchdog.New(s, inst, api)
		if in.ThresholdHours > 0 {
			w.Threshold = time.Duration(in.ThresholdHours * float64(time.Hour))
		}
		res, err := w.Scan(ctx)
		if err != nil {
			return nil, nil, err
		}
		return nil, res, nil
	}
}

type WatchdogResumeIn struct {
	TaskID   int    `json:"task_id"`
	Actor    string `json:"actor,omitempty" jsonschema:"who returns the task to in_progress (audit trail)"`
	Instance string `json:"instance,omitempty"`
}

// handleWatchdogResume returns a stalled task to in_progress with an
// audit entry naming who returned it and when (card #58).
func handleWatchdogResume(r *Registry) mcp.ToolHandlerFor[WatchdogResumeIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in WatchdogResumeIn) (*mcp.CallToolResult, any, error) {
		if in.Actor == "" {
			return nil, nil, fmt.Errorf("actor is required: the resume is audited (who returned the task)")
		}
		s, inst, err := r.fsmFor(in.Instance)
		if err != nil {
			return nil, nil, err
		}
		w := watchdog.New(s, inst, nil)
		claim, err := w.Resume(ctx, in.TaskID, in.Actor)
		if err != nil {
			return nil, nil, err
		}
		return nil, fsmOut(claim, false, claim.State), nil
	}
}

func registerWatchdog(r *Registry) {
	Add(r, "watchdog_scan", "Stall FSM-tracked in_progress tasks that have seen no tracker activity beyond the threshold", handleWatchdogScan(r))
	Add(r, "watchdog_resume", "Return a stalled task to in_progress with an audit trail (who/when)", handleWatchdogResume(r))
}
