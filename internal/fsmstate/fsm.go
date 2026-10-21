// Package fsmstate implements the task lifecycle state machine and its
// persistent store (ADR 0004): statuses are tracked in a local SQLite
// database, Vikunja stays the source of truth via the API.
package fsmstate

import (
	"fmt"
	"slices"
)

// State is a task lifecycle state.
type State string

const (
	StateBacklog    State = "backlog"
	StateInProgress State = "in_progress"
	StateInReview   State = "in_review"
	StateDone       State = "done"
	StateStalled    State = "stalled"
	StateBlocked    State = "blocked"
)

// transitions is the allowed-transition matrix. A transition absent from
// the target list is rejected before any API call is made, so an agent
// physically cannot move a task into a forbidden state.
var transitions = map[State][]State{
	StateBacklog:    {StateInProgress, StateBlocked},
	StateInProgress: {StateInReview, StateStalled, StateBlocked, StateBacklog},
	StateInReview:   {StateDone, StateInProgress, StateStalled},
	StateStalled:    {StateInProgress, StateBlocked},
	StateBlocked:    {StateBacklog, StateInProgress},
	StateDone:       {StateBacklog},
}

// ParseState validates a raw state string.
func ParseState(s string) (State, error) {
	st := State(s)
	if _, ok := transitions[st]; !ok {
		return "", fmt.Errorf("unknown state %q", s)
	}
	return st, nil
}

// CanTransition reports whether from → to is allowed.
func CanTransition(from, to State) bool {
	return slices.Contains(transitions[from], to)
}

// AllowedFrom lists the states reachable from s.
func AllowedFrom(s State) []State {
	out := slices.Clone(transitions[s])
	slices.Sort(out)
	return out
}
