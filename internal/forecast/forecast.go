// Package forecast holds baseline burden forecasters and an expanding-window
// backtest. The baselines are deliberately simple empirical-ensemble models: an
// honest floor that any stochadex model must beat before it earns publication.
//
// Every model emits an ensemble of samples from its predictive distribution,
// scored with CRPS (see internal/scoring), so models are directly comparable and
// the backtest is engine-agnostic.
package forecast

import (
	"fmt"
	"sort"

	"github.com/umbralcalc/traffic-forecaster/internal/scoring"
)

// Point is one entity-month observation of the (works) burden series.
//
//   - Value     realised total burden (the thing we score against).
//   - Pipeline  forward planned-works estimate for the month, vintaged so it is
//     known at forecast time — a model may use the TARGET's Pipeline.
//   - Planned   realised planned-works burden (known only for history).
//   - Emergency realised emergency-works burden (known only for history).
//
// Value ≈ Planned + Emergency. Models must read Planned/Emergency only from
// history, never from the target (they are unknown at forecast time).
type Point struct {
	Year, Month int
	Value       float64
	Pipeline    float64
	Planned     float64
	Emergency   float64
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

// PipelineResidual centres the forecast on the target month's known permit
// pipeline and adds the empirical distribution of past (realised - pipeline)
// residuals — which capture the emergency works, overruns, and late filings the
// forward pipeline cannot see. This is where the forward signal earns its keep.
//
// ResidualK limits the residuals to the most recent K months that had a pipeline
// (0 = all history). Burden trends down over the series, so an all-history
// residual over-predicts the now-lower months; a trailing window tracks the
// level and calibrates far better. Falls back to SeasonalRecent when no pipeline
// is known for the target.
type PipelineResidual struct{ FallbackK, ResidualK int }

func (m PipelineResidual) Name() string {
	if m.ResidualK > 0 {
		return fmt.Sprintf("pipeline+res(%dm)", m.ResidualK)
	}
	return "pipeline+res(all)"
}
func (m PipelineResidual) Predict(history []Point, target Point) []float64 {
	if target.Pipeline <= 0 {
		return SeasonalRecent{K: m.FallbackK}.Predict(history, target)
	}
	gaps := make([]float64, 0, len(history))
	for _, h := range history {
		if h.Pipeline <= 0 {
			continue // skip left-censored months: their "residual" is the whole burden
		}
		gaps = append(gaps, h.Value-h.Pipeline) // realised minus as-of pipeline
	}
	if m.ResidualK > 0 && len(gaps) > m.ResidualK {
		gaps = gaps[len(gaps)-m.ResidualK:] // most recent K (history is chronological)
	}
	if len(gaps) == 0 {
		return SeasonalRecent{K: m.FallbackK}.Predict(history, target)
	}
	out := make([]float64, len(gaps))
	for i, g := range gaps {
		if v := target.Pipeline + g; v > 0 {
			out[i] = v // burden is non-negative
		}
	}
	return out
}

// pipelineReliable guards the multiplicative models against small-denominator
// explosions: a historical pipeline is only a usable divisor when it explains a
// meaningful fraction of what it is divided into.
const pipelineReliableFraction = 0.2

// PipelineRatio scales the target's known pipeline by the empirical distribution
// of past realised/pipeline ratios. The spread then grows with the forecast
// level (heteroscedastic) and inherits the ratios' right-skew — usually better
// calibrated than an additive residual.
type PipelineRatio struct{ FallbackK int }

func (PipelineRatio) Name() string { return "pipeline×ratio" }
func (m PipelineRatio) Predict(history []Point, target Point) []float64 {
	if target.Pipeline <= 0 {
		return SeasonalRecent{K: m.FallbackK}.Predict(history, target)
	}
	out := make([]float64, 0, len(history))
	for _, h := range history {
		if h.Pipeline >= pipelineReliableFraction*h.Value && h.Pipeline > 0 {
			out = append(out, target.Pipeline*h.Value/h.Pipeline)
		}
	}
	if len(out) == 0 {
		return SeasonalRecent{K: m.FallbackK}.Predict(history, target)
	}
	return out
}

// PipelineDecomp models the two burden components separately: the planned term
// as the known pipeline scaled by past planned-overrun factors (realised
// planned / pipeline), plus the emergency term as its past realised values. The
// factor and emergency draw are taken jointly per historical month, preserving
// any within-month correlation.
//
// EMPIRICALLY POOR (kept for the record, not in the default lineup): the
// vintaged pipeline is biased LOW (it misses late-filed and overrun works), so
// planned/pipeline >> 1 and the multiplicative correction amplifies rather than
// shifts — far worse than the additive PipelineResidual. Prefer the latter.
type PipelineDecomp struct{ FallbackK int }

func (PipelineDecomp) Name() string { return "pipeline-decomp" }
func (m PipelineDecomp) Predict(history []Point, target Point) []float64 {
	if target.Pipeline <= 0 {
		return SeasonalRecent{K: m.FallbackK}.Predict(history, target)
	}
	out := make([]float64, 0, len(history))
	for _, h := range history {
		if h.Pipeline < pipelineReliableFraction*h.Planned || h.Pipeline <= 0 {
			continue // unreliable planned-overrun divisor
		}
		v := target.Pipeline*(h.Planned/h.Pipeline) + h.Emergency
		if v < 0 {
			v = 0
		}
		out = append(out, v)
	}
	if len(out) == 0 {
		return SeasonalRecent{K: m.FallbackK}.Predict(history, target)
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
	Scored    int       // points actually scored
	Abstained int       // points skipped for lack of history
	MeanCRPS  float64   // primary metric, lower better
	CalUnif   float64   // calibration deviation from uniform PIT, lower better
	PITBins   []float64 // PIT histogram (fractions); flat = well-calibrated
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
			PITBins:   a.cal.Bins(),
		}
	}
	return out
}
