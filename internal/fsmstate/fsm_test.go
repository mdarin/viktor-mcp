package fsmstate

import "testing"

func TestCanTransitionMatrix(t *testing.T) {
	allowed := [][2]State{
		{StateBacklog, StateInProgress}, {StateBacklog, StateBlocked},
		{StateInProgress, StateInReview}, {StateInProgress, StateStalled},
		{StateInProgress, StateBlocked}, {StateInProgress, StateBacklog},
		{StateInReview, StateDone}, {StateInReview, StateInProgress}, {StateInReview, StateStalled},
		{StateStalled, StateInProgress}, {StateStalled, StateBlocked},
		{StateBlocked, StateBacklog}, {StateBlocked, StateInProgress},
		{StateDone, StateBacklog},
	}
	for _, p := range allowed {
		if !CanTransition(p[0], p[1]) {
			t.Errorf("%s → %s must be allowed", p[0], p[1])
		}
	}
	forbidden := [][2]State{
		{StateBacklog, StateInReview}, {StateBacklog, StateDone}, {StateBacklog, StateStalled},
		{StateInProgress, StateDone},
		{StateInReview, StateBacklog}, {StateInReview, StateBlocked},
		{StateDone, StateInProgress}, {StateDone, StateInReview},
		{StateStalled, StateInReview}, {StateStalled, StateDone},
		{StateBlocked, StateInReview}, {StateBlocked, StateDone},
	}
	for _, p := range forbidden {
		if CanTransition(p[0], p[1]) {
			t.Errorf("%s → %s must be forbidden", p[0], p[1])
		}
	}
}

func TestParseState(t *testing.T) {
	for _, s := range []string{"backlog", "in_progress", "in_review", "done", "stalled", "blocked"} {
		if _, err := ParseState(s); err != nil {
			t.Errorf("ParseState(%q): %v", s, err)
		}
	}
	if _, err := ParseState("deployed"); err == nil {
		t.Error("unknown state must be rejected")
	}
}

func TestAllowedFrom(t *testing.T) {
	got := AllowedFrom(StateInReview)
	want := []State{StateDone, StateInProgress, StateStalled}
	if len(got) != len(want) {
		t.Fatalf("AllowedFrom(in_review) = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("AllowedFrom(in_review) = %v, want %v", got, want)
		}
	}
}
