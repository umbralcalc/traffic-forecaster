package burdenmodel

import (
	"testing"

	"github.com/umbralcalc/traffic-forecaster/internal/forecast"
)

// nestedHist builds 12 months of history for a unit with Pipeline=10 and the
// given residual pattern (Value = 10 + residual[t mod len]).
func nestedHist(resid []float64) []forecast.Point {
	var pts []forecast.Point
	for mth := 1; mth <= 12; mth++ {
		r := resid[(mth-1)%len(resid)]
		pts = append(pts, forecast.Point{Year: 2025, Month: mth, Value: 10 + r, Pipeline: 10})
	}
	return pts
}

func TestNestedWithinBoroughCorrelation(t *testing.T) {
	// A1, A2 share a borough signal; B1 is a different borough. The nested model
	// should make A1,A2 co-move more than A1,B1 (shared borough factor B_A).
	sA := []float64{3, -3, 3, -3, 2, -2}
	bB := []float64{2, 2, -2, -2, 1, -1}
	histories := map[string][]forecast.Point{
		"A1": nestedHist(sA), "A2": nestedHist(sA), "B1": nestedHist(bB),
	}
	targets := map[string]forecast.Point{
		"A1": {Year: 2026, Month: 1, Pipeline: 10},
		"A2": {Year: 2026, Month: 1, Pipeline: 10},
		"B1": {Year: 2026, Month: 1, Pipeline: 10},
	}
	m := NestedFactorModel{ResidualK: 18, N: 800, FallbackK: 6, Seed: 1,
		Group: map[string]string{"A1": "A", "A2": "A", "B1": "B"}}
	out := m.PredictAll(histories, targets)
	if len(out["A1"]) != m.N || len(out["B1"]) != m.N {
		t.Fatalf("ensemble sizes: A1=%d B1=%d, want %d", len(out["A1"]), len(out["B1"]), m.N)
	}
	withinA := corr(out["A1"], out["A2"])
	crossAB := corr(out["A1"], out["B1"])
	if !(withinA > crossAB) {
		t.Errorf("within-borough corr %.3f should exceed cross-borough %.3f", withinA, crossAB)
	}
}

func TestNestedFallbackWithoutPipeline(t *testing.T) {
	m := NestedFactorModel{ResidualK: 18, N: 16, FallbackK: 6, Seed: 1,
		Group: map[string]string{"X": "A"}}
	hist := map[string][]forecast.Point{
		"X": {{Year: 2025, Month: 1, Value: 5}, {Year: 2025, Month: 2, Value: 6}},
	}
	targets := map[string]forecast.Point{"X": {Year: 2026, Month: 1, Pipeline: 0}}
	if got := m.PredictAll(hist, targets)["X"]; len(got) == 0 {
		t.Error("expected fallback ensemble for no-pipeline unit")
	}
}
