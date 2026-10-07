package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"viktor/internal/client"
	"viktor/internal/shadow"
)

// SyncReport is the result of replaying the shadow journal onto the tracker.
type SyncReport struct {
	Replayed        int      `json:"replayed"`
	Created         int      `json:"created"`
	Updated         int      `json:"updated"`
	Completed       int      `json:"completed"`
	Labels          int      `json:"labels"`
	Comments        int      `json:"comments"`
	Deduplicated    int      `json:"deduplicated"`
	DeletedShadowed int      `json:"deleted_shadowed"`
	Pending         int      `json:"pending"`
	Conflicts       []string `json:"conflicts,omitempty"`
	Failed          []string `json:"failed,omitempty"`
}

func (s *SyncReport) fail(shadowID, op string, err error) {
	s.Failed = append(s.Failed, fmt.Sprintf("%s (%s): %v", shadowID, op, err))
}

func (s *SyncReport) conflict(msg string) {
	s.Conflicts = append(s.Conflicts, msg)
}

func entryPayload(e shadow.Entry) (map[string]any, error) {
	if len(e.Payload) == 0 {
		return map[string]any{}, nil
	}
	var m map[string]any
	if err := json.Unmarshal(e.Payload, &m); err != nil {
		return nil, fmt.Errorf("bad payload: %w", err)
	}
	return m, nil
}

func payloadInt(m map[string]any, key string) int {
	if v, ok := m[key].(float64); ok {
		return int(v)
	}
	return 0
}

