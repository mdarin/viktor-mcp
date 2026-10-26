package syncd

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"

	"viktor/internal/memory"
)

// fakeMemory records the calls and can fail on demand.
type fakeMemory struct {
	syncs, fulls int
	err          error
}

func (f *fakeMemory) Sync(context.Context) (*memory.Report, error) {
	f.syncs++
	return &memory.Report{TasksScanned: f.syncs}, f.err
}

func (f *fakeMemory) SyncFull(context.Context) (*memory.Report, error) {
	f.fulls++
	return &memory.Report{TasksScanned: f.fulls}, f.err
}

// fakeHistory likewise.
type fakeHistory struct {
	syncs, fulls int
	err          error
}

func (f *fakeHistory) Sync(_ context.Context, full bool) (map[string]any, error) {
	if full {
		f.fulls++
	} else {
		f.syncs++
	}
	return map[string]any{"tasks": f.syncs + f.fulls}, f.err
}

func TestTickReportsBothIndexesIndependently(t *testing.T) {
	mem := &fakeMemory{err: errors.New("ollama down")}
	hist := &fakeHistory{}
	d := &Daemon{Memory: mem, History: hist}
	rep := d.Tick(context.Background(), false)
	if rep.MemoryError == "" {
		t.Fatal("memory error not reported")
	}
	if rep.Memory != nil {
		t.Fatal("failed memory sync must not contribute a report")
	}
	if rep.History == nil || rep.History["tasks"] != 1 {
		t.Fatalf("history must succeed despite memory failure: %+v", rep.History)
	}
	if rep.HistoryError != "" {
		t.Fatalf("unexpected history error: %q", rep.HistoryError)
	}
	if rep.OK() {
		t.Fatal("OK must be false when an index failed")
	}
}

func TestTickOtherDirectionIndependent(t *testing.T) {
	mem := &fakeMemory{}
	hist := &fakeHistory{err: errors.New("pg down")}
	d := &Daemon{Memory: mem, History: hist}
	rep := d.Tick(context.Background(), false)
	if rep.Memory == nil || rep.MemoryError != "" {
		t.Fatalf("memory must succeed: %+v", rep)
	}
	if rep.HistoryError == "" {
		t.Fatal("history error not reported")
	}
}

func TestTickNilSyncerReportedNotFatal(t *testing.T) {
	d := &Daemon{MemoryUnavailable: "memory not configured", History: &fakeHistory{}}
	rep := d.Tick(context.Background(), false)
	if rep.MemoryError != "memory not configured" {
		t.Fatalf("disabled memory must surface its reason: %q", rep.MemoryError)
	}
	if rep.History == nil {
		t.Fatal("history must still run")
	}
	if rep.OK() {
		t.Fatal("OK must be false: disabled index is an error line")
	}
}

func TestTickModeSelectsSyncVariant(t *testing.T) {
	mem := &fakeMemory{}
	d := &Daemon{Memory: mem}
	d.Tick(context.Background(), false)
	d.Tick(context.Background(), true)
	if mem.syncs != 1 || mem.fulls != 1 {
		t.Fatalf("delta/full routing broken: syncs=%d fulls=%d", mem.syncs, mem.fulls)
	}
}

func TestNextFullRhythm(t *testing.T) {
	if !nextFull(time.Time{}, time.Hour) {
		t.Fatal("first tick must be a full rescan")
	}
	now := time.Now()
	if nextFull(now.Add(-30*time.Minute), time.Hour) {
		t.Fatal("30m after a full must stay delta")
	}
	if !nextFull(now.Add(-90*time.Minute), time.Hour) {
		t.Fatal("90m after a full must rescan")
	}
}

func TestLoopRunsFullFirstThenDeltas(t *testing.T) {
	mem := &fakeMemory{}
	d := &Daemon{Memory: mem, History: &fakeHistory{}}
	var reports []*TickReport
	ctx, cancel := context.WithCancel(context.Background())
	logger := log.New(&bytes.Buffer{}, "", 0)
	go func() {
		time.Sleep(120 * time.Millisecond)
		cancel()
	}()
	// 40ms ticks, 1h full cadence: expect full + >=2 deltas.
	if err := Loop(ctx, d, 40*time.Millisecond, time.Hour, logger, func(r *TickReport) {
		reports = append(reports, r)
	}); err != nil {
		t.Fatalf("loop: %v", err)
	}
	if len(reports) < 3 {
		t.Fatalf("expected >=3 ticks, got %d", len(reports))
	}
	if !reports[0].Full {
		t.Fatal("first tick must be full")
	}
	for _, r := range reports[1:] {
		if r.Full {
			t.Fatal("subsequent ticks within the cadence must be delta")
		}
	}
}

func TestLoopFullCadenceRepeats(t *testing.T) {
	mem := &fakeMemory{}
	d := &Daemon{Memory: mem, History: &fakeHistory{}}
	var reports []*TickReport
	ctx, cancel := context.WithCancel(context.Background())
	logger := log.New(&bytes.Buffer{}, "", 0)
	go func() {
		time.Sleep(250 * time.Millisecond)
		cancel()
	}()
	// 40ms ticks, 100ms full cadence: ticks 0, ~2, ~5 … are full.
	if err := Loop(ctx, d, 40*time.Millisecond, 100*time.Millisecond, logger, func(r *TickReport) {
		reports = append(reports, r)
	}); err != nil {
		t.Fatalf("loop: %v", err)
	}
	fulls := 0
	for _, r := range reports {
		if r.Full {
			fulls++
		}
	}
	if fulls < 2 {
		t.Fatalf("expected >=2 full rescans in %d ticks, got %d", len(reports), fulls)
	}
}

func TestAcquireLockExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "syncd.lock")
	f1, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer func() { _ = f1.Close() }()
	if _, err := AcquireLock(path); err == nil {
		t.Fatal("second concurrent acquire must fail")
	}
	// Releasing lets the next holder in.
	if err := f1.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	f2, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("re-acquire after release: %v", err)
	}
	_ = f2.Close()
}

func TestAcquireLockRequiresExistingDir(t *testing.T) {
	// OpenFile does not create parent dirs; the caller (main) MkdirAll's
	// the state path first. This asserts the failure contract.
	missing := filepath.Join(t.TempDir(), "nested", "lock")
	if _, err := AcquireLock(missing); err == nil {
		t.Fatal("lock in a missing dir must fail (caller MkdirAll's first)")
	}
	dir := filepath.Join(t.TempDir(), "syncd")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := AcquireLock(filepath.Join(dir, "lock"))
	if err != nil {
		t.Fatalf("acquire after MkdirAll: %v", err)
	}
	_ = f.Close()
}
