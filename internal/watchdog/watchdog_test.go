package watchdog

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"path/filepath"
	"testing"
	"time"

	"viktor/internal/fsmstate"
)

type fakeTasks struct {
	tasks map[int]map[string]any
	err   map[int]error
}

func (f *fakeTasks) GetTask(_ context.Context, id int, out any) error {
	if err := f.err[id]; err != nil {
		return err
	}
	t, ok := f.tasks[id]
	if !ok {
		return errors.New("404")
	}
	*(out.(*map[string]any)) = t
	return nil
}

func newStore(t *testing.T) *fsmstate.Store {
	t.Helper()
	s, err := fsmstate.Open(filepath.Join(t.TempDir(), "fsm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func ts(secondsAgo int) string {
	return time.Now().Add(-time.Duration(secondsAgo) * time.Second).UTC().Format(time.RFC3339)
}

func TestScanStallsIdleTasks(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if _, _, err := s.Claim(ctx, "default", 7, "req-1", "alice"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Claim(ctx, "default", 8, "req-2", "bob"); err != nil {
		t.Fatal(err)
	}
	w := New(s, "default", &fakeTasks{tasks: map[int]map[string]any{
		7: {"id": 7, "done": false, "updated": ts(48 * 3600)}, // 2 days idle
		8: {"id": 8, "done": false, "updated": ts(60)},        // fresh
	}})
	w.Threshold = 24 * time.Hour
	var notified []int
	w.Notify = func(st Stalled) { notified = append(notified, st.TaskID) }

	res, err := w.Scan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Scanned != 2 || len(res.Stalled) != 1 || res.Stalled[0].TaskID != 7 {
		t.Fatalf("result = %+v", res)
	}
	if len(notified) != 1 || notified[0] != 7 {
		t.Fatalf("notify = %v", notified)
	}
	c, ok, err := s.GetClaim(ctx, "default", 7)
	if err != nil || !ok {
		t.Fatalf("get claim: %v ok=%v", err, ok)
	}
	if c.State != fsmstate.StateStalled {
		t.Fatalf("state = %s, want stalled", c.State)
	}
	tr, err := s.History(ctx, "default", 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(tr) == 0 || tr[len(tr)-1]["actor"] != "watchdog" {
		t.Fatalf("audit trail = %+v, want watchdog actor", tr)
	}
}

func TestScanSkipsTrackerFailureGracefully(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if _, _, err := s.Claim(ctx, "default", 9, "req-1", "alice"); err != nil {
		t.Fatal(err)
	}
	w := New(s, "default", &fakeTasks{err: map[int]error{9: fmt.Errorf("boom")}})
	w.Threshold = time.Hour
	res, err := w.Scan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Scanned != 1 || len(res.Stalled) != 0 || len(res.Skipped) != 1 {
		t.Fatalf("result = %+v", res)
	}
	c, _, _ := s.GetClaim(ctx, "default", 9)
	if c.State != fsmstate.StateInProgress {
		t.Fatalf("state = %s, claim must survive a tracker outage", c.State)
	}
}

func TestScanSkipsDoneAndStallsNothingFresh(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	for i, actor := range map[int]string{10: "a", 11: "b"} {
		if _, _, err := s.Claim(ctx, "default", i, fmt.Sprintf("req-%d", i), actor); err != nil {
			t.Fatal(err)
		}
	}
	w := New(s, "default", &fakeTasks{tasks: map[int]map[string]any{
		10: {"id": 10, "done": true, "updated": ts(72 * 3600)}, // finished, not yet FSM-updated
		11: {"id": 11, "done": false, "updated": ts(30)},       // fresh
	}})
	w.Threshold = time.Hour
	res, err := w.Scan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Stalled) != 0 || len(res.Skipped) != 0 {
		t.Fatalf("result = %+v", res)
	}
}

func TestResumeAuditTrail(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if _, _, err := s.Claim(ctx, "default", 12, "req-1", "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(ctx, "default", 12, fsmstate.StateStalled, "", "watchdog", "watchdog: idle 2d"); err != nil {
		t.Fatal(err)
	}
	w := New(s, "default", nil)
	c, err := w.Resume(ctx, 12, "carol")
	if err != nil {
		t.Fatal(err)
	}
	if c.State != fsmstate.StateInProgress {
		t.Fatalf("state = %s", c.State)
	}
	tr, err := s.History(ctx, "default", 12)
	if err != nil {
		t.Fatal(err)
	}
	if len(tr) == 0 || tr[len(tr)-1]["actor"] != "carol" || tr[len(tr)-1]["detail"] == "" {
		t.Fatalf("resume audit = %+v", tr)
	}
}

func TestLoopSingleShot(t *testing.T) {
	s := newStore(t)
	w := New(s, "default", &fakeTasks{tasks: map[int]map[string]any{}})
	w.Threshold = time.Hour
	if err := Loop(context.Background(), w, 0, log.New(io.Discard, "", 0)); err != nil {
		t.Fatal(err)
	}
}
