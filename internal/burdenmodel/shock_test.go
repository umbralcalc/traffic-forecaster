package burdenmodel

import (
	"math"
	"testing"

	"github.com/umbralcalc/stochadex/pkg/simulator"
)

// buildShock builds a single-partition config running only the custom shock
// iteration, used to test it in isolation (and via the harness).
func buildShock(rate, shape, jrate, init float64, steps int, seed uint64, store *simulator.StateTimeStorage) (*simulator.Settings, *simulator.Implementations) {
	gen := simulator.NewConfigGenerator()
	gen.SetPartition(&simulator.PartitionConfig{
		Name:      "emergency",
		Iteration: &CompoundPoissonShockIteration{},
		Params: simulator.NewParams(map[string][]float64{
			"shock_rate": {rate}, "jump_shape": {shape}, "jump_rate": {jrate},
		}),
		InitStateValues:   []float64{init},
		StateHistoryDepth: 1,
	})
	gen.SetSimulation(&simulator.SimulationConfig{
		OutputCondition:      &simulator.EveryStepOutputCondition{},
		OutputFunction:       &simulator.StateTimeStorageOutputFunction{Store: store},
		TerminationCondition: &simulator.NumberOfStepsTerminationCondition{MaxNumberOfSteps: steps},
		TimestepFunction:     &simulator.ConstantTimestepFunction{Stepsize: 1.0},
		InitTimeValue:        0.0,
	})
	gen.SetGlobalSeed(seed)
	return gen.GenerateConfigs()
}

func TestShockZeroRateLeavesStateUnchanged(t *testing.T) {
	store := simulator.NewStateTimeStorage()
	settings, impls := buildShock(0, 1, 1, 5.0, 10, 1, store)
	simulator.NewPartitionCoordinator(settings, impls).Run()
	for i, row := range store.GetValues("emergency") {
		if row[0] != 5.0 {
			t.Fatalf("zero-rate state changed at step %d: %v (want 5.0)", i, row[0])
		}
	}
}

func TestShockIsNonDecreasing(t *testing.T) {
	store := simulator.NewStateTimeStorage()
	settings, impls := buildShock(2, 1.5, 0.5, 0.0, 40, 3, store)
	simulator.NewPartitionCoordinator(settings, impls).Run()
	rows := store.GetValues("emergency")
	for i := 1; i < len(rows); i++ {
		if rows[i][0] < rows[i-1][0] {
			t.Fatalf("shock decreased at step %d: %v < %v", i, rows[i][0], rows[i-1][0])
		}
	}
}

func TestShockMeanIncrementMatchesTheory(t *testing.T) {
	// One step per realisation; E[shock] = λ·shape/rate. λ=3, shape=1, rate=0.5
	// => mean jump 2, mean total 6. (SE over 3000 runs ≈ 0.09, so 0.5 is safe.)
	const realisations = 3000
	var sum float64
	for k := 0; k < realisations; k++ {
		store := simulator.NewStateTimeStorage()
		settings, impls := buildShock(3, 1, 0.5, 0, 1, uint64(100+k), store)
		simulator.NewPartitionCoordinator(settings, impls).Run()
		rows := store.GetValues("emergency")
		sum += rows[len(rows)-1][0]
	}
	if got := sum / realisations; math.Abs(got-6.0) > 0.5 {
		t.Errorf("mean one-step shock = %.3f, want ~6.0", got)
	}
}

func TestShockRunsWithHarnesses(t *testing.T) {
	store := simulator.NewStateTimeStorage()
	settings, impls := buildShock(2, 1.5, 0.5, 1.0, 30, 7, store)
	if err := simulator.RunWithHarnesses(settings, impls); err != nil {
		t.Errorf("test harness failed: %v", err)
	}
}
