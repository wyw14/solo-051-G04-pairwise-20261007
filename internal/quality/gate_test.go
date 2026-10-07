package quality

import (
	"testing"
	"time"

	"github.com/wyw14/cry-161/internal/journal"
	"github.com/wyw14/cry-161/internal/model"
	"github.com/wyw14/cry-161/internal/optical"
	"github.com/wyw14/cry-161/internal/quartz"
)

var (
	now     = time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	layer   = model.Layer{Number: 1, Material: "SiO2", TargetNanometers: 100, RateNanometers: 2}
	spectra = journal.SpectrumRecord{Wavelength: []float64{400, 500, 600}, Intensity: []float64{0.4, 0.5, 0.6}}
)

func sample(run model.ID, freq float64, at time.Time) model.ThicknessSample {
	return model.ThicknessSample{RunID: run, Frequency: freq, Transmission: 0.5, Valid: true, At: at}
}

// calibration.Thickness(235.7) = 0.42*235.7 + 1.5 ≈ 100.5 nm, within tolerance of target 100.
func releaseGate() (*Gate, *optical.ThicknessWindow) {
	window := optical.NewThicknessWindow(16)
	for i := 0; i < 6; i++ {
		window.Add(sample("run-1", 235.7, now.Add(-time.Duration(6-i)*time.Second)))
	}
	return NewGate(window, quartz.DefaultCalibration(), now.Add(-time.Hour), DefaultLimits()), window
}

func TestReleaseWhenAllInputsHealthy(t *testing.T) {
	gate, _ := releaseGate()
	decision := gate.Evaluate("run-1", layer, spectra, now)
	if decision.Verdict != VerdictRelease {
		t.Fatalf("expected release, got %s reasons=%v", decision.Verdict, decision.Reasons)
	}
	if decision.EstimatedNanometers < 99 || decision.EstimatedNanometers > 102 {
		t.Fatalf("unexpected estimate %f", decision.EstimatedNanometers)
	}
	if decision.Confidence <= 0 {
		t.Fatalf("expected positive confidence, got %f", decision.Confidence)
	}
}

func TestExpiredCalibrationForcesReview(t *testing.T) {
	window := optical.NewThicknessWindow(16)
	window.Add(sample("run-1", 235.7, now.Add(-time.Second)))
	stale := NewGate(window, quartz.DefaultCalibration(), now.Add(-48*time.Hour), DefaultLimits())
	decision := stale.Evaluate("run-1", layer, spectra, now)
	assertBlocked(t, decision, "calibration_expired")
}

func TestFrequencyAnomalyForcesReview(t *testing.T) {
	window := optical.NewThicknessWindow(16)
	window.Add(sample("run-1", 235.7, now.Add(-2*time.Second)))
	window.Add(sample("run-1", 0, now.Add(-time.Second)))
	gate := NewGate(window, quartz.DefaultCalibration(), now.Add(-time.Hour), DefaultLimits())
	decision := gate.Evaluate("run-1", layer, spectra, now)
	assertBlocked(t, decision, "frequency_anomaly")
}

func TestCrossRunSamplesForceReview(t *testing.T) {
	window := optical.NewThicknessWindow(16)
	window.Add(sample("run-1", 235.7, now.Add(-2*time.Second)))
	window.Add(sample("run-old", 500, now.Add(-time.Second)))
	gate := NewGate(window, quartz.DefaultCalibration(), now.Add(-time.Hour), DefaultLimits())
	decision := gate.Evaluate("run-1", layer, spectra, now)
	assertBlocked(t, decision, "cross_run_samples")
}

func TestNoSamplesForcesReview(t *testing.T) {
	gate := NewGate(optical.NewThicknessWindow(16), quartz.DefaultCalibration(), now.Add(-time.Hour), DefaultLimits())
	decision := gate.Evaluate("run-1", layer, spectra, now)
	assertBlocked(t, decision, "no_samples")
}

func TestOutOfToleranceForcesReview(t *testing.T) {
	window := optical.NewThicknessWindow(16)
	for i := 0; i < 6; i++ {
		window.Add(sample("run-1", 500, now.Add(-time.Duration(6-i)*time.Second)))
	}
	gate := NewGate(window, quartz.DefaultCalibration(), now.Add(-time.Hour), DefaultLimits())
	decision := gate.Evaluate("run-1", layer, spectra, now)
	if decision.Verdict != VerdictReview {
		t.Fatalf("expected review, got %s reasons=%v", decision.Verdict, decision.Reasons)
	}
	found := false
	for _, r := range decision.Reasons {
		if r == "target_out_of_tolerance" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected target_out_of_tolerance in %v", decision.Reasons)
	}
	if decision.EstimatedNanometers != 211.5 {
		t.Fatalf("current-run estimate should still be reported, got %f", decision.EstimatedNanometers)
	}
}

func TestInvalidSpectrumForcesReview(t *testing.T) {
	gate, _ := releaseGate()
	decision := gate.Evaluate("run-1", layer, journal.SpectrumRecord{Wavelength: []float64{400}}, now)
	assertBlocked(t, decision, "optical_invalid")
}

func assertBlocked(t *testing.T, decision Decision, reason string) {
	t.Helper()
	if decision.Verdict != VerdictReview {
		t.Fatalf("expected review, got %s reasons=%v", decision.Verdict, decision.Reasons)
	}
	found := false
	for _, r := range decision.Reasons {
		if r == reason {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected reason %q in %v", reason, decision.Reasons)
	}
	if decision.EstimatedNanometers != 0 || decision.Confidence != 0 {
		t.Fatalf("blocked decision must not report thickness, got estimate=%f confidence=%f", decision.EstimatedNanometers, decision.Confidence)
	}
}
