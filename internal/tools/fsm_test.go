package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"viktor/internal/client"
)

func fsmTestRegistry(t *testing.T, task map[string]any) *Registry {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	CloseFSMStores()
	t.Cleanup(CloseFSMStores) // release SQLite handles before TempDir RemoveAll (Windows)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v2/tasks/") {
			_ = json.NewEncoder(w).Encode(task)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	api := client.New(srv.URL, "test", 10*time.Second)
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.0.1"}, nil)
	r := NewRegistry(s, api)
	r.RegisterAll()
	return r
}

func TestClaimTaskHappyAndReplay(t *testing.T) {
	r := fsmTestRegistry(t, map[string]any{"id": float64(42), "title": "t", "project_id": float64(8), "done": false})
	h := handleClaimTask(r)
	_, out, err := h(context.Background(), nil, ClaimTaskIn{TaskID: 42, RequestID: "r1", Actor: "agent-a"})
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	res := out.(map[string]any)
	if res["state"] != "in_progress" || res["replayed"] != false {
		t.Fatalf("claim result: %+v", res)
	}
	allowed := res["allowed_next"].([]string)
	if len(allowed) == 0 {
		t.Fatal("allowed_next must not be empty")
	}

	_, out, err = h(context.Background(), nil, ClaimTaskIn{TaskID: 42, RequestID: "r1", Actor: "agent-a"})
	if err != nil {
		t.Fatalf("replay claim: %v", err)
	}
	res = out.(map[string]any)
	if res["replayed"] != true {
		t.Fatalf("repeat claim must replay: %+v", res)
	}
}

func TestClaimTaskMissingRequestID(t *testing.T) {
	r := fsmTestRegistry(t, map[string]any{"id": float64(42), "done": false})
	h := handleClaimTask(r)
	if _, _, err := h(context.Background(), nil, ClaimTaskIn{TaskID: 42}); err == nil {
		t.Fatal("claim without request_id must fail")
	}
}

func TestClaimTaskDoneTask(t *testing.T) {
	r := fsmTestRegistry(t, map[string]any{"id": float64(42), "done": true})
	h := handleClaimTask(r)
	_, out, err := h(context.Background(), nil, ClaimTaskIn{TaskID: 42, RequestID: "r1"})
	if err != nil {
		t.Fatalf("claim done task: %v", err)
	}
	res := out.(map[string]any)
	if res["state"] != "done" {
		t.Fatalf("done task claim: %+v", res)
	}
}

func TestSubmitForReviewGitArtifactCheck(t *testing.T) {
	r := fsmTestRegistry(t, map[string]any{"id": float64(42), "done": false})
	claim := handleClaimTask(r)
	if _, _, err := claim(context.Background(), nil, ClaimTaskIn{TaskID: 42, RequestID: "r1"}); err != nil {
		t.Fatalf("claim: %v", err)
	}
	submit := handleSubmitForReview(r)
	// Invalid repo: git-artifact check fails.
	if _, _, err := submit(context.Background(), nil, SubmitForReviewIn{TaskID: 42, Repo: "/nonexistent-repo"}); err == nil {
		t.Fatal("submit with invalid repo must fail")
	}
	// This package lives inside the real git repo: HEAD resolves.
	_, out, err := submit(context.Background(), nil, SubmitForReviewIn{TaskID: 42, Repo: "..", RequestID: "r2"})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	res := out.(map[string]any)
	if res["state"] != "in_review" {
		t.Fatalf("submit result: %+v", res)
	}
	if head, _ := res["head"].(string); len(head) != 40 {
		t.Fatalf("head must be a commit sha: %v", res["head"])
	}
}

func TestFSMStateForbiddenTransition(t *testing.T) {
	r := fsmTestRegistry(t, map[string]any{"id": float64(42), "done": false})
	claim := handleClaimTask(r)
	if _, _, err := claim(context.Background(), nil, ClaimTaskIn{TaskID: 42, RequestID: "r1"}); err != nil {
		t.Fatalf("claim: %v", err)
	}
	h := handleFSMState(r)
	_, _, err := h(context.Background(), nil, FSMStateIn{TaskID: 42, To: "done", RequestID: "r2"})
	if err == nil {
		t.Fatal("in_progress → done must be rejected")
	}
	if !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("error must name the forbidden transition: %v", err)
	}
	// Legal transition still works and reports allowed_next.
	_, out, err := h(context.Background(), nil, FSMStateIn{TaskID: 42, To: "blocked", RequestID: "r3"})
	if err != nil {
		t.Fatalf("block: %v", err)
	}
	if out.(map[string]any)["state"] != "blocked" {
		t.Fatalf("block result: %+v", out)
	}
}

func TestGetTaskContextBudget(t *testing.T) {
	task := map[string]any{
		"id": float64(42), "title": "t", "description": "d", "labels": []any{},
		"assignees": []any{}, "comments": []any{}, "percent_done": float64(0),
		"project_id": float64(8), "done": false,
	}
	r := fsmTestRegistry(t, task)
	claim := handleClaimTask(r)
	if _, _, err := claim(context.Background(), nil, ClaimTaskIn{TaskID: 42, RequestID: "r1"}); err != nil {
		t.Fatalf("claim: %v", err)
	}
	h := handleGetTaskContext(r)
	_, out, err := h(context.Background(), nil, GetTaskContextIn{TaskID: 42})
	if err != nil {
		t.Fatalf("context: %v", err)
	}
	res := out.(map[string]any)
	if res["state"] != "in_progress" {
		t.Fatalf("state: %+v", res)
	}
	if _, ok := res["title"]; !ok {
		t.Fatal("in_progress budget must include title")
	}
	if _, ok := res["percent_done"]; ok {
		t.Fatal("in_progress budget must exclude percent_done")
	}
	if _, ok := res["comments"]; ok {
		t.Fatal("in_progress budget must exclude comments")
	}
}

func TestClaimWorkSubmitCompleteE2E(t *testing.T) {
	r := fsmTestRegistry(t, map[string]any{"id": float64(42), "done": false})
	claim := handleClaimTask(r)
	submit := handleSubmitForReview(r)
	st := handleFSMState(r)

	if _, _, err := claim(context.Background(), nil, ClaimTaskIn{TaskID: 42, RequestID: "r1"}); err != nil {
		t.Fatalf("claim: %v", err)
	}
	_, out, err := submit(context.Background(), nil, SubmitForReviewIn{TaskID: 42, Repo: "..", RequestID: "r2"})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if out.(map[string]any)["state"] != "in_review" {
		t.Fatalf("submit state: %+v", out)
	}
	// in_review → done is the only path to completion.
	_, out, err = st(context.Background(), nil, FSMStateIn{TaskID: 42, To: "done", RequestID: "r3"})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	res := out.(map[string]any)
	if res["state"] != "done" {
		t.Fatalf("complete state: %+v", res)
	}
	// Repeat submit with the same request_id replays the stored result.
	_, out, err = submit(context.Background(), nil, SubmitForReviewIn{TaskID: 42, Repo: "..", RequestID: "r2"})
	if err != nil {
		t.Fatalf("submit replay: %v", err)
	}
	if out.(map[string]any)["replayed"] != true {
		t.Fatalf("submit replay flag: %+v", out)
	}
}
