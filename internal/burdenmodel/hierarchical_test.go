package burdenmodel

import (
	"math"
	"testing"

	"github.com/umbralcalc/stochadex/pkg/simulator"
	"github.com/umbralcalc/traffic-forecaster/internal/forecast"
)

func buildHier(pipeline, baseline, loading, sigma []float64, commonSigma float64, steps int, seed uint64, store *simulator.StateTimeStorage) (*simulator.Settings, *simulator.Implementations) {
	gen := simulator.NewConfigGenerator()
	gen.SetPartition(&simulator.PartitionConfig{
		Name:      "emergency",
		Iteration: &HierarchicalEmergencyIteration{},
		Params: simulator.NewParams(map[string][]float64{
			"pipeline": pipeline, "baseline": baseline, "loading": loading,
			"sigma": sigma, "common_sigma": {commonSigma},
		}),
		InitStateValues:   make([]float64, len(pipeline)),
		StateHistoryDepth: 1,
		Seed:              seed,
	})
	gen.SetSimulation(&simulator.SimulationConfig{
		OutputCondition:      &simulator.EveryStepOutputCondition{},
		OutputFunction:       &simulator.StateTimeStorageOutputFunction{Store: store},
		TerminationCondition: &simulator.NumberOfStepsTerminationCondition{MaxNumberOfSteps: steps},
		TimestepFunction:     &simulator.ConstantTimestepFunction{Stepsize: 1.0},
		InitTimeValue:        0.0,
	})
	return gen.GenerateConfigs()
}

func TestHierIsPipelinePlusBaselineWithoutNoise(t *testing.T) {
	store := simulator.NewStateTimeStorage()
	settings, impls := buildHier([]float64{5, 10}, []float64{1, 2}, []float64{0, 0}, []float64{0, 0}, 0, 1, 3, store)
	simulator.NewPartitionCoordinator(settings, impls).Run()
	last := store.GetValues("emergency")
	row := last[len(last)-1]
	if math.Abs(row[0]-6) > 1e-9 || math.Abs(row[1]-12) > 1e-9 {
		t.Fatalf("noiseless output = %v, want [6 12]", row)
	}
}

func TestHierRunsWithHarnesses(t *testing.T) {
	store := simulator.NewStateTimeStorage()
	settings, impls := buildHier([]float64{5, 8, 3}, []float64{1, 2, 1}, []float64{1, 0.5, 1.2}, []float64{0.5, 0.3, 0.4}, 1.0, 20, 9, store)
	if err := simulator.RunWithHarnesses(settings, impls); err != nil {
		t.Errorf("test harness failed: %v", err)
	}
}

func corr(a, b []float64) float64 {
	ma, mb := mean(a), mean(b)
	var num, da, db float64
	for i := range a {
		num += (a[i] - ma) * (b[i] - mb)
		da += (a[i] - ma) * (a[i] - ma)
		db += (b[i] - mb) * (b[i] - mb)
	}
	if da == 0 || db == 0 {
		return 0
	}
	return num / math.Sqrt(da*db)
}

func TestJointEnsembleSharedFactorCorrelates(t *testing.T) {
	// Two boroughs both load on the common factor with small idiosyncratic noise
	// => their ensembles should be strongly positively correlated.
	runs := JointEnsemble([]float64{0, 0}, []float64{0, 0}, []float64{1, 1}, []float64{0.1, 0.1}, 3.0, 500, 42)
	b0 := make([]float64, len(runs))
	b1 := make([]float64, len(runs))
	for k := range runs {
		b0[k] = runs[k][0]
		b1[k] = runs[k][1]
	}
	if c := corr(b0, b1); c < 0.7 {
		t.Errorf("shared-factor correlation = %.3f, want > 0.7", c)
	}
}

func meanCol(runs [][]float64, c int) float64 {
	var s float64
	for _, r := range runs {
		s += r[c]
	}
	return s / float64(len(runs))
}

func TestDirectEnsembleMatchesStochadex(t *testing.T) {
	// width 4 (<= directThreshold) goes through the stochadex simulator; the
	// direct sampler must reproduce the same per-dimension means.
	pipeline := []float64{5, 8, 3, 6}
	baseline := []float64{1, 2, 1, 2}
	loading := []float64{1, 0.5, 1.2, 0.8}
	sigma := []float64{0.5, 0.3, 0.4, 0.6}
	st := JointEnsemble(pipeline, baseline, loading, sigma, 1.0, 5000, 1)
	di := jointEnsembleDirect(pipeline, baseline, loading, sigma, 1.0, nil, 5000, 1)
	for i := range pipeline {
		if d := math.Abs(meanCol(st, i) - meanCol(di, i)); d > 0.3 {
			t.Errorf("dim %d: stochadex vs direct mean differ by %.3f", i, d)
		}
	}
}

func TestDirectEnsembleZeroNoise(t *testing.T) {
	runs := jointEnsembleDirect([]float64{5, 10}, []float64{1, 2}, []float64{0, 0}, []float64{0, 0}, 0, nil, 8, 1)
	for _, r := range runs {
		if r[0] != 6 || r[1] != 12 {
			t.Fatalf("noiseless direct = %v, want [6 12]", r)
		}
	}
}

func TestCommonFactorModelShapeAndFallback(t *testing.T) {
	m := CommonFactorModel{ResidualK: 18, N: 32, FallbackK: 6, Seed: 1}
	histories := map[string][]forecast.Point{}
	targets := map[string]forecast.Point{}
	// Three modelled boroughs with pipelined history; values vary by a shared trend.
	for _, b := range []string{"A", "B", "C"} {
		var pts []forecast.Point
		for mth := 1; mth <= 12; mth++ {
			pts = append(pts, forecast.Point{Year: 2025, Month: mth, Value: 20 + float64(mth), Pipeline: 12})
		}
		histories[b] = pts
		targets[b] = forecast.Point{Year: 2026, Month: 1, Pipeline: 12}
	}
	// One borough with no pipeline -> must fall back, not crash.
	histories["D"] = []forecast.Point{{Year: 2025, Month: 1, Value: 5}, {Year: 2025, Month: 2, Value: 6}}
	targets["D"] = forecast.Point{Year: 2026, Month: 1, Pipeline: 0}

	out := m.PredictAll(histories, targets)
	for _, b := range []string{"A", "B", "C"} {
		if len(out[b]) != m.N {
			t.Errorf("borough %s ensemble = %d, want %d", b, len(out[b]), m.N)
		}
	}
	if len(out["D"]) == 0 {
		t.Error("borough D (no pipeline) should fall back, got empty")
	}
}
