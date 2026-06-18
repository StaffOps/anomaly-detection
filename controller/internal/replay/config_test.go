package replay

import "testing"

func TestDefaultReplayConfig(t *testing.T) {
	cfg := DefaultReplayConfig()
	if cfg.OutputPath != "./replay-report.json" {
		t.Errorf("OutputPath: want ./replay-report.json, got %q", cfg.OutputPath)
	}
	if cfg.WarmupFraction != 0.2 {
		t.Errorf("WarmupFraction: want 0.2, got %v", cfg.WarmupFraction)
	}
	if cfg.MaxAnomalies != 1000 {
		t.Errorf("MaxAnomalies: want 1000, got %d", cfg.MaxAnomalies)
	}
	// From/To must be set by caller — should be zero
	if !cfg.From.IsZero() || !cfg.To.IsZero() {
		t.Error("From/To should be zero by default")
	}
}
