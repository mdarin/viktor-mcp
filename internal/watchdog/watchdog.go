// Package watchdog implements the stalled-task sentinel (card #58): it
// scans FSM-tracked in_progress claims whose tracker task has seen no
// activity (updated_at) for longer than the configured threshold, moves
// them to the stalled state with an audit trail, and resumes them on
// request. Vikunja stays the source of truth; the FSM store only tracks
// lifecycle state (ADR 0004).
package watchdog

import (
	"context"
	"fmt"
	"log"
	"time"

	"viktor/internal/client"
	"viktor/internal/fsmstate"
)

// TaskFetcher is the slice of the tracker client the watchdog needs.
type TaskFetcher interface {
	GetTask(ctx context.Context, taskID int, out any) error
}

// Stalled describes one task the watchdog moved to stalled.
type Stalled struct {
	TaskID    int           `json:"task_id"`
	Actor     string        `json:"actor,omitempty"`
	Idle      time.Duration `json:"idle"`
	UpdatedAt string        `json:"updated_at"`
}

// Skipped is a claim the scan could not evaluate (tracker unreachable,
// task deleted) — the watchdog degrades gracefully instead of failing.
type Skipped struct {
	TaskID int    `json:"task_id"`
	Reason string `json:"reason"`
}

// Result is one scan pass.
type Result struct {
	Instance string    `json:"instance"`
	Scanned  int       `json:"scanned"`
	Stalled  []Stalled `json:"stalled"`
	Skipped  []Skipped `json:"skipped"`
}

// Watchdog stalls idle in_progress tasks.
type Watchdog struct {
	Store     *fsmstate.Store
	Instance  string
	Tasks     TaskFetcher
	Threshold time.Duration
	// Notify is called for every task moved to stalled (operator hook).
	Notify func(Stalled)
	// Now is overridable in tests.
	Now func() time.Time
}

// DefaultThreshold is how long an in_progress task may sit without
// tracker activity before the watchdog stalls it.
const DefaultThreshold = 24 * time.Hour

// New builds a watchdog for an instance with the default threshold.
func New(store *fsmstate.Store, instance string, tasks TaskFetcher) *Watchdog {
	return &Watchdog{Store: store, Instance: instance, Tasks: tasks, Threshold: DefaultThreshold, Now: time.Now}
}

// Scan runs one pass: every FSM-tracked in_progress task whose tracker
// updated_at is older than Threshold transitions to stalled. The audit
// trail (who/when) lands in the transitions table with actor "watchdog".
func (w *Watchdog) Scan(ctx context.Context) (Result, error) {
	res := Result{Instance: w.Instance}
	claims, err := w.Store.ListByState(ctx, w.Instance, fsmstate.StateInProgress)
	if err != nil {
		return res, err
	}
	now := w.Now()
	for _, c := range claims {
		res.Scanned++
		var task map[string]any
		if err := w.Tasks.GetTask(ctx, c.TaskID, &task); err != nil {
			res.Skipped = append(res.Skipped, Skipped{TaskID: c.TaskID, Reason: err.Error()})
			continue
		}
		if done, _ := task["done"].(bool); done {
			continue
		}
		updated := parseTime(task["updated"])
		if updated.IsZero() {
			res.Skipped = append(res.Skipped, Skipped{TaskID: c.TaskID, Reason: "task has no updated_at"})
			continue
		}
		idle := now.Sub(updated)
		if idle < w.Threshold {
			continue
		}
		detail := fmt.Sprintf("watchdog: idle %s > threshold %s", idle.Round(time.Second), w.Threshold)
		if _, err := w.Store.Transition(ctx, w.Instance, c.TaskID, fsmstate.StateStalled, "", "watchdog", detail); err != nil {
			return res, fmt.Errorf("watchdog: stall task %d: %w", c.TaskID, err)
		}
		st := Stalled{TaskID: c.TaskID, Actor: c.Actor, Idle: idle.Round(time.Second), UpdatedAt: updated.UTC().Format(time.RFC3339)}
		res.Stalled = append(res.Stalled, st)
		if w.Notify != nil {
			w.Notify(st)
		}
	}
	return res, nil
}

// Resume returns a stalled task to in_progress with an audit entry naming
// the actor and the moment of the return (card #58: «кто/когда вернул»).
func (w *Watchdog) Resume(ctx context.Context, taskID int, actor string) (fsmstate.Claim, error) {
	detail := "watchdog: resume"
	if actor != "" {
		detail += " by " + actor
	}
	detail += " at " + w.Now().UTC().Format(time.RFC3339)
	return w.Store.Transition(ctx, w.Instance, taskID, fsmstate.StateInProgress, "", actor, detail)
}

// Loop runs Scan on a ticker until ctx is cancelled; every pass result is
// logged so operator-side visibility matches the card's AC. Use Interval
// <= 0 for a single shot.
func Loop(ctx context.Context, w *Watchdog, interval time.Duration, logger *log.Logger) error {
	run := func() error {
		res, err := w.Scan(ctx)
		if err != nil {
			// Graceful degradation: a tracker outage must not kill the
			// loop; log and keep ticking.
			logger.Printf("watchdog scan failed: %v", err)
			return err
		}
		if len(res.Stalled) > 0 {
			for _, st := range res.Stalled {
				logger.Printf("watchdog: task %d stalled (idle %s, actor %q) — operator attention needed", st.TaskID, st.Idle, st.Actor)
			}
		}
		return nil
	}
	if interval <= 0 {
		return run()
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	if err := run(); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			_ = run()
		}
	}
}

// compile-time check that *client.Client satisfies TaskFetcher.
var _ TaskFetcher = (*client.Client)(nil)

func parseTime(v any) time.Time {
	s, _ := v.(string)
	if s == "" || s == "0001-01-01T00:00:00Z" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}
