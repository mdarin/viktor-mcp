package scoring

import (
	"errors"
	"fmt"
	"time"
)

const ReasonDread = "dread"

var Reasons = []string{"dread", "waiting", "blocked", "too_big", "not_now"}

type DeferRequest struct {
	Reason string
	Until  time.Time
}

type DeferDecision struct {
	Allowed      bool
	NewCount     int
	Escalate     bool
	RequiresDate bool
	Message      string
}

var (
	ErrBareSnooze = errors.New("bare snooze refused: reason required")
	ErrBadReason  = errors.New("unknown defer reason")
	ErrDateNeeded = errors.New("dread defer requires a date")
	ErrDoorPassed = errors.New("deferral past door_closes rejected outright")
)

func (e Engine) EvaluateDefer(t Task, r DeferRequest) (DeferDecision, error) {
	if r.Reason == "" {
		return DeferDecision{Allowed: false}, ErrBareSnooze
	}
	known := false
	for _, reason := range Reasons {
		if r.Reason == reason {
			known = true
			break
		}
	}
	if !known {
		return DeferDecision{Allowed: false, Message: fmt.Sprintf("reason %q not in taxonomy %v", r.Reason, Reasons)}, ErrBadReason
	}

	dec := DeferDecision{Allowed: true, NewCount: t.DeferredCount + 1, Message: "deferred"}

	if r.Reason == ReasonDread {
		dec.RequiresDate = true
		if r.Until.IsZero() {
			return DeferDecision{Allowed: false, RequiresDate: true}, ErrDateNeeded
		}
		if t.DoorCloses != nil && !t.DoorCloses.IsZero() && r.Until.After(*t.DoorCloses) {
			return DeferDecision{Allowed: false, RequiresDate: true, Message: fmt.Sprintf("until %s is past door_closes %s", r.Until.Format(time.RFC3339), t.DoorCloses.Format(time.RFC3339))}, ErrDoorPassed
		}
		newDread := t.DeferredDread + 1
		if newDread >= 3 {
			dec.Escalate = true
			dec.Message = "rule_of_three raised: third dread defer"
		}
	}

	return dec, nil
}
