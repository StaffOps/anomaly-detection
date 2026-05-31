package replay

import (
	"testing"
	"time"
)

func TestP95Window(t *testing.T) {
	w := newP95Window(100)

	// Empty window returns 0.
	if got := w.p95(); got != 0 {
		t.Errorf("empty p95 = %f, want 0", got)
	}

	// Add 100 values: 1..100. P95 should be ~95.
	for i := 1; i <= 100; i++ {
		w.add(float64(i))
	}
	p := w.p95()
	if p < 94 || p > 96 {
		t.Errorf("p95 of 1..100 = %f, want ~95", p)
	}

	// Overflow: add 100 more values (101..200). P95 should be ~195.
	for i := 101; i <= 200; i++ {
		w.add(float64(i))
	}
	p = w.p95()
	if p < 194 || p > 196 {
		t.Errorf("p95 of 101..200 = %f, want ~195", p)
	}
}

func TestP95Window_Small(t *testing.T) {
	w := newP95Window(100)
	w.add(1.0)
	w.add(2.0)
	w.add(3.0)
	// With 3 elements, p95 index = int(2 * 0.95) = 1 → value 2.0 (sorted: 1,2,3)
	p := w.p95()
	if p != 2.0 {
		t.Errorf("p95 of [1,2,3] = %f, want 2.0", p)
	}
}

func TestMetricsCollector(t *testing.T) {
	mc := newMetricsCollector()
	mc.recordTick()
	mc.recordTick()
	mc.recordSkip()
	mc.recordVMQuery(100 * time.Millisecond)
	mc.recordVMQuery(200 * time.Millisecond)
	mc.recordLokiQuery()
	mc.sampleMemory()

	snap := mc.snapshot()
	if snap.TicksProcessed != 2 {
		t.Errorf("ticks = %d, want 2", snap.TicksProcessed)
	}
	if snap.TicksSkippedQueryError != 1 {
		t.Errorf("skipped = %d, want 1", snap.TicksSkippedQueryError)
	}
	if snap.VMQueriesTotal != 2 {
		t.Errorf("vm queries = %d, want 2", snap.VMQueriesTotal)
	}
	if snap.LokiQueriesTotal != 1 {
		t.Errorf("loki queries = %d, want 1", snap.LokiQueriesTotal)
	}
	if snap.MemoryPeakMB <= 0 {
		t.Error("memory peak should be > 0")
	}
	if snap.DurationSeconds <= 0 {
		t.Error("duration should be > 0")
	}
}
