package absence

import (
	"testing"
	"time"
)

func TestNewTracker_Defaults(t *testing.T) {
	tr := NewTracker(Config{Enabled: true})

	if tr.cfg.Threshold != 5*time.Minute {
		t.Errorf("Threshold = %v, want 5m", tr.cfg.Threshold)
	}
	if tr.cfg.CheckInterval != 30*time.Second {
		t.Errorf("CheckInterval = %v, want 30s", tr.cfg.CheckInterval)
	}
	if tr.cfg.StartupGrace != 10*time.Minute {
		t.Errorf("StartupGrace = %v, want 10m (2×Threshold)", tr.cfg.StartupGrace)
	}
}

func TestNewTracker_CustomValues(t *testing.T) {
	cfg := Config{
		Enabled:       true,
		Threshold:     2 * time.Minute,
		CheckInterval: 10 * time.Second,
		StartupGrace:  1 * time.Minute,
	}
	tr := NewTracker(cfg)

	if tr.cfg.Threshold != 2*time.Minute {
		t.Errorf("Threshold = %v, want 2m", tr.cfg.Threshold)
	}
	if tr.cfg.CheckInterval != 10*time.Second {
		t.Errorf("CheckInterval = %v, want 10s", tr.cfg.CheckInterval)
	}
	if tr.cfg.StartupGrace != 1*time.Minute {
		t.Errorf("StartupGrace = %v, want 1m", tr.cfg.StartupGrace)
	}
}

func TestRecordSample(t *testing.T) {
	tr := NewTracker(Config{Enabled: true})
	labels := map[string]string{"namespace": "default", "pod": "web-1"}

	tr.RecordSample("http_requests", labels)

	if tr.SeriesCount() != 1 {
		t.Fatalf("SeriesCount = %d, want 1", tr.SeriesCount())
	}

	// Same series again — no duplicate.
	tr.RecordSample("http_requests", labels)
	if tr.SeriesCount() != 1 {
		t.Fatalf("SeriesCount = %d after re-record, want 1", tr.SeriesCount())
	}

	// Different series increments count.
	tr.RecordSample("http_requests", map[string]string{"namespace": "other"})
	if tr.SeriesCount() != 2 {
		t.Fatalf("SeriesCount = %d, want 2", tr.SeriesCount())
	}
}

func TestRecordSample_UpdatesLastSeen(t *testing.T) {
	tr := NewTracker(Config{Enabled: true})
	labels := map[string]string{"namespace": "default"}

	tr.RecordSample("m1", labels)
	key := seriesKey("m1", labels)
	first := tr.series[key].lastSeen

	// Brief pause to ensure time advances.
	time.Sleep(1 * time.Millisecond)
	tr.RecordSample("m1", labels)
	second := tr.series[key].lastSeen

	if !second.After(first) {
		t.Errorf("lastSeen not updated: first=%v, second=%v", first, second)
	}
}

func TestCheck_Disabled(t *testing.T) {
	tr := newTestTracker(Config{Enabled: false, Threshold: time.Millisecond})
	// Insert a silent series.
	tr.series["m1:map[]"] = &seriesState{
		metric:   "m1",
		labels:   map[string]string{},
		lastSeen: time.Now().Add(-time.Hour),
	}

	alerts := tr.Check()
	if alerts != nil {
		t.Errorf("disabled tracker returned %d alerts, want nil", len(alerts))
	}
}

func TestCheck_StartupGrace(t *testing.T) {
	tr := NewTracker(Config{Enabled: true, Threshold: time.Millisecond, StartupGrace: time.Hour})
	// Insert a silent series.
	key := seriesKey("m1", nil)
	tr.series[key] = &seriesState{
		metric:   "m1",
		labels:   map[string]string{},
		lastSeen: time.Now().Add(-time.Hour),
	}

	alerts := tr.Check()
	if alerts != nil {
		t.Errorf("during grace period got %d alerts, want nil", len(alerts))
	}
}

