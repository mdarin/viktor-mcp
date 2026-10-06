package scoring

import "time"

type Task struct {
	ID            int
	Priority      int
	DueDate       *time.Time
	DoorCloses    *time.Time
	WaitingSince  *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
	EffortMinutes int
	DeferredCount int
	DeferredDread int
}

type Caps struct {
	OverdueDays   int
	DoorDays      int
	DeferredCount int
}

const (
	defaultOverdueCap  = 7
	defaultDoorCap     = 3
	defaultDeferredCap = 3
	dueSoonHorizonDays = 3
	staleAfterDays     = 14
	quickWinMaxMinutes = 30
	workdayMinutes     = 480
)

func DefaultCaps() Caps {
	return Caps{
		OverdueDays:   defaultOverdueCap,
		DoorDays:      defaultDoorCap,
		DeferredCount: defaultDeferredCap,
	}
}

type Term struct {
	Name  string
	Value float64
}

type Scored struct {
	Task  Task
	Score float64
	Why   []Term
}

type Engine struct {
	Weights  Weights
	Caps     Caps
	Location *time.Location
	Now      time.Time
}

func NewEngine(now time.Time, loc *time.Location) Engine {
	return Engine{
		Weights:  DefaultWeights(),
		Caps:     DefaultCaps(),
		Location: loc,
		Now:      now,
	}
}

func (e Engine) Score(t Task) Scored {
	now := e.Now.In(e.Location)
	w := e.Weights
	var terms []Term

	if t.DueDate != nil && !t.DueDate.IsZero() {
		overdueDays := -daysUntil(e.Location, now, *t.DueDate)
		if overdueDays > 0 {
			units := minInt(overdueDays, e.Caps.OverdueDays)
			terms = append(terms, Term{"overdue", float64(units) * w.Overdue})
		} else if overdueDays > -dueSoonHorizonDays {
			terms = append(terms, Term{"due_soon", float64(dueSoonHorizonDays+overdueDays) * w.DueSoon})
		}
	}

	if t.Priority > 0 {
		terms = append(terms, Term{"priority", float64(t.Priority) * w.Priority})
	}

	if t.DoorCloses != nil && !t.DoorCloses.IsZero() {
		untilDoor := daysUntil(e.Location, now, *t.DoorCloses)
		units := e.Caps.DoorDays
		if untilDoor > 0 {
			units = e.Caps.DoorDays - minInt(untilDoor, e.Caps.DoorDays)
		}
		terms = append(terms, Term{"door_closes", float64(units) * w.DoorCloses})
	}

	if t.DeferredCount > 0 {
		units := minInt(t.DeferredCount, e.Caps.DeferredCount)
		terms = append(terms, Term{"deferred", float64(units) * w.Deferred})
	}

	if t.WaitingSince != nil && !t.WaitingSince.IsZero() {
		days := -daysUntil(e.Location, now, *t.WaitingSince)
		if days > 0 {
			terms = append(terms, Term{"waiting", float64(days) * w.Waiting})
		}
	}

	if t.EffortMinutes > 0 {
		if t.EffortMinutes <= quickWinMaxMinutes && t.Priority >= 3 {
			terms = append(terms, Term{"quick_win", w.QuickWin})
		}
		if saved := workdayMinutes - t.EffortMinutes; saved > 0 {
			terms = append(terms, Term{"effort", float64(saved) / workdayMinutes * w.Effort})
		}
	}

	if !t.UpdatedAt.IsZero() {
		staleDays := -daysUntil(e.Location, now, t.UpdatedAt)
		if staleDays > staleAfterDays {
			weeks := float64(staleDays-staleAfterDays) / 7.0
			terms = append(terms, Term{"stale", weeks * w.Stale})
		}
	}

	score := 0.0
	for _, term := range terms {
		score += term.Value
	}
	return Scored{Task: t, Score: score, Why: terms}
}

func (e Engine) Rank(tasks []Task) []Scored {
	scores := make([]Scored, len(tasks))
	for i, t := range tasks {
		scores[i] = e.Score(t)
	}
	stableSortByScore(scores)
	return scores
}

func stableSortByScore(s []Scored) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0; j-- {
			a, b := s[j-1], s[j]
			less := a.Score < b.Score ||
				(a.Score == b.Score && a.Task.ID < b.Task.ID)
			if !less {
				break
			}
			s[j-1], s[j] = b, a
		}
	}
}

func daysUntil(loc *time.Location, from, to time.Time) int {
	return int(midnight(loc, to).Sub(midnight(loc, from)).Hours() / 24)
}

func midnight(loc *time.Location, t time.Time) time.Time {
	u := t.In(loc)
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, loc)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
