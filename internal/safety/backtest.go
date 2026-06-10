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

func (s *Scores) finalise() {
	if s.N > 0 {
		s.Brier /= float64(s.N)
		s.LogLoss /= float64(s.N)
		s.Deviance /= float64(s.N)
	}
	s.CalErr = s.Rel.CalibrationError()
}

// Backtest runs an expanding-window backtest of the PoissonFactorModel over a
// dense per-cell monthly panel, alongside two reference forecasters that share
// the same train/test split: a per-cell flat base-rate ("naive") and the
// London-pooled mean rate ("climatology"). It returns the three score sets.
func Backtest(panel map[string][]series.Point, minHistory, n, histK int) (model, naive, clim *Scores) {
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

		preds := PoissonFactorModel{N: n, Seed: 1, HistK: histK}.PredictAll(hist, ty, tm)
		for cell, y := range realised {
			p := preds[cell]
			model.add(1-p.Rating, p.Expected, y)

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
	}
	model.finalise()
	naive.finalise()
	clim.finalise()
	return model, naive, clim
}
