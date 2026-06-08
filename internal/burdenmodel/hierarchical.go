package burdenmodel

import (
	"math"
	"math/rand/v2"
	"sort"

	"github.com/umbralcalc/stochadex/pkg/simulator"
	"github.com/umbralcalc/traffic-forecaster/internal/forecast"
	"gonum.org/v1/gonum/stat/distuv"
)

// HierarchicalEmergencyIteration computes, in one vectorised step, the emergency
// burden of every borough at once (state width = number of boroughs). Each
// borough's value is its known pipeline plus a baseline level, a loading on a
// single shared London-wide factor F (drawn once per step, so it couples all
// boroughs), and an idiosyncratic shock:
//
//	out_i = max(0, pipeline_i + baseline_i + loading_i*F + eps_i),
//	F ~ N(0, common_sigma),  eps_i ~ N(0, sigma_i).
//
// The shared F is the common-factor coupling. (Stage 2 adds a nearest-neighbour
// spill term to this same iteration.) Per-dimension params: "pipeline",
// "baseline", "loading", "sigma"; scalar param "common_sigma" at index 0.
type HierarchicalEmergencyIteration struct {
	normal *distuv.Normal
}

func (h *HierarchicalEmergencyIteration) Configure(
	partitionIndex int,
	settings *simulator.Settings,
) {
	seed := settings.Iterations[partitionIndex].Seed
	h.normal = &distuv.Normal{Mu: 0, Sigma: 1, Src: rand.NewPCG(seed, seed)}
}

func (h *HierarchicalEmergencyIteration) Iterate(
	params *simulator.Params,
	partitionIndex int,
	stateHistories []*simulator.StateHistory,
	timestepsHistory *simulator.CumulativeTimestepsHistory,
) []float64 {
	n := stateHistories[partitionIndex].StateWidth
	f := h.normal.Rand() * params.GetIndex("common_sigma", 0) // one shared draw
	out := make([]float64, n)
	for i := 0; i < n; i++ {
		eps := h.normal.Rand() * params.GetIndex("sigma", i)
		v := params.GetIndex("pipeline", i) + params.GetIndex("baseline", i) +
			params.GetIndex("loading", i)*f + eps
		if v < 0 {
			v = 0
		}
		out[i] = v
	}
	return out
}

// JointEnsemble runs n independent one-month realisations of the hierarchical
// model and returns each realisation's per-borough burden vector. Boroughs in
// the same realisation share the factor draw F, which induces the cross-borough
// correlation the common factor represents.
func JointEnsemble(
	pipeline, baseline, loading, sigma []float64,
	commonSigma float64, n int, baseSeed uint64,
) [][]float64 {
	runs := make([][]float64, n)
	width := len(pipeline)
	for k := 0; k < n; k++ {
		store := simulator.NewStateTimeStorage()
		gen := simulator.NewConfigGenerator()
		gen.SetPartition(&simulator.PartitionConfig{
			Name:      "emergency",
			Iteration: &HierarchicalEmergencyIteration{},
			Params: simulator.NewParams(map[string][]float64{
				"pipeline":     pipeline,
				"baseline":     baseline,
				"loading":      loading,
				"sigma":        sigma,
				"common_sigma": {commonSigma},
			}),
			InitStateValues:   make([]float64, width),
			StateHistoryDepth: 1,
			Seed:              baseSeed + uint64(k),
		})
		gen.SetSimulation(&simulator.SimulationConfig{
			OutputCondition:      &simulator.EveryStepOutputCondition{},
			OutputFunction:       &simulator.StateTimeStorageOutputFunction{Store: store},
			TerminationCondition: &simulator.NumberOfStepsTerminationCondition{MaxNumberOfSteps: 1},
			TimestepFunction:     &simulator.ConstantTimestepFunction{Stepsize: 1.0},
			InitTimeValue:        0.0,
		})
		settings, impls := gen.GenerateConfigs()
		simulator.NewPartitionCoordinator(settings, impls).Run()
		series := store.GetValues("emergency")
		runs[k] = series[len(series)-1]
	}
	return runs
}

// CommonFactorModel is a forecast.JointModel: it calibrates a shared London-wide
// emergency factor (plus per-borough loadings and idiosyncratic spread) from the
// trailing window of residuals, then forecasts all boroughs jointly via the
// stochadex hierarchical simulation. Boroughs without a usable pipeline or
// enough history fall back to the per-entity SeasonalRecent baseline.
type CommonFactorModel struct {
	ResidualK int
	N         int
	FallbackK int
	Seed      uint64
}

func (CommonFactorModel) Name() string { return "hier-common" }

