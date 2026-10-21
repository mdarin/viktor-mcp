package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"viktor/internal/fsmstate"
)

// gitHead resolves HEAD of the repo backing a submit_for_review; an empty
// repo or a missing repo fails the git-artifact check.
func gitHead(ctx context.Context, repo string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repo, "rev-parse", "HEAD") // #nosec G204 -- repo comes from the caller's config
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git -C %s rev-parse HEAD: %w", repo, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// DefaultFSMPath returns the SQLite path of the FSM store for an instance
// (<XDG_STATE_HOME>/viktor/fsm/<instance>.db, ADR 0004).
func DefaultFSMPath(instance string) string {
	if instance == "" {
		instance = "default"
	}
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return filepath.Join(os.TempDir(), "viktor-fsm", instance+".db")
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "viktor", "fsm", instance+".db")
}

var (
	fsmMu    sync.Mutex
	fsmOpens = map[string]*fsmstate.Store{}
)

// fsmFor returns (and lazily opens) the FSM store of an instance.
func (r *Registry) fsmFor(instance string) (*fsmstate.Store, string, error) {
	if instance == "" {
		instance = r.active
	}
	fsmMu.Lock()
	defer fsmMu.Unlock()
	if s, ok := fsmOpens[instance]; ok {
		return s, instance, nil
	}
	path := DefaultFSMPath(instance)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, instance, fmt.Errorf("fsm: create dir: %w", err)
	}
	s, err := fsmstate.Open(path)
	if err != nil {
		return nil, instance, err
	}
	fsmOpens[instance] = s
	return s, instance, nil
}

// CloseFSMStores closes all open FSM stores (used on shutdown/tests).
func CloseFSMStores() {
	fsmMu.Lock()
	defer fsmMu.Unlock()
	for k, s := range fsmOpens {
		_ = s.Close()
		delete(fsmOpens, k)
	}
}

type ClaimTaskIn struct {
	TaskID    int    `json:"task_id" jsonschema:"task to claim for in_progress work"`
	RequestID string `json:"request_id" jsonschema:"unique request id: a repeat with the same id returns the stored result instead of claiming twice"`
	Actor     string `json:"actor,omitempty" jsonschema:"who claims (bot user name)"`
	Instance  string `json:"instance,omitempty"`
}

type FSMTaskOut struct {
	TaskID    int    `json:"task_id"`
	State     string `json:"state"`
	RequestID string `json:"request_id,omitempty"`
	Actor     string `json:"actor,omitempty"`
	Replayed  bool   `json:"replayed"`
	Allowed   []string `json:"allowed_next"`
}

func handleClaimTask(r *Registry) mcp.ToolHandlerFor[ClaimTaskIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in ClaimTaskIn) (*mcp.CallToolResult, any, error) {
		if in.RequestID == "" {
			return nil, nil, fmt.Errorf("request_id is required for claim_task (idempotency contract)")
		}
		s, inst, err := r.fsmFor(in.Instance)
		if err != nil {
			return nil, nil, err
		}
		var current map[string]any
		api, err := r.clientFor(in.Instance)
		if err != nil {
			return nil, nil, err
		}
		if err := api.GetTask(ctx, in.TaskID, &current); err != nil {
			return nil, nil, err
		}
		if done, _ := current["done"].(bool); done {
			return nil, map[string]any{
				"task_id": in.TaskID, "state": string(fsmstate.StateDone),
				"replayed": false, "allowed_next": fsmStrings(fsmstate.AllowedFrom(fsmstate.StateDone)),
			}, nil
		}
		claim, replayed, err := s.Claim(ctx, inst, in.TaskID, in.RequestID, in.Actor)
		if err != nil {
			return nil, nil, err
		}
		return nil, fsmOut(claim, replayed, claim.State), nil
	}
}

type SubmitForReviewIn struct {
	TaskID   int    `json:"task_id"`
	RequestID string `json:"request_id,omitempty" jsonschema:"unique request id for idempotent submit"`
	Actor    string `json:"actor,omitempty"`
	Repo     string `json:"repo,omitempty" jsonschema:"path to the git repo with the work artifacts; validated before the transition"`
	Note     string `json:"note,omitempty"`
	Instance string `json:"instance,omitempty"`
}

func handleSubmitForReview(r *Registry) mcp.ToolHandlerFor[SubmitForReviewIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in SubmitForReviewIn) (*mcp.CallToolResult, any, error) {
		s, inst, err := r.fsmFor(in.Instance)
		if err != nil {
			return nil, nil, err
		}
		if raw, ok, err := idempotentResult(ctx, s, inst, in.RequestID); err != nil || ok {
			if ok {
				return nil, decodeFSMResult(raw), nil
			}
		}
		// Git artifact check: the repo must exist and resolve HEAD.
		repo := in.Repo
		if repo == "" {
			repo, _ = os.Getwd()
		}
		head, err := gitHead(ctx, repo)
		if err != nil {
			return nil, nil, fmt.Errorf("submit_for_review: git artifact check failed: %w", err)
		}
		detail := in.Note
		if detail != "" {
			detail += " "
		}
		detail += "head=" + head
		claim, err := s.Transition(ctx, inst, in.TaskID, fsmstate.StateInReview, in.RequestID, in.Actor, detail)
		if err != nil {
			return nil, nil, err
		}
		out := fsmOut(claim, false, claim.State)
		out["head"] = head
		if err := s.StoreIdempotent(ctx, inst, in.RequestID, "submit_for_review", out); err != nil {
			return nil, nil, err
		}
		return nil, out, nil
	}
}

