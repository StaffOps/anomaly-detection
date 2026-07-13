package inject

import (
	"time"
)

// ScoringResult holds the computed metrics from comparing detected anomalies
// against ground truth. Serialized into the report's "scoring" block.
type ScoringResult struct {
	Precision        float64            `json:"precision"`
	Recall           float64            `json:"recall"`
	F1               float64            `json:"f1"`
	TP               int                `json:"tp"`
	FP               int                `json:"fp"`
	FN               int                `json:"fn"`
	RecallByType     map[string]float64 `json:"recall_by_type"`
	DetectionLatency map[string]float64 `json:"detection_latency_seconds"`
	FPCaveat         string             `json:"fp_caveat"`
}

// DetectedAnomaly is a minimal representation of a detected anomaly for
// scoring purposes. Avoids circular import with the replay package.
type DetectedAnomaly struct {
	Metric    string
	Labels    map[string]string
	Timestamp time.Time
}

// Score classifies each detected anomaly as TP or FP and each ground truth
// as detected or FN. grace is the tolerance window after a truth's End
// within which a detection still counts as a match (typically 1 tick interval).
//
// A detected anomaly is TP if:
//   - its normalized fingerprint matches a GroundTruth target, AND
//   - its timestamp falls within [truth.Start, truth.End + grace]
//
// A GroundTruth is detected (not FN) if at least one TP maps to it.
func Score(anomalies []DetectedAnomaly, truths []GroundTruth, grace time.Duration) *ScoringResult {
	if len(truths) == 0 && len(anomalies) == 0 {
		return &ScoringResult{
			RecallByType:     map[string]float64{},
			DetectionLatency: map[string]float64{},
			FPCaveat:         fpCaveat,
		}
	}

	// Track which truths have been detected and the earliest detection time.
	type truthState struct {
		detected       bool
		firstDetection time.Time
	}
	states := make([]truthState, len(truths))

	tpCount := 0
	fpCount := 0

	for _, da := range anomalies {
		anomFP := Fingerprint(da.Metric, da.Labels)
		matched := false

		for i, gt := range truths {
			if anomFP != gt.Target {
				continue
			}
			deadline := gt.End.Add(grace)
			if !da.Timestamp.Before(gt.Start) && !da.Timestamp.After(deadline) {
				matched = true
				if !states[i].detected || da.Timestamp.Before(states[i].firstDetection) {
					states[i].firstDetection = da.Timestamp
				}
				states[i].detected = true
				break
			}
		}

		if matched {
			tpCount++
		} else {
			fpCount++
		}
	}

	// Count FN and compute recall-by-type.
	fnCount := 0
	typeDetected := make(map[string]int)
	typeTotal := make(map[string]int)

	for i, gt := range truths {
		typeTotal[string(gt.Type)]++
		if states[i].detected {
			typeDetected[string(gt.Type)]++
		} else {
			fnCount++
		}
	}

	// Precision, recall, F1.
	precision := 0.0
	if tpCount+fpCount > 0 {
		precision = float64(tpCount) / float64(tpCount+fpCount)
	}
	detectedCount := 0
	for _, s := range states {
		if s.detected {
			detectedCount++
		}
	}
	recall := 0.0
	if len(truths) > 0 {
		recall = float64(detectedCount) / float64(len(truths))
	}
	f1 := 0.0
	if precision+recall > 0 {
		f1 = 2 * precision * recall / (precision + recall)
	}

	// Recall by type.
	recallByType := make(map[string]float64)
	for ft, total := range typeTotal {
		if total > 0 {
			recallByType[ft] = float64(typeDetected[ft]) / float64(total)
		}
	}

	// Detection latency per detected truth.
	latency := make(map[string]float64)
	for i, gt := range truths {
		if states[i].detected {
			key := gt.Target + "/" + string(gt.Type)
			latency[key] = states[i].firstDetection.Sub(gt.Start).Seconds()
		}
	}

	return &ScoringResult{
		Precision:        precision,
		Recall:           recall,
		F1:               f1,
		TP:               tpCount,
		FP:               fpCount,
		FN:               fnCount,
		RecallByType:     recallByType,
		DetectionLatency: latency,
		FPCaveat:         fpCaveat,
	}
}

// BuildInjectionResult creates the JSON-serializable injection metadata block.
func BuildInjectionResult(seed int64, truths []GroundTruth) *InjectionResult {
	gts := make([]GroundTruthJSON, len(truths))
	for i, gt := range truths {
		gts[i] = GroundTruthJSON{
			Target:    gt.Target,
			Type:      string(gt.Type),
			Start:     gt.Start.UTC().Format(time.RFC3339),
			End:       gt.End.UTC().Format(time.RFC3339),
			Magnitude: gt.Magnitude,
		}
	}
	return &InjectionResult{
		Seed:         seed,
		GroundTruths: gts,
	}
}

// InjectionResult holds the injection metadata for the report's "injection" block.
type InjectionResult struct {
	Seed         int64             `json:"seed"`
	GroundTruths []GroundTruthJSON `json:"ground_truths"`
}

// GroundTruthJSON is the JSON-serializable form of a GroundTruth entry.
type GroundTruthJSON struct {
	Target    string  `json:"target"`
	Type      string  `json:"type"`
	Start     string  `json:"start"`
	End       string  `json:"end"`
	Magnitude float64 `json:"magnitude"`
}

const fpCaveat = "FP is an upper-bound: the clean window may contain unlabeled real incidents"
