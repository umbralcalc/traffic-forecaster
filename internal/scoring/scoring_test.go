package scoring

import (
	"math"
	"testing"
)

func TestCRPSPointForecast(t *testing.T) {
	// All mass at c: CRPS reduces to |c - y|.
	if got := CRPS([]float64{5, 5, 5}, 3); math.Abs(got-2) > 1e-9 {
		t.Errorf("CRPS = %v, want 2", got)
	}
}

func TestCRPSKnownEnsemble(t *testing.T) {
	// samples {1,3}, y=2: mean|x-y| = 1, 0.5*mean|xi-xj| = 0.5 -> CRPS = 0.5.
	if got := CRPS([]float64{1, 3}, 2); math.Abs(got-0.5) > 1e-9 {
		t.Errorf("CRPS = %v, want 0.5", got)
	}
}

func TestCRPSSharperIsBetterWhenAccurate(t *testing.T) {
	// A tight ensemble near the truth should beat a wide one centred on truth.
	tight := CRPS([]float64{9, 10, 11}, 10)
	wide := CRPS([]float64{0, 10, 20}, 10)
	if !(tight < wide) {
		t.Errorf("expected tight (%v) < wide (%v)", tight, wide)
	}
}

func TestPIT(t *testing.T) {
	if got := PIT([]float64{1, 2, 3, 4}, 2); got != 0.5 {
		t.Errorf("PIT = %v, want 0.5", got)
	}
}

func TestCalibrationUniformity(t *testing.T) {
	// Perfectly uniform PITs -> ~0 uniformity.
	c := NewCalibration(10)
	for i := 0; i < 1000; i++ {
		c.Add(float64(i%10)/10 + 0.05)
	}
	if u := c.Uniformity(); u > 1e-6 {
		t.Errorf("uniform PITs gave uniformity %v, want ~0", u)
	}
	// All PITs in one corner -> large uniformity.
	c2 := NewCalibration(10)
	for i := 0; i < 1000; i++ {
		c2.Add(0.99)
	}
	if c2.Uniformity() < 1 {
		t.Errorf("degenerate PITs gave low uniformity %v", c2.Uniformity())
	}
}
