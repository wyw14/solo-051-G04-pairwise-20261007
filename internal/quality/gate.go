package quality

import (
	"math"
	"sort"
	"time"

	"github.com/wyw14/cry-161/internal/journal"
	"github.com/wyw14/cry-161/internal/model"
	"github.com/wyw14/cry-161/internal/optical"
	"github.com/wyw14/cry-161/internal/quartz"
)

type Verdict string

const (
	VerdictRelease Verdict = "release"
	VerdictReview  Verdict = "review"
)

type Decision struct {
	RunID               model.ID   `json:"run_id"`
	LayerNumber         int        `json:"layer_number"`
	Verdict             Verdict    `json:"verdict"`
	Reasons             []string   `json:"reasons"`
	EstimatedNanometers float64    `json:"estimated_nm"`
	TargetNanometers    float64    `json:"target_nm"`
	DriftNanometers     float64    `json:"drift_nm"`
	Confidence          float64    `json:"confidence"`
	SampleCount         int        `json:"sample_count"`
	EvaluatedAt         time.Time  `json:"evaluated_at"`
}

type Limits struct {
	MaxCalibrationAge time.Duration
	MaxSampleAge      time.Duration
	ToleranceNm       float64
	MinConfidence     float64
}

func DefaultLimits() Limits {
	return Limits{MaxCalibrationAge: 24 * time.Hour, MaxSampleAge: 30 * time.Second, ToleranceNm: 5, MinConfidence: 0.6}
}

type Gate struct {
	window       *optical.ThicknessWindow
	calibration  quartz.Calibration
	calibratedAt time.Time
	limits       Limits
}

func NewGate(window *optical.ThicknessWindow, calibration quartz.Calibration, calibratedAt time.Time, limits Limits) *Gate {
	if window == nil {
		window = optical.NewThicknessWindow(1)
	}
	return &Gate{window: window, calibration: calibration, calibratedAt: calibratedAt, limits: limits}
}

func (g *Gate) Evaluate(runID model.ID, layer model.Layer, spectrum journal.SpectrumRecord, now time.Time) Decision {
	decision := Decision{RunID: runID, LayerNumber: layer.Number, TargetNanometers: layer.TargetNanometers, Verdict: VerdictReview, Reasons: make([]string, 0, 4), EvaluatedAt: now}

	if _, err := optical.Analyze(spectrum); err != nil {
		decision.Reasons = append(decision.Reasons, "optical_invalid")
	}
	if g.calibratedAt.IsZero() || now.Sub(g.calibratedAt) > g.limits.MaxCalibrationAge || now.Before(g.calibratedAt) {
		decision.Reasons = append(decision.Reasons, "calibration_expired")
	}

	sameRun := make([]model.ThicknessSample, 0, len(g.window.Values()))
	crossRun := 0
	for _, sample := range g.window.Values() {
		if sample.RunID == runID {
			sameRun = append(sameRun, sample)
		} else {
			crossRun++
		}
	}
	if crossRun > 0 {
		decision.Reasons = append(decision.Reasons, "cross_run_samples")
	}
	if len(sameRun) == 0 {
		decision.Reasons = append(decision.Reasons, "no_samples")
	}
	if frequencyAnomaly(sameRun) {
		decision.Reasons = append(decision.Reasons, "frequency_anomaly")
	}

	// Blocked: never report historical thickness as the current layer result.
	if len(decision.Reasons) > 0 {
		return decision
	}

	sort.Slice(sameRun, func(i, j int) bool { return sameRun[i].At.Before(sameRun[j].At) })
	latest := sameRun[len(sameRun)-1]
	estimate := g.calibration.Thickness(latest.Frequency)
	mean := 0.0
	variance := 0.0
	for _, sample := range sameRun {
		mean += g.calibration.Thickness(sample.Frequency)
	}
	mean /= float64(len(sameRun))
	for _, sample := range sameRun {
		delta := g.calibration.Thickness(sample.Frequency) - mean
		variance += delta * delta
	}
	spread := math.Sqrt(variance / float64(len(sameRun)))

	decision.EstimatedNanometers = estimate
	decision.DriftNanometers = estimate - mean
	decision.SampleCount = len(sameRun)
	decision.Confidence = confidence(len(sameRun), now.Sub(latest.At), g.limits.MaxSampleAge, spread)

	if math.Abs(estimate-layer.TargetNanometers) > g.limits.ToleranceNm {
		decision.Reasons = append(decision.Reasons, "target_out_of_tolerance")
	}
	if decision.Confidence < g.limits.MinConfidence {
		decision.Reasons = append(decision.Reasons, "low_confidence")
	}
	if len(decision.Reasons) == 0 {
		decision.Verdict = VerdictRelease
	}
	return decision
}

func frequencyAnomaly(samples []model.ThicknessSample) bool {
	if len(samples) == 0 {
		return false
	}
	frequencies := make([]float64, 0, len(samples))
	for _, sample := range samples {
		if !sample.Valid || sample.Frequency <= 0 {
			return true
		}
		frequencies = append(frequencies, sample.Frequency)
	}
	sort.Float64s(frequencies)
	median := frequencies[len(frequencies)/2]
	for _, frequency := range frequencies {
		if math.Abs(frequency-median)/median > 0.5 {
			return true
		}
	}
	return false
}

func confidence(count int, age, maxAge time.Duration, spread float64) float64 {
	countFactor := math.Min(1, float64(count)/5)
	recencyFactor := 1.0
	if maxAge > 0 && age > maxAge {
		recencyFactor = math.Max(0, 1-float64(age-maxAge)/float64(maxAge))
	}
	spreadFactor := 1 / (1 + spread)
	return math.Max(0, math.Min(1, countFactor*recencyFactor*spreadFactor))
}
