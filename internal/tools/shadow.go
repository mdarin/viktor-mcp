package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"viktor/internal/shadow"
)

const probeCooldown = 30 * time.Second

// shadowWriteScope lists tools whose write effects can be replayed offline.
var shadowWriteScope = map[string]string{
	"task_create":             "create",
	"task_update":             "update",
	"task_complete":           "complete",
	"task_add_label":          "label",
	"comment_add":             "comment",
	"batch_create_tasks":      "batch_create",
	"batch_update_tasks":      "batch_update",
	"batch_relabel":           "batch_relabel",
	"batch_complete_by_label": "batch_complete",
}

// shadowReadScope lists tools servable (partially) from the local mirror.
var shadowReadScope = map[string]bool{
	"task_get":  true,
	"task_list": true,
}

// SetShadowStore enables shadow mode with the given store.
func (r *Registry) SetShadowStore(s *shadow.Store) {
	r.shadowStore = s
}

func (r *Registry) markReachable(ok bool) {
	prev := r.reachable.Swap(ok)
	if prev == ok {
		return
	}
	if ok {
		r.logInfo("shadow: connection restored", "pending", r.shadowPending())
	} else {
		r.logInfo("shadow: tracker unreachable — shadow mode active", "pending", r.shadowPending())
	}
}

func (r *Registry) logInfo(msg string, args ...any) {
	if r.logger != nil {
		r.logger.Info(msg, args...)
	}
}

func (r *Registry) shadowPending() int {
	if r.shadowStore == nil {
		return 0
	}
	return r.shadowStore.PendingCount()
}

// probe checks the tracker with a short timeout, rate-limited by cooldown.
func (r *Registry) probe(ctx context.Context) bool {
	if r.shadowStore == nil {
		return true
	}
	last := r.lastProbe.Load()
	if now := time.Now().Unix(); now-last < int64(probeCooldown.Seconds()) {
		return r.reachable.Load()
	}
	r.lastProbe.Store(time.Now().Unix())
	pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	ok := false
	if _, err := r.api().Info(pctx); err == nil {
		ok = true
	}
	r.markReachable(ok)
	return ok
}

// projectIDOf extracts the ProjectID field from a tool input via reflection.
func projectIDOf(in any) int64 {
	v := reflect.ValueOf(in)
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return 0
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return 0
	}
	f := v.FieldByName("ProjectID")
	if f.IsValid() && f.CanInt() {
		return f.Int()
	}
	return 0
}

// shadowResult builds a non-error result reporting a queued operation.
func shadowResult(res *mcp.CallToolResult, entry *shadow.Entry, queuePos int) *mcp.CallToolResult {
	payload := map[string]any{
		"shadowed":       true,
		"shadow_id":      entry.ShadowID,
		"queue_position": queuePos,
		"op":             entry.Op,
		"message":        "tracker unreachable — operation queued locally; it will sync automatically when the tracker is back (or call shadow_sync)",
	}
	data, _ := json.Marshal(payload)
	res.Content = []mcp.Content{&mcp.TextContent{Text: string(data)}}
	return res
}

// mirrorSnapshot builds a mirror snapshot for a shadowed task_create.
func mirrorSnapshot(entry *shadow.Entry, in any) shadow.TaskSnapshot {
	var p map[string]any
	_ = json.Unmarshal(entry.Payload, &p)
	snap := shadow.TaskSnapshot{ShadowID: entry.ShadowID}
	if v, ok := p["title"].(string); ok {
		snap.Title = v
	}
	if v, ok := p["description"].(string); ok {
		snap.Description = v
	}
	if v, ok := p["priority"].(float64); ok {
		snap.Priority = int64(v)
	}
	if v, ok := p["due_date"].(string); ok {
		snap.DueDate = v
	}
	// Fallback to reflection if payload misses fields.
	if snap.Title == "" {
		snap.Title = fmt.Sprint(reflectField(in, "Title"))
	}
	return snap
}

