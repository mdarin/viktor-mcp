package scoring

import (
	"testing"
	"time"
)

var moscow, _ = time.LoadLocation("Europe/Moscow")

func fixedNow() time.Time {
	return time.Date(2026, 10, 5, 12, 0, 0, 0, moscow)
}

func date(y int, mo time.Month, d int) *time.Time {
	t := time.Date(y, mo, d, 12, 0, 0, 0, moscow)
	return &t
}

func TestOverdueCapped(t *testing.T) {
	e := NewEngine(fixedNow(), moscow)
	cases := []struct {
		name string
		due  *time.Time
		want float64
	}{
		{"1 day overdue", date(2026, 10, 4), 1 * e.Weights.Overdue},
		{"7 days overdue", date(2026, 9, 28), 7 * e.Weights.Overdue},
		{"400 days overdue → capped at 7", date(2025, 8, 31), 7 * e.Weights.Overdue},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := e.Score(Task{ID: 1, DueDate: tc.due})
			if got.Score != tc.want {
				t.Errorf("score = %v, want %v; why = %v", got.Score, tc.want, got.Why)
			}
		})
	}
}

func TestDueSoon(t *testing.T) {
	e := NewEngine(fixedNow(), moscow)
	cases := []struct {
		name string
		due  *time.Time
		want float64
	}{
		{"due today", date(2026, 10, 5), 3 * e.Weights.DueSoon},
		{"due in 3 days", date(2026, 10, 8), 0 * e.Weights.DueSoon},
		{"due in 4 days → no term", date(2026, 10, 9), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := e.Score(Task{ID: 1, DueDate: tc.due})
			if got.Score != tc.want {
				t.Errorf("score = %v, want %v", got.Score, tc.want)
			}
		})
	}
}

func TestDeferredCapped(t *testing.T) {
	e := NewEngine(fixedNow(), moscow)
	got := e.Score(Task{ID: 1, DeferredCount: 50})
	if got.Score != float64(e.Caps.DeferredCount)*e.Weights.Deferred {
		t.Errorf("score = %v, want capped deferred", got.Score)
	}
}

func TestWaitingUncapped(t *testing.T) {
	e := NewEngine(fixedNow(), moscow)
	waiting := date(2026, 1, 5)
	got := e.Score(Task{ID: 1, WaitingSince: waiting})
	days := 273.0
	if got.Score != days*e.Weights.Waiting {
		t.Errorf("score = %v, want %v (uncapped)", got.Score, days*e.Weights.Waiting)
	}
}

func TestStalledPenalty(t *testing.T) {
	e := NewEngine(fixedNow(), moscow)
	base := e.Score(Task{ID: 1, Priority: 4})
	got := e.Score(Task{ID: 1, Priority: 4, Stalled: true})
	if got.Score != base.Score+e.Weights.Stalled {
		t.Errorf("score = %v, want %v (base + stalled penalty %v)", got.Score, base.Score+e.Weights.Stalled, e.Weights.Stalled)
	}
	found := false
	for _, term := range got.Why {
		if term.Name == "stalled" {
			found = true
		}
	}
	if !found {
		t.Errorf("stalled term missing from why trace: %v", got.Why)
	}
}

func TestDoorCloses(t *testing.T) {
	e := NewEngine(fixedNow(), moscow)
	cases := []struct {
		name string
		door *time.Time
		want float64
	}{
		{"door today → max", date(2026, 10, 5), float64(e.Caps.DoorDays) * e.Weights.DoorCloses},
		{"door in 2 days", date(2026, 10, 7), float64(e.Caps.DoorDays-2) * e.Weights.DoorCloses},
		{"door in 30 days → capped units", date(2026, 11, 4), 0},
		{"door passed → max", date(2026, 9, 1), float64(e.Caps.DoorDays) * e.Weights.DoorCloses},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := e.Score(Task{ID: 1, DoorCloses: tc.door})
			if got.Score != tc.want {
				t.Errorf("score = %v, want %v", got.Score, tc.want)
			}
		})
	}
}

