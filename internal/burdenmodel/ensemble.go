package burdenmodel

import (
	"github.com/umbralcalc/stochadex/pkg/general"
	"github.com/umbralcalc/stochadex/pkg/simulator"
)

// ShockParams calibrates the emergency/residual compound-Poisson shock.
type ShockParams struct {
	Rate     float64 // Poisson mean event count per month (λ)
	Shape    float64 // Gamma shape of each jump
	JumpRate float64 // Gamma rate of each jump
}

// CalibrateShock fits a compound-Poisson-with-Gamma-jumps shock to the mean and
// variance of the supplied monthly residual gaps (realised − pipeline), using
// exponential jumps (shape = 1) so the two free parameters are identified by the
// two moments:
//
//	E   = λ/β       Var = 2λ/β²   ⇒   β = 2E/Var,  λ = 2E²/Var.
//
// Returns ok=false when the gaps are too few or degenerate (non-positive mean or
// variance), so the caller can fall back.
func CalibrateShock(gaps []float64) (ShockParams, bool) {
	if len(gaps) < 2 {
		return ShockParams{}, false
	}
	var sum float64
	for _, g := range gaps {
		sum += g
	}
	mean := sum / float64(len(gaps))
	var ss float64
	for _, g := range gaps {
		d := g - mean
		ss += d * d
	}
	variance := ss / float64(len(gaps)-1)
	if mean <= 0 || variance <= 0 {
		return ShockParams{}, false
	}
	return ShockParams{
		Rate:     2 * mean * mean / variance,
		Shape:    1.0,
		JumpRate: 2 * mean / variance,
	}, true
}

// Ensemble runs n independent one-month realisations of the burden model and
// returns the simulated total burden of each. The configuration composes three
// partitions: a reused ParamValuesIteration holding the known planned pipeline,
// the custom CompoundPoissonShockIteration for the emergency term, and a reused
// ValuesFunctionIteration summing them (planned + emergency) into total burden.
// Each realisation re-seeds from baseSeed+k so the ensemble samples the shock.
func Ensemble(pipeline float64, p ShockParams, n int, baseSeed uint64) []float64 {
	out := make([]float64, n)
	sum := general.NewTransformReduceFunction(general.ParamsTransform, general.SumReduce)
	for k := 0; k < n; k++ {
		store := simulator.NewStateTimeStorage()
		gen := simulator.NewConfigGenerator()
		gen.SetPartition(&simulator.PartitionConfig{
			Name:              "planned",
			Iteration:         &general.ParamValuesIteration{},
			Params:            simulator.NewParams(map[string][]float64{"param_values": {pipeline}}),
			InitStateValues:   []float64{pipeline},
			StateHistoryDepth: 1,
		})
		gen.SetPartition(&simulator.PartitionConfig{
			Name:      "emergency",
			Iteration: &CompoundPoissonShockIteration{},
			Params: simulator.NewParams(map[string][]float64{
				"shock_rate": {p.Rate},
				"jump_shape": {p.Shape},
				"jump_rate":  {p.JumpRate},
			}),
			InitStateValues:   []float64{0.0},
			StateHistoryDepth: 1,
			// Seed the only stochastic partition explicitly. We avoid
			// SetGlobalSeed: it derives per-partition seeds by iterating a map,
			// so Go's randomised map order makes it non-reproducible.
			Seed: baseSeed + uint64(k),
		})
		gen.SetPartition(&simulator.PartitionConfig{
			Name:            "burden",
			Iteration:       &general.ValuesFunctionIteration{Function: sum},
			InitStateValues: []float64{0.0},
			ParamsFromUpstream: map[string]simulator.NamedUpstreamConfig{
				"planned":   {Upstream: "planned"},
				"emergency": {Upstream: "emergency"},
			},
			StateHistoryDepth: 1,
		})
		gen.SetSimulation(&simulator.SimulationConfig{
			OutputCondition: &simulator.EveryStepOutputCondition{},
			OutputFunction:  &simulator.StateTimeStorageOutputFunction{Store: store},
			TerminationCondition: &simulator.NumberOfStepsTerminationCondition{
				MaxNumberOfSteps: 1, // one step = one month (full Poisson count)
			},
			TimestepFunction: &simulator.ConstantTimestepFunction{Stepsize: 1.0},
			InitTimeValue:    0.0,
		})

		settings, implementations := gen.GenerateConfigs()
		simulator.NewPartitionCoordinator(settings, implementations).Run()

		series := store.GetValues("burden")
		out[k] = series[len(series)-1][0]
	}
	return out
}
