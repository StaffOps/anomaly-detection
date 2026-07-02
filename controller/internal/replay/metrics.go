package replay

import (
	"runtime"
	"sort"
	"sync"
	"time"
)

// ExecutionMetrics collects replay run statistics. Embedded in JSON output
// under metadata.execution_metrics. No Prometheus registry (V1).
type ExecutionMetrics struct {
	TicksProcessed         int     `json:"ticks_processed"`
	TicksSkippedQueryError int     `json:"ticks_skipped_query_error"`
	VMQueriesTotal         int     `json:"vm_queries_total"`
	VMQueryDurationP95     float64 `json:"vm_query_duration_seconds_p95"`
	LokiQueriesTotal       int     `json:"loki_queries_total"`
	MemoryPeakMB           float64 `json:"memory_peak_mb"`
	DurationSeconds        float64 `json:"duration_seconds"`
}

// metricsCollector accumulates execution metrics during a replay run.
type metricsCollector struct {
	mu             sync.Mutex
	ticksProcessed int
	ticksSkipped   int
	vmQueries      int
	lokiQueries    int
	memPeakBytes   uint64
	vmDurations    *p95Window
	start          time.Time
}

func newMetricsCollector() *metricsCollector {
	return &metricsCollector{
		vmDurations: newP95Window(100),
		start:       time.Now(),
	}
}

func (m *metricsCollector) recordTick() { m.mu.Lock(); m.ticksProcessed++; m.mu.Unlock() }
func (m *metricsCollector) recordSkip() { m.mu.Lock(); m.ticksSkipped++; m.mu.Unlock() }
func (m *metricsCollector) recordVMQuery(d time.Duration) {
	m.mu.Lock()
	m.vmQueries++
	m.vmDurations.add(d.Seconds())
	m.mu.Unlock()
}
func (m *metricsCollector) recordLokiQuery() { m.mu.Lock(); m.lokiQueries++; m.mu.Unlock() }

func (m *metricsCollector) sampleMemory() {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	m.mu.Lock()
	if ms.Alloc > m.memPeakBytes {
		m.memPeakBytes = ms.Alloc
	}
	m.mu.Unlock()
}

func (m *metricsCollector) snapshot() ExecutionMetrics {
	m.mu.Lock()
	defer m.mu.Unlock()
	return ExecutionMetrics{
		TicksProcessed:         m.ticksProcessed,
		TicksSkippedQueryError: m.ticksSkipped,
		VMQueriesTotal:         m.vmQueries,
		VMQueryDurationP95:     m.vmDurations.p95(),
		LokiQueriesTotal:       m.lokiQueries,
		MemoryPeakMB:           float64(m.memPeakBytes) / (1024 * 1024),
		DurationSeconds:        time.Since(m.start).Seconds(),
	}
}

// p95Window is a simple sliding window percentile calculator over the last N values.
type p95Window struct {
	buf  []float64
	pos  int
	full bool
}

func newP95Window(size int) *p95Window {
	return &p95Window{buf: make([]float64, size)}
}

func (w *p95Window) add(v float64) {
	w.buf[w.pos] = v
	w.pos++
	if w.pos >= len(w.buf) {
		w.pos = 0
		w.full = true
	}
}

func (w *p95Window) p95() float64 {
	n := len(w.buf)
	if !w.full {
		n = w.pos
	}
	if n == 0 {
		return 0
	}
	sorted := make([]float64, n)
	if w.full {
		copy(sorted, w.buf)
	} else {
		copy(sorted, w.buf[:n])
	}
	sort.Float64s(sorted)
	idx := int(float64(n-1) * 0.95)
	return sorted[idx]
}