func (m CommonFactorModel) PredictAll(
	histories map[string][]forecast.Point,
	targets map[string]forecast.Point,
) map[string][]float64 {
	out := make(map[string][]float64, len(targets))
	n := m.N
	if n <= 0 {
		n = 100
	}

	// Per-borough residual (realised − pipeline) indexed by month, for boroughs
	// with a usable target pipeline and enough pipelined history.
	resid := map[string]map[int]float64{}
	var modeled []string
	for b, hist := range histories {
		t := targets[b]
		if t.Pipeline <= 0 {
			continue
		}
		rm := map[int]float64{}
		for _, p := range hist {
			if p.Pipeline > 0 {
				rm[p.Year*12+p.Month-1] = p.Value - p.Pipeline
			}
		}
		if len(rm) >= 6 {
			resid[b] = rm
			modeled = append(modeled, b)
		}
	}
	sort.Strings(modeled)

	// Months present in every modeled borough, trailing ResidualK.
	months := alignedMonths(resid, modeled)
	if m.ResidualK > 0 && len(months) > m.ResidualK {
		months = months[len(months)-m.ResidualK:]
	}

	if len(modeled) >= 3 && len(months) >= 4 {
		pipeline, baseline, loading, sigma, commonSigma := calibrateCommonFactor(resid, modeled, months, targets)
		seed := m.Seed + uint64(months[len(months)-1]+1)
		runs := JointEnsemble(pipeline, baseline, loading, sigma, commonSigma, n, seed)
		for i, b := range modeled {
			col := make([]float64, len(runs))
			for k := range runs {
				col[k] = runs[k][i]
			}
			out[b] = col
		}
	}

	// Everything not modelled (no pipeline, thin history, or uncalibrated) falls
	// back to the per-entity baseline, resampled to N so all boroughs are aligned
	// (the independent draw being correct for an uncoupled borough).
	for b := range targets {
		if _, done := out[b]; done {
			continue
		}
		ens := forecast.SeasonalRecent{K: m.FallbackK}.Predict(histories[b], targets[b])
		seed := m.Seed + forecast.HashSeed(b)
		out[b] = forecast.Resample(ens, n, rand.New(rand.NewPCG(seed, seed)))
	}
	return out
}

// calibrateCommonFactor decomposes the residual matrix into a per-borough
// baseline, a single common factor L(t) = cross-borough mean deviation, each
// borough's loading on it, and the idiosyncratic spread. Returns parameter
// vectors aligned to modeled, with pipeline taken from the target month.
func calibrateCommonFactor(
	resid map[string]map[int]float64,
	modeled []string,
	months []int,
	targets map[string]forecast.Point,
) (pipeline, baseline, loading, sigma []float64, commonSigma float64) {
	demeaned := make(map[string][]float64, len(modeled))
	baseline = make([]float64, len(modeled))
	pipeline = make([]float64, len(modeled))
	for bi, b := range modeled {
		xs := make([]float64, len(months))
		for j, t := range months {
			xs[j] = resid[b][t]
		}
		mu := mean(xs)
		baseline[bi] = mu
		pipeline[bi] = targets[b].Pipeline
		d := make([]float64, len(months))
		for j := range xs {
			d[j] = xs[j] - mu
		}
		demeaned[b] = d
	}
	// Common factor = mean demeaned residual across boroughs each month.
	common := make([]float64, len(months))
	for j := range months {
		var s float64
		for _, b := range modeled {
			s += demeaned[b][j]
		}
		common[j] = s / float64(len(modeled))
	}
	varCommon := pvariance(common)
	commonSigma = math.Sqrt(varCommon)
	loading = make([]float64, len(modeled))
	sigma = make([]float64, len(modeled))
	for bi, b := range modeled {
		if varCommon > 0 {
			loading[bi] = covariance(demeaned[b], common) / varCommon
		}
		e := make([]float64, len(months))
		for j := range common {
			e[j] = demeaned[b][j] - loading[bi]*common[j]
		}
		sigma[bi] = math.Sqrt(pvariance(e))
	}
	return pipeline, baseline, loading, sigma, commonSigma
}

func alignedMonths(resid map[string]map[int]float64, modeled []string) []int {
	count := map[int]int{}
	for _, b := range modeled {
		for t := range resid[b] {
			count[t]++
		}
	}
	var months []int
	for t, c := range count {
		if c == len(modeled) {
			months = append(months, t)
		}
	}
	sort.Ints(months)
	return months
}

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var s float64
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

func pvariance(xs []float64) float64 {
	if len(xs) < 2 {
		return 0
	}
	mu := mean(xs)
	var ss float64
	for _, x := range xs {
		ss += (x - mu) * (x - mu)
	}
	return ss / float64(len(xs))
}

func covariance(a, b []float64) float64 {
	if len(a) != len(b) || len(a) < 2 {
		return 0
	}
	ma, mb := mean(a), mean(b)
	var s float64
	for i := range a {
		s += (a[i] - ma) * (b[i] - mb)
	}
	return s / float64(len(a))
}
