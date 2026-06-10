package scoring

import (
	"math"
	"testing"
)

func TestBrierBounds(t *testing.T) {
	if Brier(1, true) != 0 || Brier(0, false) != 0 {
		t.Error("perfect forecast should score 0")
	}
	if Brier(1, false) != 1 || Brier(0, true) != 1 {
		t.Error("confident miss should score 1")
	}
}

func TestLogLossFiniteOnConfidentMiss(t *testing.T) {
	if v := LogLoss(0, true); math.IsInf(v, 0) || v < 0 {
		t.Errorf("log loss should be finite and positive, got %v", v)
	}
	if LogLoss(1, true) > 1e-9 {
		t.Error("perfect confident hit should be ~0")
	}
}

func TestPoissonDevianceZeroAtTruth(t *testing.T) {
	if d := PoissonDeviance(3.0, 3); d > 1e-9 {
		t.Errorf("deviance at mu==y should be ~0, got %v", d)
	}
	// Worse mean -> larger deviance.
	if PoissonDeviance(1.0, 5) <= PoissonDeviance(4.0, 5) {
		t.Error("deviance should grow as the mean moves from the count")
	}
	// y==0 branch is finite and increases with mu.
	if PoissonDeviance(2.0, 0) <= PoissonDeviance(0.5, 0) {
		t.Error("for y=0, larger mu should be more deviant")
	}
}

func TestReliabilityPerfectCalibration(t *testing.T) {
	r := NewReliability(10)
	// A forecast of 0.3 that occurs 30% of the time is perfectly calibrated.
	for i := 0; i < 1000; i++ {
		r.Add(0.3, i%10 < 3)
	}
	if ce := r.CalibrationError(); ce > 0.02 {
		t.Errorf("calibration error = %.3f, want ~0", ce)
	}
}

func TestReliabilityDetectsOverconfidence(t *testing.T) {
	r := NewReliability(10)
	// Forecast 0.9 but it only happens half the time -> miscalibrated.
	for i := 0; i < 1000; i++ {
		r.Add(0.9, i%2 == 0)
	}
	if ce := r.CalibrationError(); ce < 0.3 {
		t.Errorf("calibration error = %.3f, want large (overconfident)", ce)
	}
}
