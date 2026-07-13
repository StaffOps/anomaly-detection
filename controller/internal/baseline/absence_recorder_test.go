package baseline

import (
	"testing"

	"github.com/staffops/staffops-anomaly-detection/internal/config"
)

// recordingAbsence captures RecordSample calls for assertion.
type recordingAbsence struct {
	calls []string
}

func (r *recordingAbsence) RecordSample(metric string, _ map[string]string) {
	r.calls = append(r.calls, metric)
}

func TestSetAbsenceRecorder_ReplacesNoop(t *testing.T) {
	s := NewStore(nil, config.Baseline{})

	// Default recorder is the no-op; calling it must not panic.
	s.absence.RecordSample("cpu", map[string]string{"pod": "x"})

	rec := &recordingAbsence{}
	s.SetAbsenceRecorder(rec)

	// After swapping, the store's recorder is the injected one.
	s.absence.RecordSample("mem", map[string]string{"pod": "y"})
	if len(rec.calls) != 1 || rec.calls[0] != "mem" {
		t.Fatalf("expected RecordSample(\"mem\") to be forwarded, got %v", rec.calls)
	}
}

func TestNoopRecorder_DoesNotPanic(t *testing.T) {
	// Guard the zero-value path explicitly.
	noopRecorder{}.RecordSample("anything", nil)
}
