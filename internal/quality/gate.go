package quality

import (
	"math"
	"time"

	"github.com/wyw14/cry-161/internal/model"
	"github.com/wyw14/cry-161/internal/quartz"
)

type Verdict string

const (
	VerdictReleaseable Verdict = "releaseable"
	VerdictReview      Verdict = "review"
)

const (
	DefaultCalibrationTTL = 24 * time.Hour
	MaxDriftNanometers    = 0.5
	MinConfidence         = 0.6
	FrequencyTolerance    = 0.2
	MinSamples            = 3
)

type GateInput struct {
	RunID          model.ID
	Layer          model.Layer
	Calibration    quartz.Calibration
	CalibratedAt   time.Time
	CalibrationTTL time.Duration
	Samples        []model.ThicknessSample
	SpectrumScore  float64
	Now            time.Time
}

type Decision struct {
	Verdict    Verdict  `json:"verdict"`
	Thickness  float64  `json:"thickness_nm"`
	Drift      float64  `json:"drift_nm"`
	Confidence float64  `json:"confidence"`
	Reasons    []string `json:"reasons,omitempty"`
}

func (d Decision) Releaseable() bool { return d.Verdict == VerdictReleaseable }

func Evaluate(input GateInput) Decision {
	decision := Decision{Verdict: VerdictReview, Reasons: make([]string, 0)}
	if input.Now.IsZero() {
		input.Now = time.Now().UTC()
	}
	if input.CalibrationTTL <= 0 {
		input.CalibrationTTL = DefaultCalibrationTTL
	}
	if input.CalibratedAt.IsZero() || input.Now.Sub(input.CalibratedAt) > input.CalibrationTTL {
		decision.Reasons = append(decision.Reasons, "calibration expired")
	}
	own := make([]model.ThicknessSample, 0, len(input.Samples))
	foreign := 0
	for _, sample := range input.Samples {
		if sample.RunID == input.RunID {
			own = append(own, sample)
		} else {
			foreign++
		}
	}
	if foreign > 0 {
		decision.Reasons = append(decision.Reasons, "window contains samples from other runs")
	}
	if len(own) == 0 {
		decision.Reasons = append(decision.Reasons, "no current-run samples")
		return decision
	}
	expected := 0.0
	if input.Calibration.Slope > 0 {
		expected = (input.Layer.TargetNanometers - input.Calibration.Offset) / input.Calibration.Slope
	}
	drift := 0.0
	anomaly := false
	for _, sample := range own {
		if !sample.Valid || sample.Frequency <= 0 {
			anomaly = true
			continue
		}
		if expected > 0 && math.Abs(sample.Frequency-expected)/expected > FrequencyTolerance {
			anomaly = true
		}
		drift = math.Max(drift, math.Abs(input.Calibration.Thickness(sample.Frequency)-sample.Nanometers))
	}
	if anomaly {
		decision.Reasons = append(decision.Reasons, "frequency anomaly")
	}
	decision.Drift = drift
	if drift > MaxDriftNanometers {
		decision.Reasons = append(decision.Reasons, "drift exceeds tolerance")
	}
	latest := own[len(own)-1]
	sampleFactor := math.Min(1, float64(len(own))/MinSamples)
	driftFactor := math.Max(0, 1-drift/MaxDriftNanometers)
	spectrumFactor := math.Min(1, math.Max(0, input.SpectrumScore))
	decision.Confidence = sampleFactor * driftFactor * spectrumFactor
	if decision.Confidence < MinConfidence {
		decision.Reasons = append(decision.Reasons, "confidence below threshold")
	}
	if len(decision.Reasons) > 0 {
		return decision
	}
	decision.Verdict = VerdictReleaseable
	decision.Thickness = input.Calibration.Thickness(latest.Frequency)
	return decision
}
