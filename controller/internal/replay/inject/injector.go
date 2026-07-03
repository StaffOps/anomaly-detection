package inject

import (
	"math/rand"
	"sort"
	"strings"

	"github.com/staffops/staffops-anomaly-detection/internal/ingestion"
)

// Injector applies synthetic faults to TimeSeries based on an InjectionConfig.
// It is deterministic given the seed, and no-op when Config is nil.
type Injector struct {
	cfg      *InjectionConfig
	acc      *GroundTruthAccumulator
	recorded map[string]bool // tracks fingerprint+type to avoid duplicate ground truths
}

// NewInjector creates an Injector from the given config. If cfg is nil, the
// injector becomes a no-op passthrough (replay normal mode).
func NewInjector(cfg *InjectionConfig) *Injector {
	return &Injector{
		cfg:      cfg,
		acc:      NewGroundTruthAccumulator(),
		recorded: make(map[string]bool),
	}
}

// Apply perturbs the given series for the named metric according to the
// injection profile. Returns the (possibly modified) series. When no config
// is loaded or no injection targets this metric, returns the series unchanged
// (exact passthrough — no allocation).
//
// Ground truth entries are accumulated internally and can be retrieved via
// GroundTruths() after the replay loop completes.
func (inj *Injector) Apply(metricName string, series []ingestion.TimeSeries) []ingestion.TimeSeries {
	if inj.cfg == nil || len(inj.cfg.Injections) == 0 {
		return series
	}

	for _, entry := range inj.cfg.Injections {
		if entry.Target.Metric != metricName {
			continue
		}

		fn := faultFuncForType(entry.Type)
		if fn == nil {
			continue
		}

		// Deterministic RNG per injection entry: seed + entry index (stable).
		rng := rand.New(rand.NewSource(inj.cfg.Seed + int64(entryHash(entry))))

		for i := range series {
			if !matchLabels(entry.Target.Labels, series[i].Labels) {
				continue
			}

			// Record ground truth (once per series×injection combination).
			fp := Fingerprint(metricName, series[i].Labels)
			gtKey := fp + "/" + string(entry.Type) + "/" + entry.Start.String()
			if !inj.recorded[gtKey] {
				inj.recorded[gtKey] = true
				inj.acc.Add(GroundTruth{
					Target:    fp,
					Type:      entry.Type,
					Start:     entry.Start,
					End:       entry.End,
					Magnitude: entry.Magnitude,
				})
			}

			fn(&series[i], entry.Start, entry.End, entry.Magnitude, rng)
		}
	}

	return series
}

// GroundTruths returns all ground truth entries accumulated during Apply calls.
func (inj *Injector) GroundTruths() []GroundTruth {
	return inj.acc.Truths()
}

// Seed returns the seed from the injection config, or 0 if no config is loaded.
func (inj *Injector) Seed() int64 {
	if inj.cfg == nil {
		return 0
	}
	return inj.cfg.Seed
}

// matchLabels returns true if every key-value pair in required is present in
// the series' labels. An empty required map matches all series.
func matchLabels(required, actual map[string]string) bool {
	for k, v := range required {
		if actual[k] != v {
			return false
		}
	}
	return true
}

// entryHash produces a simple deterministic integer from an InjectionEntry,
// used to diversify the RNG per entry while keeping everything reproducible.
func entryHash(e InjectionEntry) int {
	h := 0
	for _, c := range e.Target.Metric {
		h = h*31 + int(c)
	}
	h = h*31 + int(e.Start.Unix())
	h = h*31 + int(e.End.Unix())
	return h
}

// Fingerprint produces the normalized target identifier used for matching
// anomalies against ground truth: "metric{key1=val1,key2=val2}" with labels
// sorted lexicographically by key. The __name__ label is excluded since the
// metric name is already the prefix.
func Fingerprint(metric string, labels map[string]string) string {
	if len(labels) == 0 {
		return metric
	}

	// Collect and sort label keys (exclude __name__).
	keys := make([]string, 0, len(labels))
	for k := range labels {
		if k == "__name__" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var sb strings.Builder
	sb.WriteString(metric)
	sb.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(k)
		sb.WriteByte('=')
		sb.WriteString(labels[k])
	}
	sb.WriteByte('}')
	return sb.String()
}
