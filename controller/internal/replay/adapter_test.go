package replay

import (
	"testing"
	"time"

	"github.com/staffops/staffops-anomaly-detection/internal/ingestion"
)

func TestSamplesAt(t *testing.T) {
	base := time.Date(2026, 5, 30, 10, 0, 0, 0, time.UTC)

	series := []ingestion.TimeSeries{
		{
			Labels: map[string]string{"pod": "a"},
			Points: []ingestion.Point{
				{T: base, V: 1.0},
				{T: base.Add(30 * time.Second), V: 2.0},
				{T: base.Add(60 * time.Second), V: 3.0},
			},
		},
		{
			Labels: map[string]string{"pod": "b"},
			Points: []ingestion.Point{
				{T: base, V: 10.0},
				{T: base.Add(60 * time.Second), V: 20.0},
			},
		},
	}

	tests := []struct {
		name     string
		ts       time.Time
		wantLen  int
		wantVals map[string]float64
	}{
		{
			name:     "exact match at 30s",
			ts:       base.Add(30 * time.Second),
			wantLen:  2,
			wantVals: map[string]float64{"a": 2.0, "b": 10.0},
		},
		{
			name:     "between points at 45s",
			ts:       base.Add(45 * time.Second),
			wantLen:  2,
			wantVals: map[string]float64{"a": 2.0, "b": 10.0},
		},
		{
			name:     "at end of window",
			ts:       base.Add(60 * time.Second),
			wantLen:  2,
			wantVals: map[string]float64{"a": 3.0, "b": 20.0},
		},
		{
			name:    "before any points",
			ts:      base.Add(-1 * time.Second),
			wantLen: 0,
		},
		{
			name:     "at exact start",
			ts:       base,
			wantLen:  2,
			wantVals: map[string]float64{"a": 1.0, "b": 10.0},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SamplesAt(tt.ts, series)
			if len(got) != tt.wantLen {
				t.Fatalf("got %d samples, want %d", len(got), tt.wantLen)
			}
			for _, s := range got {
				pod := s.Labels["pod"]
				want, ok := tt.wantVals[pod]
				if !ok {
					t.Errorf("unexpected pod %q in results", pod)
					continue
				}
				if s.Value != want {
					t.Errorf("pod %q: got value %f, want %f", pod, s.Value, want)
				}
			}
		})
	}
}

func TestSamplesAt_EmptySeries(t *testing.T) {
	ts := time.Now().UTC()
	got := SamplesAt(ts, nil)
	if len(got) != 0 {
		t.Fatalf("expected 0 samples for nil series, got %d", len(got))
	}

	got = SamplesAt(ts, []ingestion.TimeSeries{{Labels: map[string]string{"x": "y"}, Points: nil}})
	if len(got) != 0 {
		t.Fatalf("expected 0 samples for series with no points, got %d", len(got))
	}
}
