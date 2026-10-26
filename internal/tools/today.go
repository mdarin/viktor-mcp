package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"viktor/internal/fsmstate"
	"viktor/internal/scoring"

	"viktor/internal/client"
)

const deferMarkerPrefix = "<!-- viktor:deferred="

var deferMarkerRe = regexp.MustCompile(`<!-- viktor:deferred=(\d+) dread=(\d+) -->`)

func parseDeferMarker(desc string) (count, dread int) {
	m := deferMarkerRe.FindStringSubmatch(desc)
	if m == nil {
		return 0, 0
	}
	count, _ = strconv.Atoi(m[1])
	dread, _ = strconv.Atoi(m[2])
	return count, dread
}

func setDeferMarker(desc string, count, dread int) string {
	base := deferMarkerRe.ReplaceAllString(desc, "")
	base = strings.TrimRight(base, " \n\t")
	return fmt.Sprintf("%s\n%s%d dread=%d -->", base, deferMarkerPrefix, count, dread)
}

func toScoringTask(t map[string]any) scoring.Task {
	st := scoring.Task{
		ID:        asInt(t["id"]),
		Priority:  asInt(t["priority"]),
		CreatedAt: parseTimeAny(t["created"]),
		UpdatedAt: parseTimeAny(t["updated"]),
	}
	st.DeferredCount, st.DeferredDread = parseDeferMarker(str(t, "description"))
	if g, _ := scoring.ParseGoals(str(t, "description")); g != nil {
		st.Goals = g
	}
	if d := parseTimeAny(t["due_date"]); !d.IsZero() {
		st.DueDate = &d
	}
	if e := parseTimeAny(t["end_date"]); !e.IsZero() {
		st.DoorCloses = &e
	}
	return st
}

func parseTimeAny(v any) time.Time {
	s, _ := v.(string)
	if s == "" || s == "0001-01-01T00:00:00Z" {
		return time.Time{}
	}
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return ts
}

func str(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

// stalledTaskIDs returns the set of task IDs currently FSM-stalled on an
// instance. The FSM store is advisory for today_*: any failure (no store
// yet, lock contention) degrades to an empty set, never blocks ranking.
func (r *Registry) stalledTaskIDs(ctx context.Context, instance string) map[int]struct{} {
	out := map[int]struct{}{}
	s, inst, err := r.fsmFor(instance)
	if err != nil {
		return out
	}
	claims, err := s.ListByState(ctx, inst, fsmstate.StateStalled)
	if err != nil {
		return out
	}
	for _, c := range claims {
		out[c.TaskID] = struct{}{}
	}
	return out
}

// fetchOpenTasks returns open tasks either for one project (projectID > 0)
// or across the whole instance. The project filter was silently dropped
// before card #887 — the parameter must reach the API.
func fetchOpenTasks(ctx context.Context, r *Registry, projectID int) ([]map[string]any, error) {
	var tasks []map[string]any
	p := client.ListParams{Filter: "done = false"}
	var err error
	if projectID > 0 {
		err = r.api().ProjectTasks(ctx, projectID, p, &tasks)
	} else {
		err = r.api().ListTasks(ctx, p, &tasks)
	}
	if err != nil {
		return nil, err
	}
	if tasks == nil {
		tasks = []map[string]any{}
	}
	return tasks, nil
}

type TodayActionsIn struct {
	Limit        int    `json:"limit,omitempty"`
	IntentFilter string `json:"intent_filter,omitempty"`
	ProjectID    int    `json:"project_id,omitempty"`
}

func handleTodayActions(r *Registry) mcp.ToolHandlerFor[TodayActionsIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in TodayActionsIn) (*mcp.CallToolResult, any, error) {
		limit := in.Limit
		if limit <= 0 {
			limit = 10
		}
		if limit > 50 {
			limit = 50
		}
		tasks, err := fetchOpenTasks(ctx, r, in.ProjectID)
		if err != nil {
			return nil, nil, err
		}
		stalled := r.stalledTaskIDs(ctx, "")
		st := make([]scoring.Task, 0, len(tasks))
		for _, t := range tasks {
			s := toScoringTask(t)
			if _, ok := stalled[s.ID]; ok {
				s.Stalled = true
			}
			st = append(st, s)
		}
		unknownGoals := map[int][]string{}
		for _, t := range tasks {
			if _, unknown := scoring.ParseGoals(str(t, "description")); len(unknown) > 0 {
				unknownGoals[asInt(t["id"])] = unknown
			}
		}
		engine := scoring.NewEngine(time.Now(), time.Local)
		ranked := engine.Rank(st)
		clusters := scoring.ClusterIntents(ranked)
		intents := []map[string]any{}
		total := 0
		for _, c := range clusters {
			if in.IntentFilter != "" && !strings.EqualFold(c.Intent, in.IntentFilter) {
				continue
			}
			items := []map[string]any{}
			for _, s := range c.Items {
				if total >= limit {
					break
				}
				item := map[string]any{
					"task_id": float64(s.Task.ID),
					"score":   s.Score,
					"why":     scoring.WhyTrace(s),
					"terms":   termsOut(s),
					"stalled": s.Task.Stalled,
				}
				if unknown, ok := unknownGoals[s.Task.ID]; ok {
					item["goals_unknown_axes"] = unknown
				}
				items = append(items, item)
				total++
			}
			if len(items) == 0 {
				continue
			}
			intents = append(intents, map[string]any{"intent": c.Intent, "items": items})
		}
		return nil, map[string]any{
			"date":    time.Now().Format("2006-01-02"),
			"intents": intents,
			"count":   total,
		}, nil
	}
}

