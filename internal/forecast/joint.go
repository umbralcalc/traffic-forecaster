package forecast

import (
	"sort"

	"github.com/umbralcalc/traffic-forecaster/internal/scoring"
)

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
