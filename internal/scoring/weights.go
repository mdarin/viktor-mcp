package scoring

type Weights struct {
	Overdue    float64 `json:"overdue,omitempty"`
	Priority   float64 `json:"priority,omitempty"`
	DueSoon    float64 `json:"due_soon,omitempty"`
	DoorCloses float64 `json:"door_closes,omitempty"`
	Deferred   float64 `json:"deferred,omitempty"`
	Waiting    float64 `json:"waiting,omitempty"`
	Effort     float64 `json:"effort,omitempty"`
	Stale      float64 `json:"stale,omitempty"`
	QuickWin   float64 `json:"quick_win,omitempty"`
	Stalled    float64 `json:"stalled,omitempty"`
}

func DefaultWeights() Weights {
	return Weights{
		Overdue:    10,
		Priority:   8,
		DueSoon:    6,
		DoorCloses: 6,
		Deferred:   4,
		Waiting:    3,
		Effort:     2,
		Stale:      2,
		QuickWin:   3,
		Stalled:    -6,
	}
}

func (w Weights) Merge(overrides Weights) Weights {
	out := w
	if overrides.Overdue != 0 {
		out.Overdue = overrides.Overdue
	}
	if overrides.Priority != 0 {
		out.Priority = overrides.Priority
	}
	if overrides.DueSoon != 0 {
		out.DueSoon = overrides.DueSoon
	}
	if overrides.DoorCloses != 0 {
		out.DoorCloses = overrides.DoorCloses
	}
	if overrides.Deferred != 0 {
		out.Deferred = overrides.Deferred
	}
	if overrides.Waiting != 0 {
		out.Waiting = overrides.Waiting
	}
	if overrides.Effort != 0 {
		out.Effort = overrides.Effort
	}
	if overrides.Stale != 0 {
		out.Stale = overrides.Stale
	}
	if overrides.QuickWin != 0 {
		out.QuickWin = overrides.QuickWin
	}
	if overrides.Stalled != 0 {
		out.Stalled = overrides.Stalled
	}
	return out
}
