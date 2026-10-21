package fsmstate

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestClaimAtomicAndIdempotent(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)

	c1, replay, err := s.Claim(ctx, "inst", 42, "req-1", "agent-a")
	if err != nil || replay {
		t.Fatalf("first claim: err=%v replay=%v", err, replay)
	}
	if c1.State != StateInProgress {
		t.Fatalf("state = %s, want in_progress", c1.State)
	}

	// Repeat claim with the same request_id → stored result, no duplicate.
	c2, replay, err := s.Claim(ctx, "inst", 42, "req-1", "agent-a")
	if err != nil || !replay {
		t.Fatalf("repeat claim: err=%v replay=%v", err, replay)
	}
	if c2.TaskID != c1.TaskID || c2.ClaimedAt != c1.ClaimedAt {
		t.Fatalf("idempotent result mismatch: %+v vs %+v", c2, c1)
	}

	// Concurrent second claim with a different request_id → conflict.
	var wg sync.WaitGroup
	errCh := make(chan error, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _, err := s.Claim(ctx, "inst", 42, "req-2", "agent-b")
		errCh <- err
	}()
	wg.Wait()
	if err := <-errCh; err == nil {
		t.Fatal("second claim with different request_id must fail (already claimed)")
	}
}

func TestTransitionMatrixEnforced(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)

	if _, _, err := s.Claim(ctx, "inst", 7, "req-a", "agent"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	// in_progress → done is forbidden: the agent physically cannot jump.
	if _, err := s.Transition(ctx, "inst", 7, StateDone, "req-b", "agent", ""); err == nil {
		t.Fatal("in_progress → done must be rejected")
	}
	c, err := s.Transition(ctx, "inst", 7, StateInReview, "req-b", "agent", "submit")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if c.State != StateInReview {
		t.Fatalf("state = %s, want in_review", c.State)
	}
	c, err = s.Transition(ctx, "inst", 7, StateDone, "req-c", "agent", "complete")
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if c.State != StateDone {
		t.Fatalf("state = %s, want done", c.State)
	}
}

func TestUntrackedTaskStartsFromBacklog(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)

	// Untracked task: backlog → done is forbidden by the matrix.
	if _, err := s.Transition(ctx, "inst", 99, StateDone, "req-x", "agent", ""); err == nil {
		t.Fatal("backlog → done must be rejected for untracked task")
	}
	c, err := s.Transition(ctx, "inst", 99, StateInProgress, "req-x", "agent", "adopt")
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	if c.State != StateInProgress {
		t.Fatalf("state = %s, want in_progress", c.State)
	}
}

func TestIdempotencyFirstResultWins(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)

	if _, ok, err := s.LookupIdempotent(ctx, "inst", "req-9"); ok || err != nil {
		t.Fatalf("fresh request_id must miss, got ok=%v err=%v", ok, err)
	}
	if err := s.StoreIdempotent(ctx, "inst", "req-9", "tool", map[string]any{"n": 1}); err != nil {
		t.Fatalf("store: %v", err)
	}
	if err := s.StoreIdempotent(ctx, "inst", "req-9", "tool", map[string]any{"n": 2}); err != nil {
		t.Fatalf("re-store: %v", err)
	}
	raw, ok, err := s.LookupIdempotent(ctx, "inst", "req-9")
	if err != nil || !ok {
		t.Fatalf("lookup: ok=%v err=%v", ok, err)
	}
	if string(raw) != `{"n":1}` {
		t.Fatalf("first result must win, got %s", raw)
	}
	// Empty request_id bypasses idempotency entirely.
	if _, ok, _ := s.LookupIdempotent(ctx, "inst", ""); ok {
		t.Fatal("empty request_id must miss")
	}
	if err := s.StoreIdempotent(ctx, "inst", "", "tool", 1); err != nil {
		t.Fatalf("empty request_id store must be a no-op: %v", err)
	}
}

func TestHistory(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	if _, _, err := s.Claim(ctx, "inst", 5, "r1", "agent"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := s.Transition(ctx, "inst", 5, StateInReview, "r2", "agent", "submit"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	h, err := s.History(ctx, "inst", 5)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(h) != 2 {
		t.Fatalf("history len = %d, want 2", len(h))
	}
	if h[0]["to"] != string(StateInProgress) || h[1]["to"] != string(StateInReview) {
		t.Fatalf("unexpected history order: %+v", h)
	}
}
