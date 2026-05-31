package replay

import "time"

// ReplayConfig holds the parameters for a replay run.
type ReplayConfig struct {
	From            time.Time
	To              time.Time
	ConfigPath      string
	OutputPath      string
	WarmupFraction  float64
	MaxAnomalies    int
}

// DefaultReplayConfig returns a ReplayConfig with sensible defaults.
// From and To must be set by the caller (via ParseWindow).
func DefaultReplayConfig() ReplayConfig {
	return ReplayConfig{
		OutputPath:     "./replay-report.json",
		WarmupFraction: 0.2,
		MaxAnomalies:   1000,
	}
}
