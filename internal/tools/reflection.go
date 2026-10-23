package tools

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"viktor/internal/reflection"
)

// ReflectionLogIn is the reflection_log input (card #853): a machine-
// readable journal record of how a goal conflict was resolved.
type ReflectionLogIn struct {
	TaskID int64 `json:"task_id"`
	// GoalConflict is the tension being resolved (required), e.g.
	// "quality vs speed" or "broad fix vs scoped patch".
	GoalConflict string `json:"goal_conflict"`
	// Resolution is what was actually chosen and why (required).
	Resolution string `json:"resolution"`
	// Outcome is the observed result, when known.
	Outcome string `json:"outcome,omitempty"`
	// Confidence is self-reported confidence in the resolution, 0..1.
	Confidence float64 `json:"confidence,omitempty"`
	// Goals maps goal axes to weights at decision time
	// (e.g. {"quality":1,"speed":0.6}).
	Goals    map[string]float64 `json:"goals,omitempty"`
	Repo     string             `json:"repo,omitempty"`
	Comment  bool               `json:"comment,omitempty"` // also post a human-readable task comment
	Instance string             `json:"instance,omitempty"`
}

type ReflectionListIn struct {
	TaskID   int64  `json:"task_id"`
	Limit    int    `json:"limit,omitempty"`
	Instance string `json:"instance,omitempty"`
}

// registerReflection installs the reflection tools (card #853): a
// machine-readable journal of goal-conflict outcomes stored in
// PostgreSQL (canonical) with a best-effort human-readable copy as a
// tracker comment. Without a configured section, or with the database
// down, tools answer {supported:false} — the same degradation contract
// as the memory tools.
func registerReflection(r *Registry) {
	add[ReflectionLogIn](r, "reflection_log", "Log a reflection: how a goal conflict (quality vs speed, follow-up incident) was resolved. Stores in the reflection journal (PostgreSQL) and optionally posts a human-readable task comment. Use after design decisions, trade-offs, and incident post-mortems", handleReflectionLog(r))
	add[ReflectionListIn](r, "reflection_list", "List logged reflections for a task: goal_conflict, resolution, outcome, confidence. Use to recall why a task was resolved the way it was", handleReflectionList(r))
}

// reflectionFor resolves the journal store for an instance: an injected
// store (tests) first, then a cached PG store built from the config.
func (r *Registry) reflectionFor(instance string) (reflection.Store, bool) {
	r.mu.RLock()
	cfgs := r.reflectCfgs
	stores := r.reflectStores
	active := r.active
	r.mu.RUnlock()
	if instance == "" {
		instance = active
	}
	if s, ok := stores[instance]; ok && s != nil {
		return s, true
	}
	cfg, ok := cfgs[instance]
	if !ok || cfg.PGDSN == "" {
		return nil, false
	}
	s, err := reflection.NewPG(cfg)
	if err != nil {
		return nil, false
	}
	r.mu.Lock()
	if r.reflectStores == nil {
		r.reflectStores = map[string]reflection.Store{}
	}
	r.reflectStores[instance] = s
	r.mu.Unlock()
	return s, true
}

// SetReflectionConfigs installs per-instance reflection settings
// (reflection_log / reflection_list, card #853).
func (r *Registry) SetReflectionConfigs(cfgs map[string]reflection.Config) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reflectCfgs = cfgs
}

// SetReflectionStore injects a store directly (tests); it overrides the
// config-derived PG store for that instance.
func (r *Registry) SetReflectionStore(instance string, s reflection.Store) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.reflectStores == nil {
		r.reflectStores = map[string]reflection.Store{}
	}
	r.reflectStores[instance] = s
}

func degradeReflection(err error) map[string]any {
	var un *reflection.ErrUnavailable
	if errors.As(reflection.ClassifyUnavailable(err), &un) {
		return map[string]any{"supported": false, "message": un.Error()}
	}
	return nil
}