func reflectField(in any, name string) any {
	v := reflect.ValueOf(in)
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return ""
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return ""
	}
	f := v.FieldByName(name)
	if !f.IsValid() {
		return ""
	}
	return f.Interface()
}

// saveCreateMirror persists the shadowed task_create into the local mirror.
func (r *Registry) saveCreateMirror(entry *shadow.Entry, in any) {
	if r.shadowStore == nil || entry.ProjectID == 0 {
		return
	}
	title := ""
	if ref, ok := r.shadowStore.FindProjectByID(entry.ProjectID); ok {
		title = ref.Title
	} else {
		title = fmt.Sprintf("project-%d", entry.ProjectID)
	}
	_ = r.shadowStore.SaveProject(shadow.ProjectRef{ID: entry.ProjectID, Title: title})
	if err := r.shadowStore.SaveTask(entry.ProjectID, title, entry.ShadowID, mirrorSnapshot(entry, in)); err == nil {
		_ = r.shadowStore.RenderProject(entry.ProjectID, title)
	}
}

// handleUnreachable routes an offline write into the shadow journal.
func handleUnreachable[In, Out any](r *Registry, name string, in In, err error) (*mcp.CallToolResult, Out, error) {
	r.markReachable(false)
	if r.shadowStore == nil {
		return nil, zero[Out](), fmt.Errorf("%w: shadow mode is disabled; operation failed", err)
	}
	op, ok := shadowWriteScope[name]
	if !ok {
		return nil, zero[Out](), fmt.Errorf("%w: tool %q is not supported offline", err, name)
	}
	payload, mErr := json.Marshal(in)
	if mErr != nil {
		payload = nil
	}
	entry := &shadow.Entry{Tool: name, Op: op, ProjectID: projectIDOf(in), Payload: payload}
	if aErr := r.shadowStore.Append(entry); aErr != nil {
		return nil, zero[Out](), fmt.Errorf("%w: shadow queue write failed: %v", err, aErr)
	}
	if op == "create" {
		r.saveCreateMirror(entry, in)
	}
	r.logInfo("shadow: operation queued", "tool", name, "shadow_id", entry.ShadowID)
	res := shadowResult(&mcp.CallToolResult{}, entry, r.shadowStore.PendingCount())
	return res, zero[Out](), nil
}

// serveMirror reads a task or project listing from the local mirror.
func serveMirror[Out any](r *Registry, name string, in any) (*mcp.CallToolResult, Out, error) {
	switch name {
	case "task_get":
		id := fmt.Sprint(reflectField(in, "TaskID"))
		snap, projID, projTitle, ok := r.shadowStore.FindTask(id)
		if !ok {
			break
		}
		data, _ := json.Marshal(map[string]any{
			"shadowed_data": true,
			"shadow_id":     snap.ShadowID,
			"remote_id":     snap.ID,
			"project_id":    projID,
			"project":       projTitle,
			"task":          snap,
			"message":       "tracker unreachable — served from local shadow mirror (may be stale)",
		})
		res := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(data)}}}
		return res, zero[Out](), nil
	case "task_list":
		pid := projectIDOf(in)
		if pid == 0 {
			break
		}
		ref, ok := r.shadowStore.FindProjectByID(pid)
		if !ok {
			break
		}
		tasks := r.shadowStore.TasksOfProject(ref)
		data, _ := json.Marshal(map[string]any{
			"shadowed_data": true,
			"project_id":    pid,
			"project":       ref.Title,
			"count":         len(tasks),
			"tasks":         tasks,
			"message":       "tracker unreachable — served from local shadow mirror (may be stale)",
		})
		res := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(data)}}}
		return res, zero[Out](), nil
	}
	return nil, zero[Out](), fmt.Errorf("tracker unreachable and no local mirror data for this query")
}

func zero[T any]() T { var z T; return z }
