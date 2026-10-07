package quality

import (
	"testing"
	"time"

	"github.com/wyw14/cry-161/internal/model"
	"github.com/wyw14/cry-161/internal/quartz"
)

func baseInput() GateInput {
	runID := model.NewID()
	calibration := quartz.DefaultCalibration()
	layer := model.Layer{Number: 1, TargetNanometers: 50, RateNanometers: 2}
	expected := (layer.TargetNanometers - calibration.Offset) / calibration.Slope
	samples := make([]model.ThicknessSample, 0, MinSamples)
	for i := 0; i < MinSamples; i++ {
		samples = append(samples, model.ThicknessSample{RunID: runID, Frequency: expected, Transmission: 0.9, Nanometers: calibration.Thickness(expected), Valid: true, At: time.Now().UTC()})
	}
	return GateInput{RunID: runID, Layer: layer, Calibration: calibration, CalibratedAt: time.Now().UTC(), Samples: samples, SpectrumScore: 0.95, Now: time.Now().UTC()}
}

func TestEvaluateReleaseable(t *testing.T) {
	decision := Evaluate(baseInput())
	if !decision.Releaseable() {
		t.Fatalf("expected releaseable, got %v reasons=%v", decision.Verdict, decision.Reasons)
	}
	if decision.Thickness != 50 {
		t.Fatalf("expected thickness derived from calibration, got %v", decision.Thickness)
	}
	if decision.Confidence < MinConfidence {
		t.Fatalf("expected confidence >= %v, got %v", MinConfidence, decision.Confidence)
	}
}

func TestEvaluateExpiredCalibration(t *testing.T) {
	input := baseInput()
	input.CalibratedAt = input.Now.Add(-2 * DefaultCalibrationTTL)
	decision := Evaluate(input)
	if decision.Releaseable() {
		t.Fatalf("expected review for expired calibration")
	}
	if decision.Thickness != 0 {
		t.Fatalf("review decision must not emit a thickness, got %v", decision.Thickness)
	}
}

func TestEvaluateFrequencyAnomaly(t *testing.T) {
	input := baseInput()
	input.Samples[1].Frequency = input.Samples[1].Frequency * 2
	decision := Evaluate(input)
	if decision.Releaseable() {
		t.Fatalf("expected review for frequency anomaly")
	}
}

func TestEvaluateCrossRunSamples(t *testing.T) {
	input := baseInput()
	historical := model.ThicknessSample{RunID: model.NewID(), Frequency: 115, Transmission: 0.9, Nanometers: 999, Valid: true, At: time.Now().UTC()}
	input.Samples = append([]model.ThicknessSample{historical}, input.Samples...)
	decision := Evaluate(input)
	if decision.Releaseable() {
		t.Fatalf("expected review when window spans runs")
	}
	if decision.Thickness == historical.Nanometers {
		t.Fatalf("historical thickness must not become the current layer result")
	}
}

func TestEvaluateNoCurrentRunSamples(t *testing.T) {
	input := baseInput()
	for i := range input.Samples {
		input.Samples[i].RunID = model.NewID()
	}
	decision := Evaluate(input)
	if decision.Releaseable() {
		t.Fatalf("expected review with no current-run samples")
	}
	if decision.Thickness != 0 {
		t.Fatalf("review decision must not emit a thickness, got %v", decision.Thickness)
	}
}
