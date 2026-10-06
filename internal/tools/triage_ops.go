package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const triageParkedKey = "triage_parked"

type parkedEntry struct {
	Reason string `json:"reason"`
	At     string `json:"at"`
}

func loadParked() (map[string]parkedEntry, error) {
	s, err := loadSettings()
	if err != nil {
		return nil, err
	}
	out := map[string]parkedEntry{}
	if raw, ok := s[triageParkedKey]; ok && raw != "" {
		if err := json.Unmarshal([]byte(raw), &out); err != nil {
			return nil, fmt.Errorf("%s: invalid JSON: %w", triageParkedKey, err)
		}
	}
	return out, nil
}

func saveParked(m map[string]parkedEntry) error {
	s, err := loadSettings()
	if err != nil {
		return err
	}
	if len(m) == 0 {
		delete(s, triageParkedKey)
	} else {
		data, err := json.Marshal(m)
		if err != nil {
			return err
		}
		s[triageParkedKey] = string(data)
	}
	return saveSettings(s)
}

func setProjectArchived(ctx context.Context, api archiver, projectID int, archived bool) (map[string]any, error) {
	var current map[string]any
	if err := api.GetProject(ctx, projectID, &current); err != nil {
		return nil, err
	}
	if cur, _ := current["is_archived"].(bool); cur == archived {
		return current, nil
	}
	body := map[string]any{
		"title":       current["title"],
		"is_archived": archived,
	}
	var updated map[string]any
	if err := api.UpdateProject(ctx, projectID, body, &updated); err != nil {
		return nil, err
	}
	return updated, nil
}

type archiver interface {
	GetProject(ctx context.Context, projectID int, out any) error
	UpdateProject(ctx context.Context, projectID int, body, out any) error
}

func handleTriagePark(r *Registry) mcp.ToolHandlerFor[TriageParkIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in TriageParkIn) (*mcp.CallToolResult, any, error) {
		if in.ProjectID == 0 {
			return nil, nil, fmt.Errorf("project_id is required")
		}
		reason := in.Reason
		if reason == "" {
			reason = "speculative"
		}
		parked, err := loadParked()
		if err != nil {
			return nil, nil, err
		}
		already := false
		if _, ok := parked[strconv.Itoa(in.ProjectID)]; ok {
			already = true
		}
		updated, err := setProjectArchived(ctx, r.api(), in.ProjectID, true)
		if err != nil {
			return nil, nil, fmt.Errorf("park project %d: %w", in.ProjectID, err)
		}
		now := time.Now().UTC().Format(time.RFC3339)
		if prev, ok := parked[strconv.Itoa(in.ProjectID)]; ok && already {
			now = prev.At
		}
		parked[strconv.Itoa(in.ProjectID)] = parkedEntry{Reason: reason, At: now}
		if err := saveParked(parked); err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{
			"project_id":     in.ProjectID,
			"title":          updated["title"],
			"archived":       updated["is_archived"],
			"already_parked": already,
			"reason":         reason,
			"parked_at":      now,
			"message":        "parked: project tree archived (children archived automatically by Vikunja); reversible via unarchive",
		}, nil
	}
}

func handleTriageParked(r *Registry) mcp.ToolHandlerFor[TriageParkedIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in TriageParkedIn) (*mcp.CallToolResult, any, error) {
		parked, err := loadParked()
		if err != nil {
			return nil, nil, err
		}
		rows := []map[string]any{}
		restored := []int{}
		missing := []int{}
		ids := []int{}
		for raw := range parked {
			id, err := strconv.Atoi(raw)
			if err != nil {
				continue
			}
			ids = append(ids, id)
		}
		sort.Ints(ids)
		for _, id := range ids {
			if in.ProjectID != 0 && id != in.ProjectID {
				continue
			}
			entry := parked[strconv.Itoa(id)]
			var proj map[string]any
			if err := r.api().GetProject(ctx, id, &proj); err != nil {
				missing = append(missing, id)
				continue
			}
			if arch, _ := proj["is_archived"].(bool); !arch {
				restored = append(restored, id)
				continue
			}
			rows = append(rows, map[string]any{
				"project_id": id,
				"title":      proj["title"],
				"reason":     entry.Reason,
				"parked_at":  entry.At,
			})
		}
		if len(missing) > 0 || len(restored) > 0 {
			for _, id := range missing {
				delete(parked, strconv.Itoa(id))
			}
			for _, id := range restored {
				delete(parked, strconv.Itoa(id))
			}
			if err := saveParked(parked); err != nil {
				return nil, nil, err
			}
		}
		out := map[string]any{
			"parked":   rows,
			"count":    len(rows),
			"restored": restored,
			"missing":  missing,
		}
		return nil, out, nil
	}
}
