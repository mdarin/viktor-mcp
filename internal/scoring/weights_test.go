package scoring

import "testing"

func TestDefaultWeightsComplete(t *testing.T) {
	w := DefaultWeights()
	for _, v := range []float64{w.Overdue, w.Priority, w.DueSoon, w.DoorCloses, w.Deferred, w.Waiting, w.Effort, w.Stale, w.QuickWin} {
		if v <= 0 {
			t.Error("default weight must be positive")
		}
	}
}

func TestMergeOverrides(t *testing.T) {
	base := DefaultWeights()
	merged := base.Merge(Weights{Overdue: 99, QuickWin: 0})
	if merged.Overdue != 99 {
		t.Errorf("overdue = %v, want override 99", merged.Overdue)
	}
	if merged.QuickWin != base.QuickWin {
		t.Errorf("quick_win = %v, zero override must keep base", merged.QuickWin)
	}
	if merged.Priority != base.Priority {
		t.Errorf("priority = %v, base lost", merged.Priority)
	}
}

func TestMergeNoSideEffects(t *testing.T) {
	base := DefaultWeights()
	_ = base.Merge(Weights{Overdue: 99})
	if base.Overdue == 99 {
		t.Error("merge mutated base")
	}
}
