package detection

import "testing"

func TestFilterByFloor(t *testing.T) {
	t.Run("empty map is a no-op", func(t *testing.T) {
		in := []*Anomaly{{MetricName: "x", Detector: "adaptive", Value: 0.5}}
		out, dropped := FilterByFloor(in, nil)
		if dropped != 0 || len(out) != 1 {
			t.Errorf("empty map: got kept=%d dropped=%d, want 1/0", len(out), dropped)
		}
	})

	t.Run("drops below-floor adaptive only", func(t *testing.T) {
		in := []*Anomaly{
			// active_requests floor 20
			{MetricName: "active_requests", Detector: "adaptive", Value: 46},  // above  → keep
			{MetricName: "active_requests", Detector: "adaptive", Value: 2},   // below  → drop
			{MetricName: "active_requests", Detector: "adaptive", Value: 20},  // equal  → keep
			{MetricName: "active_requests", Detector: "adaptive", Value: -46}, // |v| above → keep
			{MetricName: "active_requests", Detector: "adaptive", Value: -2},  // |v| below → drop
			// static/pattern never floored, even below the floor
			{MetricName: "active_requests", Detector: "static", Value: 1},
			{MetricName: "active_requests", Detector: "pattern", Value: 1},
			// rule without a floor passes through
			{MetricName: "unmapped", Detector: "adaptive", Value: 0.01},
			// floor <= 0 means "no floor"
			{MetricName: "zero_floor", Detector: "adaptive", Value: 0.01},
		}
		floors := map[string]float64{"active_requests": 20, "zero_floor": 0}
		out, dropped := FilterByFloor(in, floors)
		if dropped != 2 {
			t.Fatalf("got dropped=%d, want 2", dropped)
		}
		if len(out) != 7 {
			t.Fatalf("got kept=%d, want 7", len(out))
		}
		for _, a := range out {
			if a.Detector == "adaptive" && a.MetricName == "active_requests" && abs(a.Value) < 20 {
				t.Errorf("kept a below-floor adaptive anomaly: value=%v", a.Value)
			}
		}
	})
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