func TestQuickWinAndEffort(t *testing.T) {
	e := NewEngine(fixedNow(), moscow)
	got := e.Score(Task{ID: 1, Priority: 4, EffortMinutes: 15})
	want := 4*e.Weights.Priority + e.Weights.QuickWin + float64(workdayMinutes-15)/workdayMinutes*e.Weights.Effort
	if got.Score != want {
		t.Errorf("score = %v, want %v; why=%v", got.Score, want, got.Why)
	}

	notQuick := e.Score(Task{ID: 1, Priority: 2, EffortMinutes: 15})
	for _, term := range notQuick.Why {
		if term.Name == "quick_win" {
			t.Error("priority 2 must not fire quick_win")
		}
	}

	big := e.Score(Task{ID: 1, Priority: 4, EffortMinutes: workdayMinutes})
	for _, term := range big.Why {
		if term.Name == "effort" {
			t.Error("full workday effort must not fire effort term")
		}
	}
}

func TestStale(t *testing.T) {
	e := NewEngine(fixedNow(), moscow)
	updated := fixedNow().AddDate(0, 0, -21)
	got := e.Score(Task{ID: 1, UpdatedAt: updated})
	want := float64(21-14) / 7.0 * e.Weights.Stale
	if got.Score != want {
		t.Errorf("score = %v, want %v", got.Score, want)
	}

	fresh := e.Score(Task{ID: 1, UpdatedAt: fixedNow()})
	if len(fresh.Why) != 0 {
		t.Errorf("fresh task fired terms: %v", fresh.Why)
	}
}

func TestDayBoundaryTimezone(t *testing.T) {
	now := time.Date(2026, 10, 5, 23, 59, 0, 0, moscow)
	e := NewEngine(now, moscow)

	sameDay := time.Date(2026, 10, 5, 23, 0, 0, 0, moscow)
	nextDay := time.Date(2026, 10, 6, 1, 0, 0, 0, moscow)

	s1 := e.Score(Task{ID: 1, DueDate: &sameDay})
	s2 := e.Score(Task{ID: 1, DueDate: &nextDay})

	if s1.Score <= s2.Score {
		t.Errorf("due today (%v) must outrank due tomorrow (%v)", s1.Score, s2.Score)
	}

	ny, _ := time.LoadLocation("America/New_York")
	nowMSK := time.Date(2026, 10, 5, 10, 0, 0, 0, moscow)
	due := time.Date(2026, 10, 5, 6, 0, 0, 0, moscow)

	eMSK := NewEngine(nowMSK, moscow)
	eNY := NewEngine(nowMSK.In(ny), ny)

	sMSK := eMSK.Score(Task{ID: 1, DueDate: &due})
	sNY := eNY.Score(Task{ID: 1, DueDate: &due})

	if sMSK.Score == sNY.Score {
		t.Errorf("same instant in different locations must differ: MSK=%v NY=%v", sMSK.Score, sNY.Score)
	}
}

func TestDeterminism(t *testing.T) {
	e := NewEngine(fixedNow(), moscow)
	tasks := []Task{
		{ID: 1, Priority: 5, DueDate: date(2026, 10, 3)},
		{ID: 2, Priority: 3, DoorCloses: date(2026, 10, 6)},
		{ID: 3, Priority: 4, WaitingSince: date(2026, 10, 1)},
		{ID: 4, Priority: 1, UpdatedAt: fixedNow().AddDate(0, 0, -30)},
	}
	r1 := e.Rank(tasks)
	r2 := e.Rank(tasks)
	for i := range r1 {
		if r1[i].Score != r2[i].Score || r1[i].Task.ID != r2[i].Task.ID {
			t.Fatalf("ranking not deterministic: %v vs %v", r1, r2)
		}
	}
	want := []int{1, 3, 2, 4}
	for i, id := range want {
		if r1[i].Task.ID != id {
			t.Errorf("rank[%d] = %d, want %d (score %v)", i, r1[i].Task.ID, id, r1[i].Score)
		}
	}
}

func TestRankTieBreakerByID(t *testing.T) {
	e := NewEngine(fixedNow(), moscow)
	r := e.Rank([]Task{{ID: 10}, {ID: 2}, {ID: 7}})
	if r[0].Task.ID != 10 || r[1].Task.ID != 7 || r[2].Task.ID != 2 {
		t.Errorf("tie order = %d,%d,%d, want 10,7,2", r[0].Task.ID, r[1].Task.ID, r[2].Task.ID)
	}
}

func TestWhyTraceFormat(t *testing.T) {
	e := NewEngine(fixedNow(), moscow)
	s := e.Score(Task{ID: 1, DueDate: date(2026, 10, 4)})
	trace := WhyTrace(s)
	if trace == "" {
		t.Fatal("empty trace")
	}
	if trace != "overdue 10.00" {
		t.Errorf("trace = %q, want %q", trace, "overdue 10.00")
	}
}