type FSMStateIn struct {
	TaskID   int    `json:"task_id"`
	To       string `json:"to,omitempty" jsonschema:"target state; empty = report current state only"`
	RequestID string `json:"request_id,omitempty"`
	Actor    string `json:"actor,omitempty"`
	Instance string `json:"instance,omitempty"`
}

func handleFSMState(r *Registry) mcp.ToolHandlerFor[FSMStateIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in FSMStateIn) (*mcp.CallToolResult, any, error) {
		s, inst, err := r.fsmFor(in.Instance)
		if err != nil {
			return nil, nil, err
		}
		if in.To == "" {
			claim, tracked, err := s.GetClaim(ctx, inst, in.TaskID)
			if err != nil {
				return nil, nil, err
			}
			from := fsmstate.StateBacklog
			if tracked {
				from = claim.State
			}
			return nil, map[string]any{
				"task_id": in.TaskID, "tracked": tracked, "state": string(from),
				"allowed_next": fsmStrings(fsmstate.AllowedFrom(from)),
			}, nil
		}
		to, err := fsmstate.ParseState(in.To)
		if err != nil {
			return nil, nil, err
		}
		if raw, ok, err := idempotentResult(ctx, s, inst, in.RequestID); err != nil || ok {
			if ok {
				return nil, decodeFSMResult(raw), nil
			}
		}
		claim, err := s.Transition(ctx, inst, in.TaskID, to, in.RequestID, in.Actor, "")
		if err != nil {
			return nil, nil, err
		}
		out := fsmOut(claim, false, claim.State)
		if err := s.StoreIdempotent(ctx, inst, in.RequestID, "fsm_state", out); err != nil {
			return nil, nil, err
		}
		return nil, out, nil
	}
}

type GetTaskContextIn struct {
	TaskID   int    `json:"task_id"`
	Instance string `json:"instance,omitempty"`
}

// statusRelevance defines the context budget: which task fields are
// relevant per FSM state (card #56: только релевантное статусу).
var statusRelevance = map[fsmstate.State][]string{
	fsmstate.StateBacklog:    {"id", "title", "description", "labels", "priority", "due_date"},
	fsmstate.StateInProgress: {"id", "title", "description", "labels", "assignees", "due_date", "related_tasks"},
	fsmstate.StateInReview:   {"id", "title", "done", "percent_done", "comments", "labels"},
	fsmstate.StateDone:       {"id", "title", "done", "done_at"},
	fsmstate.StateStalled:    {"id", "title", "updated_at", "description", "labels"},
	fsmstate.StateBlocked:    {"id", "title", "description", "labels", "related_tasks"},
}

func handleGetTaskContext(r *Registry) mcp.ToolHandlerFor[GetTaskContextIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in GetTaskContextIn) (*mcp.CallToolResult, any, error) {
		s, inst, err := r.fsmFor(in.Instance)
		if err != nil {
			return nil, nil, err
		}
		claim, tracked, err := s.GetClaim(ctx, inst, in.TaskID)
		if err != nil {
			return nil, nil, err
		}
		state := fsmstate.StateBacklog
		if tracked {
			state = claim.State
		}
		api, err := r.clientFor(in.Instance)
		if err != nil {
			return nil, nil, err
		}
		var task map[string]any
		if err := api.GetTask(ctx, in.TaskID, &task); err != nil {
			return nil, nil, err
		}
		budget := map[string]any{"task_id": in.TaskID, "state": string(state), "tracked": tracked}
		for _, field := range statusRelevance[state] {
			if v, ok := task[field]; ok && v != nil {
				budget[field] = v
			}
		}
		hist, err := s.History(ctx, inst, in.TaskID)
		if err == nil && len(hist) > 0 {
			budget["history"] = hist
		}
		return nil, budget, nil
	}
}

func fsmOut(c fsmstate.Claim, replayed bool, st fsmstate.State) map[string]any {
	return map[string]any{
		"task_id": c.TaskID, "state": string(st), "request_id": c.RequestID,
		"actor": c.Actor, "replayed": replayed,
		"allowed_next": fsmStrings(fsmstate.AllowedFrom(st)),
	}
}

func fsmStrings(states []fsmstate.State) []string {
	out := make([]string, len(states))
	for i, s := range states {
		out[i] = string(s)
	}
	return out
}

func idempotentResult(ctx context.Context, s *fsmstate.Store, inst, requestID string) (json.RawMessage, bool, error) {
	if requestID == "" {
		return nil, false, nil
	}
	return s.LookupIdempotent(ctx, inst, requestID)
}

func decodeFSMResult(raw json.RawMessage) map[string]any {
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]any{"replayed": true}
	}
	out["replayed"] = true
	return out
}

func registerFsm(r *Registry) {
	Add(r, "claim_task", "Atomically claim a task for in_progress work with request_id idempotency (FSM)", handleClaimTask(r))
	Add(r, "submit_for_review", "Move a claimed task to in_review after validating the git artifact", handleSubmitForReview(r))
	Add(r, "fsm_state", "Report or change the FSM state of a task; forbidden transitions are rejected", handleFSMState(r))
	Add(r, "get_task_context", "Task context budgeted by its FSM state (only status-relevant fields)", handleGetTaskContext(r))
}
