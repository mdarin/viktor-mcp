package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ShadowStatusIn struct {
	Instance string `json:"instance,omitempty" jsonschema:"optional instance name"`
}

type ShadowClearIn struct {
	Instance string `json:"instance,omitempty" jsonschema:"optional instance name"`
}

func registerShadow(r *Registry) {
	add[ShadowStatusIn](r, "shadow_status", "Show shadow-mode state: online/offline, pending queue size, mirror root", handleShadowStatus(r))
	add[ShadowStatusIn](r, "shadow_sync", "Probe the tracker and report shadow-queue synchronization state", handleShadowSync(r))
	add[ShadowClearIn](r, "shadow_clear", "Remove already-synced entries from the shadow journal", handleShadowClear(r))
}

func handleShadowStatus(r *Registry) mcp.ToolHandlerFor[ShadowStatusIn, any] {
	return func(_ context.Context, _ *mcp.CallToolRequest, _ ShadowStatusIn) (*mcp.CallToolResult, any, error) {
		if r.shadowStore == nil {
			out, _ := json.MarshalIndent(map[string]any{"enabled": false}, "", "  ")
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(out)}}}, nil, nil
		}
		pending := r.shadowStore.PendingCount()
		mode := "online"
		if !r.reachable.Load() {
			mode = "shadow (offline)"
		}
		out := map[string]any{
			"enabled":   true,
			"mode":      mode,
			"reachable": r.reachable.Load(),
			"pending":   pending,
			"root":      r.shadowStore.Root(),
		}
		if pending > 0 {
			var rows []map[string]any
			for _, e := range r.shadowStore.Pending() {
				rows = append(rows, map[string]any{
					"seq": e.Seq, "tool": e.Tool, "op": e.Op, "shadow_id": e.ShadowID, "ts": e.TS,
				})
			}
			out["queue"] = rows
		}
		data, _ := json.MarshalIndent(out, "", "  ")
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(data)}}}, nil, nil
	}
}

func handleShadowSync(r *Registry) mcp.ToolHandlerFor[ShadowStatusIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, _ ShadowStatusIn) (*mcp.CallToolResult, any, error) {
		if r.shadowStore == nil {
			return nil, nil, fmt.Errorf("shadow mode is disabled")
		}
		r.lastProbe.Store(0) // force a fresh probe
		if !r.probe(ctx) {
			out, _ := json.MarshalIndent(map[string]any{
				"synced":  false,
				"pending": r.shadowStore.PendingCount(),
				"message": "tracker still unreachable — queue kept locally",
			}, "", "  ")
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(out)}}}, nil, nil
		}
		pending := r.shadowStore.PendingCount()
		msg := "queue is empty — nothing to sync"
		if pending > 0 {
			msg = "tracker is reachable; replay engine lands with shadow #359 — queue preserved"
		}
		out, _ := json.MarshalIndent(map[string]any{
			"synced":  pending == 0,
			"pending": pending,
			"message": msg,
		}, "", "  ")
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(out)}}}, nil, nil
	}
}

func handleShadowClear(r *Registry) mcp.ToolHandlerFor[ShadowClearIn, any] {
	return func(_ context.Context, _ *mcp.CallToolRequest, _ ShadowClearIn) (*mcp.CallToolResult, any, error) {
		if r.shadowStore == nil {
			return nil, nil, fmt.Errorf("shadow mode is disabled")
		}
		if err := r.shadowStore.ClearSynced(); err != nil {
			return nil, nil, err
		}
		out, _ := json.MarshalIndent(map[string]any{
			"cleared": true,
			"pending": r.shadowStore.PendingCount(),
		}, "", "  ")
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(out)}}}, nil, nil
	}
}
