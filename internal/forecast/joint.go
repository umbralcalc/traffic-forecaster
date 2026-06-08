package forecast

import (
	"hash/fnv"
	"math/rand/v2"
	"sort"

	"github.com/umbralcalc/traffic-forecaster/internal/scoring"
)

// Resample draws n samples with replacement from the given ensemble, so models
// with different ensemble sizes can be combined index-aligned.
func Resample(samples []float64, n int, rng *rand.Rand) []float64 {
	if len(samples) == 0 || n <= 0 {
		return nil
	}
	out := make([]float64, n)
	for i := range out {
		out[i] = samples[rng.IntN(len(samples))]
	}
	return out
}

// HashSeed maps a string to a stable seed, for per-entity RNG streams.
func HashSeed(s string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(s))
	return h.Sum64()
}

// IndependentJoint adapts a per-entity Model into a JointModel that forecasts
// each entity independently (no coupling), resampling each to N index-aligned
// samples with its own RNG stream. Summing across entities then gives the
// correct independent total — the baseline a coupled model must beat on a joint
// metric.
type IndependentJoint struct {
	Model Model
	N     int
	Seed  uint64
}

func (m IndependentJoint) Name() string { return "indep(" + m.Model.Name() + ")" }

func (m IndependentJoint) PredictAll(
	histories map[string][]Point,
	targets map[string]Point,
) map[string][]float64 {
	out := make(map[string][]float64, len(targets))
	for b, t := range targets {
		ens := m.Model.Predict(histories[b], t)
		seed := m.Seed + HashSeed(b)
		out[b] = Resample(ens, m.N, rand.New(rand.NewPCG(seed, seed)))
	}
	return out
}

// JointModel forecasts all entities for a target month together, so a model can
// couple them (e.g. a shared common factor or nearest-neighbour spillover).
// Given each entity's history (strictly before the target month) and the target
// month's known inputs per entity, it returns an ensemble per entity.
type JointModel interface {
	Name() string
	PredictAll(histories map[string][]Point, targets map[string]Point) map[string][]float64
}

// BacktestJoint runs an expanding-window backtest of a joint model. For each
// target month it gathers every entity that has an observation that month and at
// least minHistory prior months, calls PredictAll once, and scores each entity's
// marginal forecast. Aggregates exactly like Backtest so results are comparable.
func BacktestJoint(series map[string][]Point, model JointModel, minHistory int) ModelResult {
	// Sort each entity's series and index points by month.
	sorted := make(map[string][]Point, len(series))
	byMonth := make(map[string]map[int]Point, len(series))
	monthSet := map[int]bool{}
	for entity, raw := range series {
		pts := append([]Point(nil), raw...)
		sort.Slice(pts, func(a, b int) bool { return pts[a].index() < pts[b].index() })
		sorted[entity] = pts
		m := make(map[int]Point, len(pts))
		for _, p := range pts {
			m[p.index()] = p
			monthSet[p.index()] = true
		}
		byMonth[entity] = m
	}
	months := make([]int, 0, len(monthSet))
	for m := range monthSet {
		months = append(months, m)
	}
	sort.Ints(months)

	cal := scoring.NewCalibration(10)
	var sumCRPS float64
	var scored int

	for _, m := range months {
		histories := map[string][]Point{}
		targets := map[string]Point{}
		for entity, pts := range sorted {
			target, ok := byMonth[entity][m]
			if !ok {
				continue
			}
			// History = this entity's points strictly before m; require minHistory.
			hist := make([]Point, 0, len(pts))
			for _, p := range pts {
				if p.index() < m {
					hist = append(hist, p)
				}
			}
			if len(hist) < minHistory {
				continue
			}
			histories[entity] = hist
			targets[entity] = target
		}
		if len(targets) == 0 {
			continue
		}
		ensembles := model.PredictAll(histories, targets)
		for entity, target := range targets {
			samples := ensembles[entity]
			if len(samples) == 0 {
				continue
			}
			sumCRPS += scoring.CRPS(samples, target.Value)
			cal.Add(scoring.PIT(samples, target.Value))
			scored++
		}
	}

	mean := 0.0
	if scored > 0 {
		mean = sumCRPS / float64(scored)
	}
	return ModelResult{
		Name:     model.Name(),
		Scored:   scored,
		MeanCRPS: mean,
		CalUnif:  cal.Uniformity(),
		PITBins:  cal.Bins(),
	}
}

// BacktestJointTotal scores a joint model on the London-wide TOTAL burden (the
// sum over boroughs) each month — the metric where cross-borough coupling pays
// off, because independent models underestimate the variance of the sum. It
// relies on PredictAll returning index-aligned ensembles (same index = same
// joint realisation), summing them index-wise into a total ensemble.
func BacktestJointTotal(series map[string][]Point, model JointModel, minHistory int) ModelResult {
	sorted := make(map[string][]Point, len(series))
	byMonth := make(map[string]map[int]Point, len(series))
	monthSet := map[int]bool{}
	for entity, raw := range series {
		pts := append([]Point(nil), raw...)
		sort.Slice(pts, func(a, b int) bool { return pts[a].index() < pts[b].index() })
		sorted[entity] = pts
		mm := make(map[int]Point, len(pts))
		for _, p := range pts {
			mm[p.index()] = p
			monthSet[p.index()] = true
		}
		byMonth[entity] = mm
	}
	months := make([]int, 0, len(monthSet))
	for mth := range monthSet {
		months = append(months, mth)
	}
	sort.Ints(months)

	cal := scoring.NewCalibration(10)
	var sumCRPS float64
	var scored int

	for _, mth := range months {
		histories := map[string][]Point{}
		targets := map[string]Point{}
		for entity, pts := range sorted {
			target, ok := byMonth[entity][mth]
			if !ok {
				continue
			}
			hist := make([]Point, 0, len(pts))
			for _, p := range pts {
				if p.index() < mth {
					hist = append(hist, p)
				}
			}
			if len(hist) < minHistory {
				continue
			}
			histories[entity] = hist
			targets[entity] = target
		}
		if len(targets) == 0 {
			continue
		}
		ensembles := model.PredictAll(histories, targets)

		// Common ensemble width N = the modal aligned length.
		n := 0
		for _, e := range ensembles {
			if len(e) > n {
				n = len(e)
			}
		}
		if n == 0 {
			continue
		}
		total := make([]float64, n)
		var actual float64
		var used int
		for b, target := range targets {
			e := ensembles[b]
			if len(e) != n {
				continue // skip boroughs not aligned to the common width
			}
			for k := 0; k < n; k++ {
				total[k] += e[k]
			}
			actual += target.Value
			used++
		}
		if used == 0 {
			continue
		}
		sumCRPS += scoring.CRPS(total, actual)
		cal.Add(scoring.PIT(total, actual))
		scored++
	}

	mean := 0.0
	if scored > 0 {
		mean = sumCRPS / float64(scored)
	}
	return ModelResult{
		Name:     model.Name() + " [LONDON TOTAL]",
		Scored:   scored,
		MeanCRPS: mean,
		CalUnif:  cal.Uniformity(),
		PITBins:  cal.Bins(),
	}
}
