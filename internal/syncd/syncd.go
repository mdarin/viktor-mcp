// Package syncd implements the periodic delta-sync daemon (card #880):
// one tick refreshes both the semantic memory projection (Qdrant) and
// the lexical history projection (ParadeDB) sequentially, and a full
// rescan of both runs on a slower cadence so soft-deleted tasks stop
// haunting the indexes (ghost cleanup). The daemon is best-effort by
// design: a failure of one index never cancels the other and never
// kills the loop — each tick reports per-index errors as fields.
package syncd

import (
	"context"
	"log"
	"time"

	"viktor/internal/memory"
)

// DefaultInterval is the delta tick cadence when -interval is omitted.
const DefaultInterval = 15 * time.Minute

// DefaultFullInterval is the full-rescan cadence when -full-interval is
// omitted (matches memory.FullSyncMaxAge's 24h default).
const DefaultFullInterval = 24 * time.Hour

// MemorySyncer is the memory-indexer slice the daemon drives: *memory.Indexer
// satisfies it; tests fake it.
type MemorySyncer interface {
	Sync(ctx context.Context) (*memory.Report, error)
	SyncFull(ctx context.Context) (*memory.Report, error)
}

// HistorySyncer is the history-sync slice the daemon drives. Full forces
// a full rescan (ghost-row cleanup); the report is the same JSON shape
// `viktor history sync` prints.
type HistorySyncer interface {
	Sync(ctx context.Context, full bool) (map[string]any, error)
}

// Daemon runs both syncers on one tick. A nil syncer (with its
// Unavailable reason set) means that index is permanently disabled —
// reported every tick as an error field, never fatal.
type Daemon struct {
	Memory  MemorySyncer
	History HistorySyncer
	// MemoryUnavailable / HistoryUnavailable explain a nil syncer.
	MemoryUnavailable  string
	HistoryUnavailable string
}

// TickReport is one syncd tick: the reports (or errors) of both indexes.
type TickReport struct {
	StartedAt    time.Time      `json:"tick_started"`
	Full         bool           `json:"full"`
	DurationMS   int64          `json:"duration_ms"`
	Memory       *memory.Report `json:"memory,omitempty"`
	MemoryError  string         `json:"memory_error,omitempty"`
	History      map[string]any `json:"history,omitempty"`
	HistoryError string         `json:"history_error,omitempty"`
}

// OK reports whether both indexes synced without errors this tick.
func (r *TickReport) OK() bool { return r.MemoryError == "" && r.HistoryError == "" }

// Tick runs one pass: memory first, then history (sequential — both hit
// the tracker, and history's delta window wants the memory cursor order
// anyway). full forces full rescans of both indexes.
func (d *Daemon) Tick(ctx context.Context, full bool) *TickReport {
	rep := &TickReport{StartedAt: time.Now().UTC(), Full: full}
	start := time.Now()
	if d.Memory != nil {
		var (
			r   *memory.Report
			err error
		)
		if full {
			r, err = d.Memory.SyncFull(ctx)
		} else {
			r, err = d.Memory.Sync(ctx)
		}
		if err != nil {
			rep.MemoryError = err.Error()
		} else {
			rep.Memory = r
		}
	} else if d.MemoryUnavailable != "" {
		rep.MemoryError = d.MemoryUnavailable
	}
	if d.History != nil {
		if r, err := d.History.Sync(ctx, full); err != nil {
			rep.HistoryError = err.Error()
		} else {
			rep.History = r
		}
	} else if d.HistoryUnavailable != "" {
		rep.HistoryError = d.HistoryUnavailable
	}
	rep.DurationMS = time.Since(start).Milliseconds()
	return rep
}

// nextFull decides the rescan mode of a tick: the daemon's first tick is
// a full rescan (cleans ghosts accumulated while it was down), then a
// full runs once per fullInterval.
func nextFull(lastFull time.Time, fullInterval time.Duration) bool {
	return lastFull.IsZero() || time.Since(lastFull) >= fullInterval
}

// Loop ticks the daemon until ctx is cancelled (graceful shutdown, the
// watchdog.Loop pattern, card #58): the first tick runs immediately and
// is a full rescan; every subsequent tick is a delta except when the
// full cadence comes due. Ticks never fail — per-index errors travel
// inside the report — so the loop only stops on ctx cancellation.
// onTick receives every report (the caller prints/logs it).
func Loop(ctx context.Context, d *Daemon, interval, fullInterval time.Duration, logger *log.Logger, onTick func(*TickReport)) error {
	if interval <= 0 {
		interval = DefaultInterval
	}
	if fullInterval <= 0 {
		fullInterval = DefaultFullInterval
	}
	var lastFull time.Time
	run := func() {
		full := nextFull(lastFull, fullInterval)
		rep := d.Tick(ctx, full)
		if full {
			lastFull = time.Now()
		}
		if onTick != nil {
			onTick(rep)
		}
		if logger != nil {
			logger.Printf("syncd tick: mode=%s ok=%t memory_ok=%t history_ok=%t",
				mode(full), rep.OK(), rep.MemoryError == "", rep.HistoryError == "")
		}
	}
	run()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			run()
		}
	}
}

func mode(full bool) string {
	if full {
		return "full"
	}
	return "delta"
}
