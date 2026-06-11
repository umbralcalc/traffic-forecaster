package safety

import (
	"math"
	"sort"

	"github.com/umbralcalc/traffic-forecaster/internal/scoring"
	"github.com/umbralcalc/traffic-forecaster/internal/series"
)

// Scores accumulates the proper scores for one forecaster over a backtest: Brier
// and log-loss on the published P(incident), Poisson deviance on the expected
// count, and the calibration error of the probability forecast. All are means
// over the scored cell-months; lower is better (cal.err closer to 0).
type Scores struct {
	Name             string
	Brier, LogLoss   float64
	Deviance, CalErr float64
	N                int
	Rel              *scoring.Reliability
	// London-total metrics (where the shared factor's quality shows up): mean CRPS
	// of the predicted total-collision ensemble against the realised total, and the
	// fraction of months whose realised total fell in the predictive 90% interval.
	TotalCRPS  float64
	TotalCover float64
	totalN     int
}

func newScores(name string) *Scores { return &Scores{Name: name, Rel: scoring.NewReliability(10)} }

func (s *Scores) add(pIncident, expected float64, y int) {
	occurred := y > 0
	s.Brier += scoring.Brier(pIncident, occurred)
	s.LogLoss += scoring.LogLoss(pIncident, occurred)
	s.Deviance += scoring.PoissonDeviance(expected, y)
	s.Rel.Add(pIncident, occurred)
	s.N++
}

// addTotal scores one month's London-total forecast: the CRPS of the (unsorted)
// total-count ensemble against the realised total, and whether the total fell in
// the predictive 90% interval.
func (s *Scores) addTotal(ens []float64, realised int) {
	if len(ens) == 0 {
		return
	}
	sorted := append([]float64(nil), ens...)
	sort.Float64s(sorted)
	s.TotalCRPS += ensembleCRPS(sorted, float64(realised))
	lo, hi := sorted[int(0.05*float64(len(sorted)-1))], sorted[int(0.95*float64(len(sorted)-1))]
	if float64(realised) >= lo && float64(realised) <= hi {
		s.TotalCover++
	}
	s.totalN++
}

func (s *Scores) finalise() {
	if s.N > 0 {
		s.Brier /= float64(s.N)
		s.LogLoss /= float64(s.N)
		s.Deviance /= float64(s.N)
	}
	if s.totalN > 0 {
		s.TotalCRPS /= float64(s.totalN)
		s.TotalCover /= float64(s.totalN)
	}
	s.CalErr = s.Rel.CalibrationError()
}

// ensembleCRPS is the CRPS of a sorted sample ensemble against observation y,
// via CRPS = E|X-y| - 0.5 E|X-X'| (the standard sample estimator).
func ensembleCRPS(sorted []float64, y float64) float64 {
	n := len(sorted)
	var mad float64
	for _, x := range sorted {
		mad += math.Abs(x - y)
	}
	mad /= float64(n)
	// E|X-X'| via the sorted-sample identity: (2/n^2) * sum_i (2i-n+1) x_i.
	var spread float64
	for i, x := range sorted {
		spread += float64(2*i-n+1) * x
	}
	spread *= 2.0 / float64(n*n)
	return mad - 0.5*spread
}

// Backtest runs an expanding-window backtest of the given PoissonFactorModel over
// a dense per-cell monthly panel, alongside two reference forecasters that share
// the same train/test split: a per-cell flat base-rate ("naive") and the
// London-pooled mean rate ("climatology"). It returns the three score sets.
func Backtest(panel map[string][]series.Point, fitted PoissonFactorModel, minHistory int) (model, naive, clim *Scores) {
	idxSet := map[int]bool{}
	for _, pts := range panel {
		for _, p := range pts {
			idxSet[p.Year*12+p.Month-1] = true
		}
	}
	var months []int
	for k := range idxSet {
		months = append(months, k)
	}
	sort.Ints(months)

	model = newScores("poisson-factor")
	naive = newScores("naive-baserate")
	clim = newScores("climatology")

	for ti := minHistory; ti < len(months); ti++ {
		target := months[ti]
		ty, tm := target/12, target%12+1

		hist := map[string][]series.Point{}
		realised := map[string]int{}
		var poolSum, poolN float64
		for cell, pts := range panel {
			var h []series.Point
			for _, p := range pts {
				k := p.Year*12 + p.Month - 1
				switch {
				case k < target:
					h = append(h, p)
					poolSum += p.Value
					poolN++
				case k == target:
					realised[cell] = int(p.Value + 0.5)
				}
			}
			hist[cell] = h
		}
		poolRate := 0.0
		if poolN > 0 {
			poolRate = poolSum / poolN
		}

		preds := fitted.PredictAll(hist, ty, tm)
		var totalEns []float64
		var realisedTotal int
		for cell, y := range realised {
			p := preds[cell]
			model.add(1-p.Rating, p.Expected, y)
			if totalEns == nil {
				totalEns = make([]float64, len(p.Ensemble))
			}
			for k, v := range p.Ensemble {
				totalEns[k] += float64(v)
			}
			realisedTotal += y

			var cs, cn float64
			for _, h := range hist[cell] {
				cs += h.Value
				cn++
			}
			rate := poolRate
			if cn > 0 {
				rate = cs / cn
			}
			naive.add(1-math.Exp(-rate), rate, y)
			clim.add(1-math.Exp(-poolRate), poolRate, y)
		}
		model.addTotal(totalEns, realisedTotal)
	}
	model.finalise()
	naive.finalise()
	clim.finalise()
	return model, naive, clim
}