func handleReflectionLog(r *Registry) mcp.ToolHandlerFor[ReflectionLogIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in ReflectionLogIn) (*mcp.CallToolResult, any, error) {
		if in.TaskID <= 0 {
			return nil, nil, fmt.Errorf("reflection log: task_id is required")
		}
		if strings.TrimSpace(in.GoalConflict) == "" {
			return nil, nil, fmt.Errorf("reflection log: goal_conflict is required")
		}
		if strings.TrimSpace(in.Resolution) == "" {
			return nil, nil, fmt.Errorf("reflection log: resolution is required")
		}
		store, ok := r.reflectionFor(in.Instance)
		if !ok {
			return nil, notConfigured(in.Instance), nil
		}
		if in.Confidence < 0 {
			in.Confidence = 0
		}
		if in.Confidence > 1 {
			in.Confidence = 1
		}
		id, err := store.Log(ctx, reflection.Record{
			TaskID:       in.TaskID,
			Repo:         in.Repo,
			Goals:        in.Goals,
			GoalConflict: strings.TrimSpace(in.GoalConflict),
			Resolution:   strings.TrimSpace(in.Resolution),
			Outcome:      strings.TrimSpace(in.Outcome),
			Confidence:   in.Confidence,
		})
		if err != nil {
			if row := degradeReflection(err); row != nil {
				return nil, row, nil
			}
			return nil, nil, fmt.Errorf("reflection log: %w", err)
		}
		out := map[string]any{
			"supported": true,
			"id":        id,
			"task_id":   in.TaskID,
		}
		if in.Comment {
			api, cerr := r.clientFor(in.Instance)
			if cerr != nil || api == nil {
				out["comment_error"] = fmt.Sprintf("no client for instance %q", in.Instance)
			} else {
				var commentID any
				if err := api.AddComment(ctx, int(in.TaskID), reflectionCommentText(id, in), &commentID); err != nil {
					out["comment_error"] = err.Error() // journal row is saved; the copy is best-effort
				} else {
					out["comment_posted"] = true
				}
			}
		}
		return nil, out, nil
	}
}

// reflectionCommentText renders the human-readable duplicate posted to
// the tracker alongside the journal row (card #853: the comment keeps
// the record recoverable when the database is down).
func reflectionCommentText(journalID int64, in ReflectionLogIn) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Reflection #%d logged\nGoal conflict: %s\nResolution: %s", journalID, in.GoalConflict, in.Resolution)
	if in.Outcome != "" {
		fmt.Fprintf(&sb, "\nOutcome: %s", in.Outcome)
	}
	if in.Confidence > 0 {
		fmt.Fprintf(&sb, "\nConfidence: %.2f", in.Confidence)
	}
	if len(in.Goals) > 0 {
		axes := make([]string, 0, len(in.Goals))
		for a := range in.Goals {
			axes = append(axes, a)
		}
		sort.Strings(axes)
		parts := make([]string, 0, len(axes))
		for _, a := range axes {
			parts = append(parts, a+"="+strconv.FormatFloat(in.Goals[a], 'f', 2, 64))
		}
		fmt.Fprintf(&sb, "\nGoals: %s", strings.Join(parts, ", "))
	}
	if in.Repo != "" {
		fmt.Fprintf(&sb, "\nRepo: %s", in.Repo)
	}
	return sb.String()
}

func handleReflectionList(r *Registry) mcp.ToolHandlerFor[ReflectionListIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in ReflectionListIn) (*mcp.CallToolResult, any, error) {
		if in.TaskID <= 0 {
			return nil, nil, fmt.Errorf("reflection list: task_id is required")
		}
		store, ok := r.reflectionFor(in.Instance)
		if !ok {
			return nil, notConfigured(in.Instance), nil
		}
		records, err := store.List(ctx, in.TaskID, in.Limit)
		if err != nil {
			if row := degradeReflection(err); row != nil {
				return nil, row, nil
			}
			return nil, nil, fmt.Errorf("reflection list: %w", err)
		}
		return nil, map[string]any{
			"supported": true,
			"task_id":   in.TaskID,
			"records":   records,
			"count":     len(records),
		}, nil
	}
}