func termsOut(s scoring.Scored) []map[string]any {
	out := []map[string]any{}
	for _, t := range s.Why {
		out = append(out, map[string]any{"name": t.Name, "value": t.Value})
	}
	return out
}

type TodaySnoozeIn struct {
	TaskID  int    `json:"task_id"`
	Reason  string `json:"reason"`
	Until   string `json:"until,omitempty"`
	Comment string `json:"comment,omitempty"`
}

func handleTodaySnooze(r *Registry) mcp.ToolHandlerFor[TodaySnoozeIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in TodaySnoozeIn) (*mcp.CallToolResult, any, error) {
		if in.TaskID == 0 {
			return nil, nil, fmt.Errorf("task_id is required")
		}
		engine := scoring.NewEngine(time.Now(), time.Local)
		var task map[string]any
		if err := r.api().GetTask(ctx, in.TaskID, &task); err != nil {
			return nil, nil, err
		}
		st := toScoringTask(task)
		req := scoring.DeferRequest{Reason: in.Reason}
		var until time.Time
		if in.Until != "" {
			ts, err := time.Parse(time.RFC3339, in.Until)
			if err != nil {
				return nil, nil, fmt.Errorf("until: need RFC3339 (e.g. 2026-10-07T09:00:00Z), got %q", in.Until)
			}
			until = ts
			req.Until = ts
		}
		dec, err := engine.EvaluateDefer(st, req)
		if err != nil {
			out := map[string]any{"allowed": false, "task_id": float64(in.TaskID), "message": dec.Message}
			if out["message"] == "" {
				out["message"] = err.Error()
			}
			if dec.RequiresDate {
				out["requires_date"] = true
			}
			return nil, out, nil
		}
		body := map[string]any{}
		for k, v := range task {
			body[k] = v
		}
		if !until.IsZero() {
			body["due_date"] = until.UTC().Format(time.RFC3339)
		}
		dread := st.DeferredDread
		if in.Reason == scoring.ReasonDread {
			dread++
		}
		body["description"] = setDeferMarker(str(task, "description"), dec.NewCount, dread)
		body["done"] = false
		var saved map[string]any
		if err := r.api().UpdateTask(ctx, in.TaskID, body, &saved); err != nil {
			return nil, nil, err
		}
		if in.Comment != "" {
			var out any
			if err := r.api().AddComment(ctx, in.TaskID, map[string]any{"comment": in.Comment}, &out); err != nil {
				return nil, nil, fmt.Errorf("comment: %w", err)
			}
		}
		return nil, map[string]any{
			"task_id":        float64(in.TaskID),
			"deferred_count": float64(dec.NewCount),
			"dread_count":    float64(dread),
			"escalate":       dec.Escalate,
			"due_date":       saved["due_date"],
			"message":        dec.Message,
		}, nil
	}
}

type TodayGetWeightsIn struct{}

type TodayReckoningIn struct {
	Threshold int `json:"threshold,omitempty"`
	ProjectID int `json:"project_id,omitempty"`
}

func handleTodayReckoning(r *Registry) mcp.ToolHandlerFor[TodayReckoningIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in TodayReckoningIn) (*mcp.CallToolResult, any, error) {
		threshold := in.Threshold
		if threshold <= 0 {
			threshold = 3
		}
		tasks, err := fetchOpenTasks(ctx, r, in.ProjectID)
		if err != nil {
			return nil, nil, err
		}
		rows := []map[string]any{}
		for _, t := range tasks {
			count, dread := parseDeferMarker(str(t, "description"))
			if count < threshold {
				continue
			}
			rows = append(rows, map[string]any{
				"task_id":        t["id"],
				"title":          t["title"],
				"project_id":     t["project_id"],
				"deferred_count": float64(count),
				"dread_count":    float64(dread),
			})
		}
		sort.SliceStable(rows, func(i, j int) bool {
			return rows[i]["deferred_count"].(float64) > rows[j]["deferred_count"].(float64)
		})
		return nil, map[string]any{"threshold": float64(threshold), "reckoning": rows, "count": len(rows)}, nil
	}
}

