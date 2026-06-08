package burdenmodel

import (
	"testing"

	"github.com/umbralcalc/traffic-forecaster/internal/forecast"
)

func TestStochadexBurdenFallsBackWithoutPipeline(t *testing.T) {
	m := StochadexBurden{ResidualK: 18, N: 16, FallbackK: 6}
	hist := []forecast.Point{
		{Year: 2025, Month: 1, Value: 10}, {Year: 2025, Month: 2, Value: 12},
		{Year: 2025, Month: 3, Value: 11}, {Year: 2025, Month: 4, Value: 13},
	}
	got := m.Predict(hist, forecast.Point{Year: 2025, Month: 5, Pipeline: 0})
	if len(got) == 0 {
		t.Fatal("expected fallback ensemble, got abstain")
	}
}

func TestStochadexBurdenEnsembleShape(t *testing.T) {
	m := StochadexBurden{ResidualK: 18, N: 32, FallbackK: 6, Seed: 1}
	// History with pipeline and positive residual gaps to calibrate from.
	hist := []forecast.Point{
		{Year: 2025, Month: 1, Value: 18, Pipeline: 10},
		{Year: 2025, Month: 2, Value: 16, Pipeline: 10},
		{Year: 2025, Month: 3, Value: 20, Pipeline: 12},
		{Year: 2025, Month: 4, Value: 15, Pipeline: 11},
	}
	target := forecast.Point{Year: 2025, Month: 6, Pipeline: 12}
	got := m.Predict(hist, target)
	if len(got) != m.N {
		t.Fatalf("ensemble size = %d, want %d", len(got), m.N)
	}
	// Burden = pipeline + non-negative shock, so never below the pipeline.
	for i, v := range got {
		if v < target.Pipeline-1e-9 {
			t.Errorf("realisation %d = %v below pipeline %v", i, v, target.Pipeline)
		}
	}
}
