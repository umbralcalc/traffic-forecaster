package burdenmodel

import (
	"math"
	"testing"

	"github.com/umbralcalc/stochadex/pkg/simulator"
	"github.com/umbralcalc/traffic-forecaster/internal/forecast"
)

func TestAdjacencyIsSymmetric(t *testing.T) {
	for a, nbrs := range boroughAdjacency {
		for _, b := range nbrs {
			found := false
			for _, x := range boroughAdjacency[b] {
				if x == a {
					found = true
				}
			}
			if !found {
				t.Errorf("adjacency not symmetric: %q lists %q but not vice versa", a, b)
			}
		}
	}
}

func TestWeightMatrixRowsNormalised(t *testing.T) {
	modeled := []string{"Camden", "Westminster", "Islington", "City of London"}
	w := weightMatrix(modeled)
	n := len(modeled)
	for i := 0; i < n; i++ {
		var sum float64
		for j := 0; j < n; j++ {
			sum += w[i*n+j]
		}
		if math.Abs(sum-1) > 1e-9 { // all four are mutually connected, so each has neighbours
			t.Errorf("row %d (%s) sums to %v, want 1", i, modeled[i], sum)
		}
	}
}

// buildHierSpatial runs the iteration with a spatial matrix param (for the harness).
func buildHierSpatial(store *simulator.StateTimeStorage) (*simulator.Settings, *simulator.Implementations) {
	gen := simulator.NewConfigGenerator()
	gen.SetPartition(&simulator.PartitionConfig{
		Name:      "emergency",
		Iteration: &HierarchicalEmergencyIteration{},
		Params: simulator.NewParams(map[string][]float64{
			"pipeline": {5, 8}, "baseline": {2, 3}, "loading": {1, 0.5},
			"sigma": {0.5, 0.4}, "common_sigma": {1.0},
			"spatial_matrix": {1.2, 0.3, 0.3, 1.2},
		}),
		InitStateValues:   []float64{0, 0},
		StateHistoryDepth: 1,
		Seed:              5,
	})
	gen.SetSimulation(&simulator.SimulationConfig{
		OutputCondition:      &simulator.EveryStepOutputCondition{},
		OutputFunction:       &simulator.StateTimeStorageOutputFunction{Store: store},
		TerminationCondition: &simulator.NumberOfStepsTerminationCondition{MaxNumberOfSteps: 20},
		TimestepFunction:     &simulator.ConstantTimestepFunction{Stepsize: 1.0},
		InitTimeValue:        0.0,
	})
	return gen.GenerateConfigs()
}

func TestHierSpatialRunsWithHarnesses(t *testing.T) {
	store := simulator.NewStateTimeStorage()
	settings, impls := buildHierSpatial(store)
	if err := simulator.RunWithHarnesses(settings, impls); err != nil {
		t.Errorf("test harness failed: %v", err)
	}
}

func TestSpatialMatrixCouplesNeighbours(t *testing.T) {
	// M for two coupled boroughs (spill 0.5 on a 2-cycle): inv(I-0.5W).
	coupled := []float64{1.3333, 0.6667, 0.6667, 1.3333}
	identity := []float64{1, 0, 0, 1}
	base := []float64{10, 10}

	cRuns := JointEnsembleSpatial(base, []float64{0, 0}, []float64{0, 0}, []float64{1, 1}, 0, coupled, 600, 7)
	iRuns := JointEnsembleSpatial(base, []float64{0, 0}, []float64{0, 0}, []float64{1, 1}, 0, identity, 600, 7)

	col := func(runs [][]float64, c int) []float64 {
		out := make([]float64, len(runs))
		for k := range runs {
			out[k] = runs[k][c]
		}
		return out
	}
	cc := corr(col(cRuns, 0), col(cRuns, 1))
	ic := corr(col(iRuns, 0), col(iRuns, 1))
	if cc < 0.5 {
		t.Errorf("coupled neighbour correlation = %.3f, want > 0.5", cc)
	}
	if ic > 0.2 {
		t.Errorf("identity (uncoupled) correlation = %.3f, want ~0", ic)
	}
}

func TestSpatialFactorModelShapeAndFallback(t *testing.T) {
	m := SpatialFactorModel{ResidualK: 18, N: 32, FallbackK: 6, Seed: 1}
	histories := map[string][]forecast.Point{}
	targets := map[string]forecast.Point{}
	// Use real adjacent boroughs so the spatial graph has edges.
	for _, b := range []string{"Camden", "Westminster", "Islington", "City of London"} {
		var pts []forecast.Point
		for mth := 1; mth <= 12; mth++ {
			pts = append(pts, forecast.Point{Year: 2025, Month: mth, Value: 20 + float64(mth), Pipeline: 12})
		}
		histories[b] = pts
		targets[b] = forecast.Point{Year: 2026, Month: 1, Pipeline: 12}
	}
	histories["Zzz"] = []forecast.Point{{Year: 2025, Month: 1, Value: 5}}
	targets["Zzz"] = forecast.Point{Year: 2026, Month: 1, Pipeline: 0}

	out := m.PredictAll(histories, targets)
	for _, b := range []string{"Camden", "Westminster", "Islington", "City of London"} {
		if len(out[b]) != m.N {
			t.Errorf("borough %s ensemble = %d, want %d", b, len(out[b]), m.N)
		}
	}
	if len(out["Zzz"]) == 0 {
		t.Error("borough Zzz (no pipeline) should fall back, got empty")
	}
}