const todayWeightsKey = "today_weights"

func handleTodayGetWeights(_ *Registry) mcp.ToolHandlerFor[TodayGetWeightsIn, any] {
	return func(_ context.Context, _ *mcp.CallToolRequest, _ TodayGetWeightsIn) (*mcp.CallToolResult, any, error) {
		overrides, err := loadWeightOverrides()
		if err != nil {
			return nil, nil, err
		}
		weights := scoring.DefaultWeights().Merge(overrides)
		return nil, map[string]any{"weights": weightsOut(weights), "custom": overrides != scoring.Weights{}}, nil
	}
}

func loadWeightOverrides() (scoring.Weights, error) {
	s, err := loadSettings()
	if err != nil {
		return scoring.Weights{}, err
	}
	raw, ok := s[todayWeightsKey]
	if !ok {
		return scoring.Weights{}, nil
	}
	var w scoring.Weights
	if err := json.Unmarshal([]byte(raw), &w); err != nil {
		return scoring.Weights{}, fmt.Errorf("%s: invalid JSON: %w", todayWeightsKey, err)
	}
	return w, nil
}

func weightsOut(w scoring.Weights) map[string]any {
	return map[string]any{
		"overdue":     w.Overdue,
		"priority":    w.Priority,
		"due_soon":    w.DueSoon,
		"door_closes": w.DoorCloses,
		"deferred":    w.Deferred,
		"waiting":     w.Waiting,
		"effort":      w.Effort,
		"stale":       w.Stale,
		"quick_win":   w.QuickWin,
	}
}

type TodaySetWeightsIn struct {
	Weights map[string]float64 `json:"weights"`
}

func handleTodaySetWeights(_ *Registry) mcp.ToolHandlerFor[TodaySetWeightsIn, any] {
	return func(_ context.Context, _ *mcp.CallToolRequest, in TodaySetWeightsIn) (*mcp.CallToolResult, any, error) {
		if len(in.Weights) == 0 {
			return nil, nil, fmt.Errorf("weights must not be empty")
		}
		valid := map[string]bool{
			"overdue": true, "priority": true, "due_soon": true, "door_closes": true,
			"deferred": true, "waiting": true, "effort": true, "stale": true, "quick_win": true,
		}
		var overrides scoring.Weights
		current, err := loadWeightOverrides()
		if err != nil {
			return nil, nil, err
		}
		overrides = current
		cur := map[string]float64{}
		data, _ := json.Marshal(current)
		_ = json.Unmarshal(data, &cur)
		for k, v := range in.Weights {
			if !valid[k] {
				return nil, nil, fmt.Errorf("unknown weight %q; valid: overdue, priority, due_soon, door_closes, deferred, waiting, effort, stale, quick_win", k)
			}
			if v < 0 {
				return nil, nil, fmt.Errorf("weight %q must be >= 0", k)
			}
			cur[k] = v
		}
		data, _ = json.Marshal(cur)
		if err := json.Unmarshal(data, &overrides); err != nil {
			return nil, nil, err
		}
		s, err := loadSettings()
		if err != nil {
			return nil, nil, err
		}
		s[todayWeightsKey] = string(data)
		if err := saveSettings(s); err != nil {
			return nil, nil, err
		}
		weights := scoring.DefaultWeights().Merge(overrides)
		return nil, map[string]any{"saved": true, "weights": weightsOut(weights)}, nil
	}
}

type TriageParkIn struct {
	ProjectID int    `json:"project_id"`
	Reason    string `json:"reason,omitempty"`
}

type TriageParkedIn struct {
	ProjectID int `json:"project_id,omitempty"`
}

func registerToday(r *Registry) {
	add[TriageParkIn](r, "triage_park", "Park a whole speculative project tree aside in one gesture; reversible, never deletes", handleTriagePark(r))
	add[TriageParkedIn](r, "triage_parked", "List currently parked project trees", handleTriageParked(r))
	add[TodayActionsIn](r, "today_actions", "Scored, clustered candidates for today under named intents, each with a numeric score and a why trace", handleTodayActions(r))
	add[TodaySnoozeIn](r, "today_snooze", "Defer a task with a mandatory reason; dread deferrals demand a date and escalate on return", handleTodaySnooze(r))
	add[TodayReckoningIn](r, "today_reckoning", "Weekly kill list: tasks deferred N+ times, loudest first", handleTodayReckoning(r))
	add[TodayGetWeightsIn](r, "today_get_weights", "Read the current effective scoring weights", handleTodayGetWeights(r))
	add[TodaySetWeightsIn](r, "today_set_weights", "Retune deterministic scoring weights at runtime, per user", handleTodaySetWeights(r))
}