func payloadString(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// createBody rebuilds the task body from a shadowed task_create payload.
func createBody(m map[string]any) map[string]any {
	body := map[string]any{"title": m["title"]}
	if d := payloadString(m, "description"); d != "" {
		body["description"] = d
	}
	if p := payloadInt(m, "priority"); p != 0 {
		body["priority"] = p
	}
	if ra := payloadInt(m, "repeat_after"); ra != 0 {
		body["repeat_after"] = ra
	}
	if rm := payloadInt(m, "repeat_mode"); rm != 0 {
		body["repeat_mode"] = rm
	}
	for key, src := range map[string]string{
		"due_date": "due_date", "start_date": "start_date", "end_date": "end_date",
	} {
		if v := payloadString(m, src); v != "" && !strings.HasPrefix(v, "0001-01-01") {
			body[key] = v
		}
	}
	return body
}

// fingerprintOf builds the dedup key for a shadowed task_create.
func fingerprintOf(projectID int64, body map[string]any) string {
	title, _ := body["title"].(string)
	due, _ := body["due_date"].(string)
	return fmt.Sprintf("%d|%s|%s", projectID, title, due)
}

// remoteFingerprint fetches existing open tasks of the project and returns
// the id of a task matching the fingerprint (dedup of partial replays).
func remoteFingerprint(ctx context.Context, api *client.Client, projectID int64, fp string) (int, error) {
	var tasks []map[string]any
	if err := api.ProjectTasks(ctx, int(projectID), client.ListParams{}, &tasks); err != nil {
		return 0, err
	}
	for _, t := range tasks {
		if done, _ := t["done"].(bool); done {
			continue
		}
		title, _ := t["title"].(string)
		due, _ := t["due_date"].(string)
		candidate := fmt.Sprintf("%d|%s|%s", projectID, title, normalizeDue(due))
		if candidate == fp {
			if id, ok := t["id"].(float64); ok {
				return int(id), nil
			}
		}
	}
	return 0, nil
}

// normalizeDue trims the zero-date and normalizes RFC3339 with offset to UTC
// so that fingerprints from different sources match.
func normalizeDue(due string) string {
	if strings.HasPrefix(due, "0001-01-01") {
		return ""
	}
	if t, err := time.Parse(time.RFC3339, due); err == nil {
		return t.UTC().Format(time.RFC3339)
	}
	return due
}

// syncShadow replays pending journal entries onto the tracker in order.
func (r *Registry) syncShadow(ctx context.Context) SyncReport {
	report := SyncReport{}
	if r.shadowStore == nil {
		return report
	}
	api := r.api()
	touched := map[int64]string{} // projectID -> mirror title

	for _, e := range r.shadowStore.Pending() {
		m, err := entryPayload(e)
		if err != nil {
			report.fail(e.ShadowID, e.Op, err)
			continue
		}
		switch e.Op {
		case "create":
			body := createBody(m)
			fp := fingerprintOf(e.ProjectID, body)
			existing, err := remoteFingerprint(ctx, api, e.ProjectID, fp)
			if err != nil {
				report.fail(e.ShadowID, e.Op, err)
				continue
			}
			remoteID := existing
			if remoteID != 0 {
				report.Deduplicated++
			} else {
				var created map[string]any
				if err := api.CreateTask(ctx, int(e.ProjectID), body, &created); err != nil {
					report.fail(e.ShadowID, e.Op, err)
					continue
				}
				if id, ok := created["id"].(float64); ok {
					remoteID = int(id)
				}
				report.Created++
			}
			if err := r.shadowStore.MarkSynced(e.ShadowID, int64(remoteID)); err != nil {
				report.fail(e.ShadowID, e.Op, err)
				continue
			}
			touched[e.ProjectID] = ""
			report.Replayed++

		case "update":
			taskID := payloadInt(m, "task_id")
			if taskID == 0 {
				report.fail(e.ShadowID, e.Op, fmt.Errorf("no task_id in payload"))
				continue
			}
			var current map[string]any
			if err := api.GetTask(ctx, taskID, &current); err != nil {
				report.fail(e.ShadowID, e.Op, err)
				continue
			}
			if remoteUpdated, _ := current["updated"].(string); remoteUpdated != "" {
				if rt, err1 := time.Parse(time.RFC3339, remoteUpdated); err1 == nil {
					if et, err2 := time.Parse(time.RFC3339, e.TS); err2 == nil && rt.After(et) {
						report.conflict(fmt.Sprintf("%s: task %d changed on tracker after %s — local edits applied anyway",
							e.ShadowID, taskID, e.TS))
					}
				}
			}
			patch := map[string]any{}
			for _, key := range []string{"title", "description", "due_date", "start_date", "end_date"} {
				if v, ok := m[key].(string); ok && v != "" {
					patch[key] = v
				}
			}
			if p, ok := m["priority"].(float64); ok {
				patch["priority"] = int(p)
			}
			if len(patch) > 0 {
				if err := api.UpdateTask(ctx, taskID, patch, nil); err != nil {
					report.fail(e.ShadowID, e.Op, err)
					continue
				}
				report.Updated++
			}
			if err := r.shadowStore.MarkSynced(e.ShadowID, int64(taskID)); err != nil {
				report.fail(e.ShadowID, e.Op, err)
				continue
			}
			report.Replayed++

		case "complete":
			taskID := payloadInt(m, "task_id")
			if taskID == 0 {
				report.fail(e.ShadowID, e.Op, fmt.Errorf("no task_id in payload"))
				continue
			}
			if err := api.UpdateTask(ctx, taskID, map[string]any{"done": true}, nil); err != nil {
				report.fail(e.ShadowID, e.Op, err)
				continue
			}
			if err := r.shadowStore.MarkSynced(e.ShadowID, int64(taskID)); err != nil {
				report.fail(e.ShadowID, e.Op, err)
				continue
			}
			report.Completed++
			report.Replayed++

		case "label":
			var out map[string]any
			if err := api.AddTaskLabel(ctx, payloadInt(m, "task_id"), payloadInt(m, "label_id"), &out); err != nil {
				report.fail(e.ShadowID, e.Op, err)
				continue
			}
			if err := r.shadowStore.MarkSynced(e.ShadowID, int64(payloadInt(m, "task_id"))); err != nil {
				report.fail(e.ShadowID, e.Op, err)
				continue
			}
			report.Labels++
			report.Replayed++

		case "comment":
			var out map[string]any
			if err := api.AddComment(ctx, payloadInt(m, "task_id"), map[string]any{"comment": payloadString(m, "text")}, &out); err != nil {
				report.fail(e.ShadowID, e.Op, err)
				continue
			}
			if err := r.shadowStore.MarkSynced(e.ShadowID, int64(payloadInt(m, "task_id"))); err != nil {
				report.fail(e.ShadowID, e.Op, err)
				continue
			}
			report.Comments++
			report.Replayed++

		default:
			report.fail(e.ShadowID, e.Op, fmt.Errorf("operation is not replayed automatically"))
		}
	}

	report.DeletedShadowed = r.cleanupMirror(touched)
	report.Pending = r.shadowStore.PendingCount()
	r.logInfo("shadow: sync finished",
		"replayed", report.Replayed, "created", report.Created, "updated", report.Updated,
		"completed", report.Completed, "pending", report.Pending,
		"failed", len(report.Failed), "conflicts", len(report.Conflicts))
	return report
}

// cleanupMirror removes synced shadow-created task snapshots, regenerates
// project.md and drops empty mirrored projects.
func (r *Registry) cleanupMirror(touched map[int64]string) int {
	deleted := 0
	for projectID := range touched {
		ref, ok := r.shadowStore.FindProjectByID(projectID)
		if !ok {
			continue
		}
		keys, err := r.shadowStore.TaskKeys(projectID, ref.Title)
		if err != nil {
			continue
		}
		for _, key := range keys {
			var snap shadow.TaskSnapshot
			if r.shadowStore.LoadTask(projectID, ref.Title, key, &snap) != nil {
				continue
			}
			if snap.ShadowID == "" {
				continue // not a shadow-created card
			}
			all, err := r.shadowStore.ReadAll()
			if err != nil {
				continue
			}
			synced := false
			for _, e := range all {
				if e.ShadowID == snap.ShadowID && e.Status == "synced" {
					synced = true
					break
				}
			}
			if !synced {
				continue
			}
			if err := r.shadowStore.DeleteTask(projectID, ref.Title, key); err == nil {
				deleted++
			}
		}
		if err := r.shadowStore.RenderProject(projectID, ref.Title); err == nil {
			if keys, _ := r.shadowStore.TaskKeys(projectID, ref.Title); len(keys) == 0 {
				_ = r.shadowStore.DeleteProject(projectID, ref.Title)
			}
		}
	}
	return deleted
}
