// Package burdenmodel is the stochadex configuration for the disruption-burden
// forecast. It composes the burden as planned works (the known forward pipeline,
// injected via a reused ParamValuesIteration) plus an emergency/residual term
// (a compound-Poisson shock), summed by a reused ValuesFunctionIteration, and
// run as a seed-varied ensemble to produce a predictive distribution scored by
// internal/scoring.
package burdenmodel

import (
	"math/rand/v2"

	"github.com/umbralcalc/stochadex/pkg/simulator"
	"gonum.org/v1/gonum/stat/distuv"
)

// CompoundPoissonShockIteration adds, each step, a full compound-Poisson shock to
// the running state: a Poisson(shock_rate * dt) number of jumps, each drawn from
// Gamma(jump_shape, jump_rate).
//
// It differs deliberately from stochadex's continuous.CompoundPoissonProcessIteration,
// which admits at most one jump per step (suited to fine time resolution): this
// draws the entire event count in a single step, so one monthly step yields a
// whole month's emergency burden. The state is non-decreasing (jumps ≥ 0).
//
// Per-dimension params:
//   - "shock_rate": Poisson mean event rate λ (events per unit time)
//   - "jump_shape": Gamma shape k of each jump
//   - "jump_rate":  Gamma rate β of each jump (jump mean = k/β)
type CompoundPoissonShockIteration struct {
	poisson *distuv.Poisson
	gamma   *distuv.Gamma
}

func (c *CompoundPoissonShockIteration) Configure(
	partitionIndex int,
	settings *simulator.Settings,
) {
	seed := settings.Iterations[partitionIndex].Seed
	// Two independent streams (count and jump sizes), both reset here so the
	// iteration is fully reproducible from the seed — as the test harness checks.
	c.poisson = &distuv.Poisson{Lambda: 1.0, Src: rand.NewPCG(seed, seed)}
	c.gamma = &distuv.Gamma{Alpha: 1.0, Beta: 1.0, Src: rand.NewPCG(seed+1, seed+1)}
}

func (c *CompoundPoissonShockIteration) Iterate(
	params *simulator.Params,
	partitionIndex int,
	stateHistories []*simulator.StateHistory,
	timestepsHistory *simulator.CumulativeTimestepsHistory,
) []float64 {
	sh := stateHistories[partitionIndex]
	dt := timestepsHistory.NextIncrement
	out := make([]float64, sh.StateWidth)
	for i := 0; i < sh.StateWidth; i++ {
		prev := sh.Values.At(0, i)
		lambda := params.GetIndex("shock_rate", i) * dt
		shape := params.GetIndex("jump_shape", i)
		rate := params.GetIndex("jump_rate", i)
		var shock float64
		if lambda > 0 && shape > 0 && rate > 0 {
			c.poisson.Lambda = lambda
			c.gamma.Alpha = shape
			c.gamma.Beta = rate
			n := int(c.poisson.Rand())
			for j := 0; j < n; j++ {
				shock += c.gamma.Rand()
			}
		}
		out[i] = prev + shock
	}
	return out
}
