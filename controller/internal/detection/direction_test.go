package detection

import "testing"

func TestDirectionAllows(t *testing.T) {
	cases := []struct {
		name      string
		value     float64
		mean      float64
		direction string
		want      bool
	}{
		{"up_bad + rising fires", 10, 5, DirectionUpBad, true},
		{"up_bad + falling dropped", 2, 5, DirectionUpBad, false},
		{"up_bad + equal fires", 5, 5, DirectionUpBad, true},
		{"down_bad + falling fires", 2, 5, DirectionDownBad, true},
		{"down_bad + rising dropped", 10, 5, DirectionDownBad, false},
		{"down_bad + equal fires", 5, 5, DirectionDownBad, true},
		{"both_bad always fires (rising)", 10, 5, DirectionBothBad, true},
		{"both_bad always fires (falling)", 2, 5, DirectionBothBad, true},
		{"empty always fires", 2, 5, "", true},
		{"unknown direction is permissive", 2, 5, "sideways", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := Anomaly{Value: c.value, Mean: c.mean}
			if got := DirectionAllows(a, c.direction); got != c.want {
				t.Errorf("DirectionAllows(value=%v mean=%v dir=%q) = %v, want %v",
					c.value, c.mean, c.direction, got, c.want)
			}
		})
	}
}

func TestFilterByDirection(t *testing.T) {
	t.Run("empty map is a no-op", func(t *testing.T) {
		in := []*Anomaly{{MetricName: "x", Detector: "adaptive", Value: 2, Mean: 5}}
		out, dropped := FilterByDirection(in, nil)
		if dropped != 0 || len(out) != 1 {
			t.Errorf("empty map: got kept=%d dropped=%d, want 1/0", len(out), dropped)
		}
	})

	t.Run("drops wrong-direction adaptive only", func(t *testing.T) {
		in := []*Anomaly{
			{MetricName: "latency", Detector: "adaptive", Value: 10, Mean: 5}, // up, up_bad → keep
			{MetricName: "latency", Detector: "adaptive", Value: 2, Mean: 5},  // down, up_bad → drop
			{MetricName: "replicas", Detector: "adaptive", Value: 1, Mean: 3}, // down, down_bad → keep
			{MetricName: "replicas", Detector: "adaptive", Value: 9, Mean: 3}, // up, down_bad → drop
			{MetricName: "restarts", Detector: "static", Value: 2, Mean: 5},   // static → keep (not adaptive)
			{MetricName: "unmapped", Detector: "adaptive", Value: 2, Mean: 5}, // no direction → keep
		}
		dirs := map[string]string{"latency": DirectionUpBad, "replicas": DirectionDownBad}
		out, dropped := FilterByDirection(in, dirs)
		if dropped != 2 {
			t.Fatalf("got dropped=%d, want 2", dropped)
		}
		if len(out) != 4 {
			t.Fatalf("got kept=%d, want 4", len(out))
		}
		for _, a := range out {
			if a.MetricName == "latency" && a.Value < a.Mean {
				t.Error("kept a falling up_bad latency anomaly")
			}
			if a.MetricName == "replicas" && a.Value > a.Mean {
				t.Error("kept a rising down_bad replicas anomaly")
			}
		}
	})
}
