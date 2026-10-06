package scoring

import (
	"errors"
	"testing"
	"time"
)

func door(y int, mo time.Month, d int) *time.Time {
	t := time.Date(y, mo, d, 12, 0, 0, 0, moscow)
	return &t
}

func TestDeferBareSnoozeRefused(t *testing.T) {
	e := NewEngine(fixedNow(), moscow)
	dec, err := e.EvaluateDefer(Task{ID: 1}, DeferRequest{})
	if err == nil || !errors.Is(err, ErrBareSnooze) {
		t.Fatalf("err = %v, want ErrBareSnooze", err)
	}
	if dec.Allowed {
		t.Error("bare snooze must be refused")
	}
}

func TestDeferUnknownReason(t *testing.T) {
	e := NewEngine(fixedNow(), moscow)
	_, err := e.EvaluateDefer(Task{ID: 1}, DeferRequest{Reason: "procrastination"})
	if err == nil || !errors.Is(err, ErrBadReason) {
		t.Fatalf("err = %v, want ErrBadReason", err)
	}
}

func TestDeferAllReasonsInTaxonomy(t *testing.T) {
	e := NewEngine(fixedNow(), moscow)
	for _, reason := range Reasons {
		dec, err := e.EvaluateDefer(Task{ID: 1}, DeferRequest{Reason: reason, Until: time.Now().Add(24 * time.Hour)})
		if err != nil {
			t.Errorf("reason %q rejected: %v", reason, err)
		}
		if !dec.Allowed || dec.NewCount != 1 {
			t.Errorf("reason %q: allowed=%v count=%d", reason, dec.Allowed, dec.NewCount)
		}
	}
}

func TestDeferDreadRequiresDate(t *testing.T) {
	e := NewEngine(fixedNow(), moscow)
	dec, err := e.EvaluateDefer(Task{ID: 1}, DeferRequest{Reason: ReasonDread})
	if err == nil || !errors.Is(err, ErrDateNeeded) {
		t.Fatalf("err = %v, want ErrDateNeeded", err)
	}
	if !dec.RequiresDate {
		t.Error("decision must mark RequiresDate")
	}
}

func TestDeferPastDoorRejected(t *testing.T) {
	e := NewEngine(fixedNow(), moscow)
	task := Task{ID: 1, DoorCloses: door(2026, 10, 10)}
	_, err := e.EvaluateDefer(task, DeferRequest{Reason: ReasonDread, Until: time.Date(2026, 10, 11, 0, 0, 0, 0, moscow)})
	if err == nil || !errors.Is(err, ErrDoorPassed) {
		t.Fatalf("err = %v, want ErrDoorPassed", err)
	}

	ok, err := e.EvaluateDefer(task, DeferRequest{Reason: ReasonDread, Until: time.Date(2026, 10, 9, 0, 0, 0, 0, moscow)})
	if err != nil {
		t.Fatalf("until before door rejected: %v", err)
	}
	if !ok.Allowed {
		t.Error("until before door must be allowed")
	}
}

func TestDeferNoDoorNoLimit(t *testing.T) {
	e := NewEngine(fixedNow(), moscow)
	dec, err := e.EvaluateDefer(Task{ID: 1}, DeferRequest{Reason: "waiting", Until: time.Date(2027, 1, 1, 0, 0, 0, 0, moscow)})
	if err != nil {
		t.Fatal(err)
	}
	if !dec.Allowed {
		t.Error("non-dread defer without door must pass")
	}
	if dec.RequiresDate {
		t.Error("only dread demands a date")
	}
}

func TestRuleOfThree(t *testing.T) {
	e := NewEngine(fixedNow(), moscow)
	third, err := e.EvaluateDefer(Task{ID: 1, DeferredCount: 2, DeferredDread: 2}, DeferRequest{Reason: ReasonDread, Until: time.Date(2026, 10, 9, 0, 0, 0, 0, moscow)})
	if err != nil {
		t.Fatal(err)
	}
	if !third.Escalate {
		t.Error("third dread must raise rule_of_three")
	}
	if third.NewCount != 3 {
		t.Errorf("new count = %d, want 3", third.NewCount)
	}

	second, err := e.EvaluateDefer(Task{ID: 1, DeferredCount: 1, DeferredDread: 1}, DeferRequest{Reason: ReasonDread, Until: time.Date(2026, 10, 9, 0, 0, 0, 0, moscow)})
	if err != nil {
		t.Fatal(err)
	}
	if second.Escalate {
		t.Error("second dread must not escalate")
	}

	nonDread, err := e.EvaluateDefer(Task{ID: 1, DeferredCount: 5, DeferredDread: 5}, DeferRequest{Reason: "waiting", Until: time.Date(2026, 10, 9, 0, 0, 0, 0, moscow)})
	if err != nil {
		t.Fatal(err)
	}
	if nonDread.Escalate {
		t.Error("non-dread defer must not raise rule_of_three even at high count")
	}
}

func TestDeferredEscalationInScore(t *testing.T) {
	e := NewEngine(fixedNow(), moscow)
	fresh := e.Score(Task{ID: 1, Priority: 3})
	deferred := e.Score(Task{ID: 2, Priority: 3, DeferredCount: 2})
	if deferred.Score <= fresh.Score {
		t.Errorf("deferred (%v) must outrank identical fresh (%v)", deferred.Score, fresh.Score)
	}
}
