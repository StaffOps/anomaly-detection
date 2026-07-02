package replay

import (
	"strings"
	"testing"
	"time"
)

func TestParseWindow_DurationAndNow(t *testing.T) {
	from, to, err := ParseWindow("24h", "now", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if to.Sub(from) < 23*time.Hour || to.Sub(from) > 25*time.Hour {
		t.Errorf("expected ~24h window, got %s", to.Sub(from))
	}
	if from.Location() != time.UTC || to.Location() != time.UTC {
		t.Errorf("expected UTC, got from=%s to=%s", from.Location(), to.Location())
	}
}

func TestParseWindow_DurationDays(t *testing.T) {
	from, to, err := ParseWindow("3d", "", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := 72 * time.Hour
	got := to.Sub(from)
	if got < want-time.Minute || got > want+time.Minute {
		t.Errorf("expected ~72h, got %s", got)
	}
}

func TestParseWindow_AbsoluteTimestamps(t *testing.T) {
	tests := []struct {
		name       string
		from, to   string
		wantWindow time.Duration
	}{
		{"24h_absolute", "2026-05-29T00:00:00Z", "2026-05-30T00:00:00Z", 24 * time.Hour},
		{"6h_absolute", "2026-05-30T00:00:00Z", "2026-05-30T06:00:00Z", 6 * time.Hour},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// past timestamps relative to this code's creation, so won't trip the future check
			// (test timestamps are 2026; if test runs in 2025 we'd need to skip — accept this limitation)
			from, to, err := ParseWindow(tt.from, tt.to, 0)
			if err != nil {
				if strings.Contains(err.Error(), "in the future") {
					t.Skipf("skipping: test timestamp is in the future, system clock < 2026")
					return
				}
				t.Fatalf("unexpected error: %v", err)
			}
			if got := to.Sub(from); got != tt.wantWindow {
				t.Errorf("window: want %s, got %s", tt.wantWindow, got)
			}
		})
	}
}

func TestParseWindow_MixedDurationAndTimestamp(t *testing.T) {
	// from absolute (4h ago), to relative (1h ago) — valid 3h window > MinWindow (2.5h)
	from := time.Now().UTC().Add(-4 * time.Hour).Format(time.RFC3339)
	_, _, err := ParseWindow(from, "1h", 0)
	if err != nil {
		t.Errorf("unexpected error for mixed absolute-from + relative-to: %v", err)
	}
}

func TestParseWindow_DefaultsToNow(t *testing.T) {
	_, to1, err := ParseWindow("3h", "", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, to2, err := ParseWindow("3h", "now", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// to1 and to2 should be within a second of each other (set to now in both cases)
	if diff := to2.Sub(to1).Abs(); diff > 2*time.Second {
		t.Errorf("default `to` and explicit `now` differ by %s", diff)
	}
}

func TestParseWindow_FromRequired(t *testing.T) {
	_, _, err := ParseWindow("", "now", 0)
	if err == nil || !strings.Contains(err.Error(), "--from is required") {
		t.Errorf("expected required-from error, got: %v", err)
	}
}

func TestParseWindow_FromBeforeTo(t *testing.T) {
	// from = 1h ago (later) and to = 24h ago (earlier) — invalid order
	_, _, err := ParseWindow("1h", "24h", 0)
	if err == nil || !strings.Contains(err.Error(), "must be before") {
		t.Errorf("expected ordering error, got: %v", err)
	}
}

func TestParseWindow_FutureTo(t *testing.T) {
	// future timestamp far beyond the slack window
	future := time.Now().UTC().Add(2 * time.Hour).Format(time.RFC3339)
	_, _, err := ParseWindow("4h", future, 0)
	if err == nil || !strings.Contains(err.Error(), "in the future") {
		t.Errorf("expected future error, got: %v", err)
	}
}

func TestParseWindow_BelowMin(t *testing.T) {
	_, _, err := ParseWindow("1h", "now", 0) // 1h < 2.5h MinWindow
	if err == nil || !strings.Contains(err.Error(), "below minimum") {
		t.Errorf("expected min-window error, got: %v", err)
	}
}

func TestParseWindow_AboveMax(t *testing.T) {
	// override max-range to 1h, request 24h → should fail
	_, _, err := ParseWindow("24h", "now", 1*time.Hour)
	if err == nil || !strings.Contains(err.Error(), "exceeds max-range") {
		t.Errorf("expected max-window error, got: %v", err)
	}
}

func TestParseWindow_DefaultMax7d(t *testing.T) {
	_, _, err := ParseWindow("8d", "now", 0)
	if err == nil || !strings.Contains(err.Error(), "exceeds max-range") {
		t.Errorf("expected default 7d limit, got: %v", err)
	}
}

func TestParseWindow_AlwaysUTC(t *testing.T) {
	from, to, err := ParseWindow("4h", "now", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if from.Location() != time.UTC {
		t.Errorf("from not UTC: %s", from.Location())
	}
	if to.Location() != time.UTC {
		t.Errorf("to not UTC: %s", to.Location())
	}
}

func TestParseWindow_NegativeDuration(t *testing.T) {
	_, _, err := ParseWindow("-1h", "now", 0)
	if err == nil {
		t.Errorf("expected error for negative duration")
	}
}

func TestParseWindow_GarbageInput(t *testing.T) {
	_, _, err := ParseWindow("garbage", "now", 0)
	if err == nil || !strings.Contains(err.Error(), "invalid --from") {
		t.Errorf("expected invalid-from error, got: %v", err)
	}
}

func TestParseDays(t *testing.T) {
	tests := []struct {
		input   string
		wantD   time.Duration
		wantOk  bool
		wantErr bool
	}{
		{"3d", 72 * time.Hour, true, false},
		{"7d", 7 * 24 * time.Hour, true, false},
		{"0d", 0, true, false},
		{"-1d", 0, true, true},
		{"24h", 0, false, false}, // not days
		{"3", 0, false, false},   // no unit
		{"abc", 0, false, false}, // not a number
		{"3da", 0, false, false}, // doesn't end in `d`
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			d, ok, err := parseDays(tt.input)
			if ok != tt.wantOk {
				t.Errorf("ok: want %v, got %v", tt.wantOk, ok)
			}
			if (err != nil) != tt.wantErr {
				t.Errorf("err: want %v, got %v", tt.wantErr, err)
			}
			if !tt.wantErr && tt.wantOk && d != tt.wantD {
				t.Errorf("duration: want %s, got %s", tt.wantD, d)
			}
		})
	}
}
