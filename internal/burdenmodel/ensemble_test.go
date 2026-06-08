package burdenmodel

import (
	"math"
	"testing"
)

func meanOf(xs []float64) float64 {
	var s float64
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

func TestEnsembleZeroRateIsAllPipeline(t *testing.T) {
	out := Ensemble(10.0, ShockParams{Rate: 0, Shape: 1, JumpRate: 1}, 50, 42)
	for i, v := range out {
		if v != 10.0 {
			t.Fatalf("realisation %d = %v, want 10.0 (no shock)", i, v)
		}
	}
}

func TestEnsembleIsSeedDeterministic(t *testing.T) {
	p := ShockParams{Rate: 2, Shape: 1, JumpRate: 0.5}
	a := Ensemble(5, p, 64, 9)
	b := Ensemble(5, p, 64, 9)
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("same seed not reproducible at %d: %v vs %v", i, a[i], b[i])
		}
	}
	c := Ensemble(5, p, 64, 10)
	same := true
	for i := range a {
		if a[i] != c[i] {
			same = false
			break
		}
	}
	if same {
		t.Error("different seed produced an identical ensemble")
	}
}

func TestEnsembleMeanIsPipelinePlusShock(t *testing.T) {
	// pipeline 10 + E[shock]=λ·shape/rate = 3·1/0.5 = 6 -> mean burden ~16.
	out := Ensemble(10, ShockParams{Rate: 3, Shape: 1, JumpRate: 0.5}, 4000, 123)
	if m := meanOf(out); math.Abs(m-16.0) > 0.6 {
		t.Errorf("ensemble mean = %.3f, want ~16.0", m)
	}
}

func TestCalibrateShockRoundTripsMoments(t *testing.T) {
	// gaps mean=5, sample var = 20/3 ≈ 6.667.
	gaps := []float64{2, 4, 6, 8}
	p, ok := CalibrateShock(gaps)
	if !ok {
		t.Fatal("calibration unexpectedly failed")
	}
	wantMean, wantVar := 5.0, 20.0/3.0
	gotMean := p.Rate * p.Shape / p.JumpRate
	gotVar := p.Rate * p.Shape * (p.Shape + 1) / (p.JumpRate * p.JumpRate)
	if math.Abs(gotMean-wantMean) > 1e-9 || math.Abs(gotVar-wantVar) > 1e-9 {
		t.Errorf("implied moments mean=%.4f var=%.4f, want %.4f / %.4f", gotMean, gotVar, wantMean, wantVar)
	}
}

func TestCalibrateShockRejectsDegenerate(t *testing.T) {
	if _, ok := CalibrateShock([]float64{3, 3, 3}); ok {
		t.Error("expected calibration to reject zero-variance gaps")
	}
	if _, ok := CalibrateShock([]float64{1}); ok {
		t.Error("expected calibration to reject too-few gaps")
	}
}
