package scoring

import (
	"testing"
	"time"
)

func TestParseGoalsAbsent(t *testing.T) {
	g, unknown := ParseGoals("plain description\n<!-- viktor:deferred=1 dread=0 -->")
	if g != nil {
		t.Fatalf("expected nil goals for description without marker, got %+v", g)
	}
	if len(unknown) != 0 {
		t.Fatalf("expected no unknown axes, got %v", unknown)
	}
}

func TestParseGoalsFull(t *testing.T) {
	g, unknown := ParseGoals("desc\n<!-- viktor:goals speed=0.9 quality=0.3 safety=1 scope=0 -->")
	if g == nil {
		t.Fatal("expected goals parsed")
	}
	want := Goals{Speed: 0.9, Quality: 0.3, Safety: 1, Scope: 0}
	if *g != want {
		t.Fatalf("got %+v, want %+v", *g, want)
	}
	if len(unknown) != 0 {
		t.Fatalf("expected no unknown axes, got %v", unknown)
	}
}

func TestParseGoalsPartialDefaultsNeutral(t *testing.T) {
	g, _ := ParseGoals("<!-- viktor:goals speed=0.8 -->")
	if g == nil {
		t.Fatal("expected goals parsed")
	}
	want := Goals{Speed: 0.8, Quality: 0.5, Safety: 0.5, Scope: 0.5}
	if *g != want {
		t.Fatalf("got %+v, want %+v", *g, want)
	}
}

func TestParseGoalsClampAndMalformed(t *testing.T) {
	g, _ := ParseGoals("<!-- viktor:goals speed=1.7 quality=-3 safety=abc -->")
	if g == nil {
		t.Fatal("expected goals parsed")
	}
	want := Goals{Speed: 1, Quality: 0, Safety: 0.5, Scope: 0.5}
	if *g != want {
		t.Fatalf("got %+v, want %+v", *g, want)
	}
}

func TestParseGoalsUnknownAxes(t *testing.T) {
	g, unknown := ParseGoals("<!-- viktor:goals speed=0.9 style=high polish=2 -->")
	if g == nil {
		t.Fatal("expected goals parsed")
	}
	if g.Speed != 0.9 {
		t.Fatalf("known axis lost: %+v", g)
	}
	if len(unknown) != 2 || unknown[0] != "style" || unknown[1] != "polish" {
		t.Fatalf("expected [style polish], got %v", unknown)
	}
}

func TestGoalsContributionNeutralIsZero(t *testing.T) {
	g := Goals{Speed: 0.5, Quality: 0.5, Safety: 0.5, Scope: 0.5}
	if got := g.Contribution(); got != 0 {
		t.Fatalf("neutral vector must contribute 0, got %v", got)
	}
}

func TestGoalsContributionExtremes(t *testing.T) {
	neutral := func(mod func(*Goals)) Goals {
		g := Goals{Speed: 0.5, Quality: 0.5, Safety: 0.5, Scope: 0.5}
		mod(&g)
		return g
	}
	cases := []struct {
		g    Goals
		want float64
	}{
		{neutral(func(g *Goals) { g.Speed = 1 }), 3},   // (1-0.5)*2*3
		{neutral(func(g *Goals) { g.Speed = 0 }), -3},  // (0-0.5)*2*3
		{neutral(func(g *Goals) { g.Quality = 1 }), 4}, // (1-0.5)*2*4
		{neutral(func(g *Goals) { g.Quality = 0 }), -4},
		{neutral(func(g *Goals) { g.Safety = 1 }), 3},
		{neutral(func(g *Goals) { g.Safety = 0 }), -3},
		{neutral(func(g *Goals) { g.Scope = 1 }), -2}, // negative weight: broad scope pushes down
		{neutral(func(g *Goals) { g.Scope = 0 }), 2},
	}
	for _, c := range cases {
		if got := c.g.Contribution(); got != c.want {
			t.Fatalf("%+v: got %v, want %v", c.g, got, c.want)
		}
	}
}

func TestGoalsDamped(t *testing.T) {
	g := Goals{Speed: 1}
	d := g.Damped(GoalSpeed, 0.5)
	if d.Speed != 0.75 {
		t.Fatalf("damped speed: got %v, want 0.75", d.Speed)
	}
	if g.Speed != 1 {
		t.Fatalf("Damped must not mutate receiver, got %v", g.Speed)
	}
	// factor out of range clamps
	if full := g.Damped(GoalSpeed, 2); full.Speed != 1 {
		t.Fatalf("factor>1: got %v, want 1", full.Speed)
	}
	if zero := g.Damped(GoalSpeed, -1); zero.Speed != 0.5 {
		t.Fatalf("factor<0: got %v, want 0.5", zero.Speed)
	}
}

func TestEngineGoalsTermFiresOnlyWithMarker(t *testing.T) {
	moscow, _ := time.LoadLocation("Europe/Moscow")
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, moscow)
	e := NewEngine(now, moscow)

	legacy := e.Score(Task{ID: 1})
	for _, term := range legacy.Why {
		if term.Name == "goals" {
			t.Fatalf("goals term must not fire without marker: %v", legacy.Why)
		}
	}

	neutral := Goals{Speed: 0.5, Quality: 0.5, Safety: 0.5, Scope: 0.5}
	g := Goals{Speed: 1, Quality: 0.5, Safety: 0.5, Scope: 0.5}
	if got := g.Contribution() - neutral.Contribution(); got != 3 {
		t.Fatalf("full-speed vs neutral: got %v, want 3", got)
	}
	scored := e.Score(Task{ID: 2, Goals: &g})
	if got := scored.Score - legacy.Score; got != 3 {
		t.Fatalf("goals contribution: got %v, want 3", got)
	}
	if term := findTerm(scored.Why, "goals"); term == nil || term.Value != 3 {
		t.Fatalf("goals term missing or wrong: %v", scored.Why)
	}
}

func TestEngineSpeedDampModulates(t *testing.T) {
	moscow, _ := time.LoadLocation("Europe/Moscow")
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, moscow)
	e := NewEngine(now, moscow)
	e.SpeedDamp = 0.5

	g := Goals{Speed: 1, Quality: 0.5, Safety: 0.5, Scope: 0.5}
	scored := e.Score(Task{ID: 1, Goals: &g})
	// damped speed 0.75 -> (0.75-0.5)*2*3 = 1.5, other axes neutral
	if term := findTerm(scored.Why, "goals"); term == nil || term.Value != 1.5 {
		t.Fatalf("damped goals term: got %v, want 1.5", scored.Why)
	}
}

func findTerm(terms []Term, name string) *Term {
	for i := range terms {
		if terms[i].Name == name {
			return &terms[i]
		}
	}
	return nil
}
