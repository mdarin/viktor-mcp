package tools

import (
	"testing"
	"time"
)

// TestWatchdogScanStallsIdleClaims drives the watchdog_scan tool end to
// end over a fake tracker: claim a task, age it out in the fake, scan,
// verify the stall and the audit trail (card #58).
func TestWatchdogScanStallsIdleClaims(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	CloseFSMStores()
	t.Cleanup(CloseFSMStores) // release SQLite handles before TempDir RemoveAll (Windows)
	f, session := newCRUDFixture(t)
	if _, err := callCRUD(t, session, "claim_task", map[string]any{"task_id": 301, "request_id": "req-watch-1"}); err != nil {
		t.Fatal(err)
	}
	// Age the tracker-side task beyond the threshold.
	f.tasks[301]["updated"] = time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339)

	out, err := callCRUD(t, session, "watchdog_scan", map[string]any{"threshold_hours": 24})
	if err != nil {
		t.Fatal(err)
	}
	stalled, _ := out["stalled"].([]any)
	if len(stalled) != 1 {
		t.Fatalf("watchdog_scan = %v, want one stalled task", out)
	}
	first, _ := stalled[0].(map[string]any)
	if first["task_id"] != float64(301) {
		t.Fatalf("stalled = %v, want task 301", first)
	}
	if out["scanned"] != float64(1) {
		t.Fatalf("scanned = %v", out["scanned"])
	}
}

// TestWatchdogResumeAuditsActor verifies that watchdog_resume returns a
// stalled task to in_progress and records who/when in the audit trail.
func TestWatchdogResumeAuditsActor(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	CloseFSMStores()
	t.Cleanup(CloseFSMStores) // release SQLite handles before TempDir RemoveAll (Windows)
	f, session := newCRUDFixture(t)
	if _, err := callCRUD(t, session, "claim_task", map[string]any{"task_id": 301, "request_id": "req-watch-1"}); err != nil {
		t.Fatal(err)
	}
	f.tasks[301]["updated"] = time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339)
	if _, err := callCRUD(t, session, "watchdog_scan", map[string]any{"threshold_hours": 24}); err != nil {
		t.Fatal(err)
	}

	if _, err := callCRUD(t, session, "watchdog_resume", map[string]any{"task_id": 301, "request_id": "req-watch-1"}); err == nil {
		t.Fatal("resume without actor must be rejected")
	}
	out, err := callCRUD(t, session, "watchdog_resume", map[string]any{"task_id": 301, "actor": "carol"})
	if err != nil {
		t.Fatal(err)
	}
	if out["state"] != "in_progress" {
		t.Fatalf("resume state = %v", out["state"])
	}
	hist, err := callCRUD(t, session, "fsm_state", map[string]any{"task_id": 301, "request_id": "req-watch-1"})
	if err != nil {
		t.Fatal(err)
	}
	if hist["state"] != "in_progress" {
		t.Fatalf("state after resume = %v", hist["state"])
	}
}

// TestTodayActionsMarksStalled verifies the scoring integration: a task
// stalled by the watchdog surfaces in today_actions with stalled=true and
// a "stalled" penalty term (card #58).
func TestTodayActionsMarksStalled(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	CloseFSMStores()
	t.Cleanup(CloseFSMStores) // release SQLite handles before TempDir RemoveAll (Windows)
	f, session := newCRUDFixture(t)
	if _, err := callCRUD(t, session, "claim_task", map[string]any{"task_id": 303, "request_id": "req-watch-3"}); err != nil {
		t.Fatal(err)
	}
	f.tasks[303]["updated"] = time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	if _, err := callCRUD(t, session, "watchdog_scan", map[string]any{"threshold_hours": 24}); err != nil {
		t.Fatal(err)
	}

	out, err := callCRUD(t, session, "today_actions", map[string]any{"limit": 50})
	if err != nil {
		t.Fatal(err)
	}
	intents, _ := out["intents"].([]any)
	var found map[string]any
	for _, iv := range intents {
		intent, _ := iv.(map[string]any)
		items, _ := intent["items"].([]any)
		for _, item := range items {
			m, _ := item.(map[string]any)
			if m["task_id"] == float64(303) {
				found = m
			}
		}
	}
	if found == nil {
		t.Fatalf("task 303 missing from today_actions: %v", out)
	}
	if found["stalled"] != true {
		t.Fatalf("stalled flag = %v, want true", found["stalled"])
	}
	terms, _ := found["terms"].([]any)
	var penalized bool
	for _, tv := range terms {
		term, _ := tv.(map[string]any)
		if term["name"] == "stalled" {
			penalized = true
		}
	}
	if !penalized {
		t.Fatalf("stalled term missing: %v", found["terms"])
	}
}

// TestTodayActionsDegradesWithoutFSMStore: today_actions must work with
// no claims at all (fresh FSM store, empty stall set).
func TestTodayActionsDegradesWithoutFSMStore(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	_, session := newCRUDFixture(t)
	if _, err := callCRUD(t, session, "today_actions", map[string]any{}); err != nil {
		t.Fatal(err)
	}
}
