// Package forecast holds baseline burden forecasters and an expanding-window
// backtest. The baselines are deliberately simple empirical-ensemble models: an
// honest floor that any stochadex model must beat before it earns publication.
//
// Every model emits an ensemble of samples from its predictive distribution,
// scored with CRPS (see internal/scoring), so models are directly comparable and
// the backtest is engine-agnostic.
package forecast

import (
	"sort"

	"github.com/umbralcalc/traffic-forecaster/internal/scoring"
)

// Point is one entity-month observation of the (works) burden series.
type Point struct {
	Year, Month int
	Value       float64
}

// index is a sortable month key (months since year 0).
func (p Point) index() int { return p.Year*12 + (p.Month - 1) }

// Model turns the history strictly before a target month into an ensemble of
// samples for that month. It must not look at or beyond the target (no leakage).
// Returning nil means "abstain" (insufficient history) and the point is skipped.
type Model interface {
	Name() string
	Predict(history []Point, target Point) []float64
}

// ClimatologyMean predicts a single sample: the mean of all prior values. A
// point forecast — the dumb baseline whose CRPS is just mean absolute error.
type ClimatologyMean struct{}

func (ClimatologyMean) Name() string { return "climatology-mean" }
func (ClimatologyMean) Predict(history []Point, _ Point) []float64 {
	if len(history) == 0 {
		return nil
	}
	var sum float64
	for _, p := range history {
		sum += p.Value
	}
	return []float64{sum / float64(len(history))}
}

// RecentWindow predicts the empirical distribution of the last K values
// (persistence plus recent variability).
type RecentWindow struct{ K int }

func (m RecentWindow) Name() string { return "recent-window" }
func (m RecentWindow) Predict(history []Point, _ Point) []float64 {
	if len(history) == 0 {
		return nil
	}
	k := m.K
	if k <= 0 || k > len(history) {
		k = len(history)
	}
	return values(history[len(history)-k:])
}

// Seasonal predicts the empirical distribution of the same calendar month in all
// prior years — capturing the strong month-of-year structure (e.g. the festive
// works embargo). Falls back to a recent window when no same-month history.
type Seasonal struct{ FallbackK int }

func (m Seasonal) Name() string { return "seasonal" }
func (m Seasonal) Predict(history []Point, target Point) []float64 {
	var same []float64
	for _, p := range history {
		if p.Month == target.Month {
			same = append(same, p.Value)
		}
	}
	if len(same) > 0 {
		return same
	}
	return RecentWindow{K: m.FallbackK}.Predict(history, target)
}

// SeasonalRecent unions the same-month history with the last K months, blending
// seasonal shape with current level.
type SeasonalRecent struct{ K int }

func (m SeasonalRecent) Name() string { return "seasonal+recent" }
func (m SeasonalRecent) Predict(history []Point, target Point) []float64 {
	out := Seasonal{FallbackK: m.K}.Predict(history, target)
	out = append(out, RecentWindow{K: m.K}.Predict(history, target)...)
	if len(out) == 0 {
		return nil
	}
	return out
}

func values(ps []Point) []float64 {
	out := make([]float64, len(ps))
	for i, p := range ps {
		out[i] = p.Value
	}
	return out
}

// ModelResult is a model's aggregate backtest performance.
type ModelResult struct {
	Name      string
	Scored    int     // points actually scored
	Abstained int     // points skipped for lack of history
	MeanCRPS  float64 // primary metric, lower better
	CalUnif   float64 // calibration deviation from uniform PIT, lower better
}

// Backtest runs an expanding-window backtest of each model over each entity's
// series. For target month i, models see only points[:i]; a point is scored only
// once at least minHistory prior months exist. Series are not mutated.
func Backtest(series map[string][]Point, models []Model, minHistory int) []ModelResult {
	type acc struct {
		sumCRPS   float64
		scored    int
		abstained int
		cal       *scoring.Calibration
	}
	accs := make([]*acc, len(models))
	for i := range accs {
		accs[i] = &acc{cal: scoring.NewCalibration(10)}
	}

	for _, raw := range series {
		pts := append([]Point(nil), raw...)
		sort.Slice(pts, func(a, b int) bool { return pts[a].index() < pts[b].index() })
		for i := minHistory; i < len(pts); i++ {
			hist := pts[:i]
			target := pts[i]
			for mi, model := range models {
				samples := model.Predict(hist, target)
				if len(samples) == 0 {
					accs[mi].abstained++
					continue
				}
				accs[mi].sumCRPS += scoring.CRPS(samples, target.Value)
				accs[mi].cal.Add(scoring.PIT(samples, target.Value))
				accs[mi].scored++
			}
		}
	}

	out := make([]ModelResult, len(models))
	for i, model := range models {
		a := accs[i]
		mean := 0.0
		if a.scored > 0 {
			mean = a.sumCRPS / float64(a.scored)
		}
		out[i] = ModelResult{
			Name:      model.Name(),
			Scored:    a.scored,
			Abstained: a.abstained,
			MeanCRPS:  mean,
			CalUnif:   a.cal.Uniformity(),
		}
	}
	return out
}