func TestCheck_RecentSeries(t *testing.T) {
	tr := newTestTracker(Config{Enabled: true, Threshold: 5 * time.Minute})
	tr.series[seriesKey("m1", nil)] = &seriesState{
		metric:   "m1",
		labels:   map[string]string{},
		lastSeen: time.Now(), // just seen
	}

	alerts := tr.Check()
	if len(alerts) != 0 {
		t.Errorf("got %d alerts for recent series, want 0", len(alerts))
	}
}

func TestCheck_SilentSeries(t *testing.T) {
	tr := newTestTracker(Config{Enabled: true, Threshold: time.Minute})
	labels := map[string]string{"namespace": "production", "pod": "api-1"}
	lastSeen := time.Now().Add(-2 * time.Minute)
	tr.series[seriesKey("cpu_usage", labels)] = &seriesState{
		metric:   "cpu_usage",
		labels:   labels,
		lastSeen: lastSeen,
	}

	alerts := tr.Check()
	if len(alerts) != 1 {
		t.Fatalf("got %d alerts, want 1", len(alerts))
	}
	a := alerts[0]
	if a.Metric != "cpu_usage" {
		t.Errorf("Metric = %q, want %q", a.Metric, "cpu_usage")
	}
	if a.Labels["namespace"] != "production" {
		t.Errorf("Labels[namespace] = %q, want %q", a.Labels["namespace"], "production")
	}
	if a.LastSeen != lastSeen {
		t.Errorf("LastSeen = %v, want %v", a.LastSeen, lastSeen)
	}
	if a.SilentFor <= time.Minute {
		t.Errorf("SilentFor = %v, want > 1m", a.SilentFor)
	}
}

func TestCheck_SuppressedNamespace(t *testing.T) {
	tests := []struct {
		name      string
		patterns  []string
		namespace string
		wantAlert bool
	}{
		{"exact match suppresses", []string{"keda-system"}, "keda-system", false},
		{"wildcard suppresses matching", []string{"batch-*"}, "batch-jobs", false},
		{"wildcard does not suppress non-matching", []string{"batch-*"}, "production", true},
		{"no namespace label not suppressed", []string{"batch-*"}, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := newTestTracker(Config{
				Enabled:          true,
				Threshold:        time.Minute,
				SuppressPatterns: tt.patterns,
			})
			labels := map[string]string{}
			if tt.namespace != "" {
				labels["namespace"] = tt.namespace
			}
			tr.series[seriesKey("m1", labels)] = &seriesState{
				metric:   "m1",
				labels:   labels,
				lastSeen: time.Now().Add(-2 * time.Minute),
			}

			alerts := tr.Check()
			got := len(alerts) > 0
			if got != tt.wantAlert {
				t.Errorf("alert present = %v, want %v", got, tt.wantAlert)
			}
		})
	}
}

func TestCheck_MultipleSilentSeries(t *testing.T) {
	tr := newTestTracker(Config{Enabled: true, Threshold: time.Minute})
	past := time.Now().Add(-5 * time.Minute)

	for i := 0; i < 3; i++ {
		labels := map[string]string{"namespace": "prod", "pod": string(rune('a' + i))}
		tr.series[seriesKey("m1", labels)] = &seriesState{
			metric:   "m1",
			labels:   labels,
			lastSeen: past,
		}
	}

	alerts := tr.Check()
	if len(alerts) != 3 {
		t.Errorf("got %d alerts, want 3", len(alerts))
	}
}

func TestCheck_Integration_SilenceDetection(t *testing.T) {
	tr := newTestTracker(Config{Enabled: true, Threshold: 10 * time.Millisecond})
	labels := map[string]string{"namespace": "default"}

	tr.RecordSample("req_count", labels)
	time.Sleep(30 * time.Millisecond)

	alerts := tr.Check()
	if len(alerts) != 1 {
		t.Fatalf("got %d alerts after silence, want 1", len(alerts))
	}
	if alerts[0].Metric != "req_count" {
		t.Errorf("Metric = %q, want %q", alerts[0].Metric, "req_count")
	}
}

// newTestTracker creates a Tracker with startedAt far in the past to skip grace period.
func newTestTracker(cfg Config) *Tracker {
	tr := NewTracker(cfg)
	tr.startedAt = time.Now().Add(-24 * time.Hour)
	return tr
}
