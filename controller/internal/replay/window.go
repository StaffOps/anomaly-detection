// Package replay implements the replay mode for the controller, which simulates
// detection over historical metrics/logs without producing any side effects
// (no Redis writes, no Alertmanager dispatches, no gRPC fan-out to workers).
package replay

import (
	"fmt"
	"time"
)

// MinWindow is the minimum allowed replay window. Derived from the warm-up
// rule: warm-up = max(0.2 × window, warm_up_samples × tick_interval). For the
// default warm_up_samples=60 and tick=30s the absolute minimum warm-up is
// 30min, so a window of 30min / 0.2 = 2.5h ensures detection phase exists.
const MinWindow = 150 * time.Minute // 2.5h

// DefaultMaxWindow is the default maximum replay range. Operators can override
// via --max-range, but ranges beyond a week typically exceed Prometheus-compatible TSDB
// retention or produce baselines that drift too much for meaningful comparison.
const DefaultMaxWindow = 7 * 24 * time.Hour

// futureSlack tolerates small clock skew when checking that --to <= now.
// A few minutes is enough for typical NTP drift.
const futureSlack = 5 * time.Minute

// ParseWindow parses --from and --to flags into UTC time.Time values and
// validates the resulting range.
//
// Accepted formats:
//   - Relative duration: "24h", "30m", "7d" (interpreted as "now - duration")
//   - Absolute timestamp: RFC3339, e.g. "2026-05-30T00:00:00Z"
//
// fromStr is required (cannot be empty). toStr defaults to "now" when empty.
// maxRange is the maximum allowed window; pass DefaultMaxWindow for the default.
//
// Validations applied:
//   - both from and to are present
//   - from < to
//   - to <= now (with futureSlack tolerance)
//   - to - from >= MinWindow
//   - to - from <= maxRange
func ParseWindow(fromStr, toStr string, maxRange time.Duration) (time.Time, time.Time, error) {
	if maxRange <= 0 {
		maxRange = DefaultMaxWindow
	}
	now := time.Now().UTC()

	if fromStr == "" {
		return time.Time{}, time.Time{}, fmt.Errorf("--from is required")
	}

	from, err := parseTimeOrDuration(fromStr, now)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid --from %q: %w", fromStr, err)
	}

	var to time.Time
	if toStr == "" || toStr == "now" {
		to = now
	} else {
		to, err = parseTimeOrDuration(toStr, now)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid --to %q: %w", toStr, err)
		}
	}

	from = from.UTC()
	to = to.UTC()

	if !from.Before(to) {
		return time.Time{}, time.Time{}, fmt.Errorf("--from (%s) must be before --to (%s)",
			from.Format(time.RFC3339), to.Format(time.RFC3339))
	}

	if to.After(now.Add(futureSlack)) {
		return time.Time{}, time.Time{}, fmt.Errorf("--to (%s) is in the future (now=%s)",
			to.Format(time.RFC3339), now.Format(time.RFC3339))
	}

	window := to.Sub(from)
	if window < MinWindow {
		return time.Time{}, time.Time{}, fmt.Errorf(
			"window %s is below minimum %s (need at least %s for warm-up + detection)",
			window.Round(time.Second), MinWindow, MinWindow)
	}
	if window > maxRange {
		return time.Time{}, time.Time{}, fmt.Errorf(
			"window %s exceeds max-range %s (override with --max-range)",
			window.Round(time.Second), maxRange)
	}

	return from, to, nil
}

// parseTimeOrDuration accepts either a Go duration ("24h") or an RFC3339
// timestamp ("2026-05-30T00:00:00Z"). Durations are subtracted from `now`.
//
// Custom durations supported beyond stdlib: "Nd" for N days (Go's time.Parse
// does not natively accept "d"). Other units (h, m, s, ms, us, ns) come from
// stdlib unchanged.
func parseTimeOrDuration(s string, now time.Time) (time.Time, error) {
	// Try days ("Nd") first since stdlib doesn't accept "d".
	if d, ok, err := parseDays(s); ok {
		if err != nil {
			return time.Time{}, err
		}
		return now.Add(-d), nil
	}

	// Try Go duration ("24h", "30m", "1h30m", etc.)
	if d, err := time.ParseDuration(s); err == nil {
		if d < 0 {
			return time.Time{}, fmt.Errorf("duration must be positive (got %s)", d)
		}
		return now.Add(-d), nil
	}

	// Fallback: RFC3339 absolute timestamp
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("not a duration or RFC3339 timestamp")
	}
	return t, nil
}

// parseDays returns (duration, ok, err) where ok=true if the input matched
// the "Nd" pattern. ok=false signals "not a days expression — try other
// parsers". Used because stdlib time.ParseDuration does not accept "d".
func parseDays(s string) (time.Duration, bool, error) {
	if len(s) < 2 || s[len(s)-1] != 'd' {
		return 0, false, nil
	}
	num := s[:len(s)-1]
	var n int
	if _, err := fmt.Sscanf(num, "%d", &n); err != nil {
		return 0, false, nil // not a clean "Nd"
	}
	if n < 0 {
		return 0, true, fmt.Errorf("days must be non-negative (got %d)", n)
	}
	return time.Duration(n) * 24 * time.Hour, true, nil
}
