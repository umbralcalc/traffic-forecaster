// Command safety-backtest runs an expanding-window backtest of the road-safety
// rating model over the per-cell collision panel (built by cmd/build-accident-
// burden). It scores the actual published quantities — P(incident) via Brier and
// log-loss, and the expected count via Poisson deviance — against a naive per-cell
// base-rate floor, so we can see whether the hierarchical Poisson model earns its
// complexity. Run per tier: -col accidents (headline) or -col ksi (serious).
package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"

	"github.com/umbralcalc/traffic-forecaster/internal/forecast"
	"github.com/umbralcalc/traffic-forecaster/internal/safety"
	"github.com/umbralcalc/traffic-forecaster/internal/scoring"
	"github.com/umbralcalc/traffic-forecaster/internal/series"
)

func main() {
	in := flag.String("in", filepath.Join("data", "incidents", "accident-burden.csv"), "collision panel CSV")
	col := flag.String("col", "accidents", "tier column to forecast (accidents | ksi)")
	minHistory := flag.Int("min-history", 24, "months of history before scoring a target")
	histK := flag.Int("hist-k", 0, "trailing window for level/anomaly (0 = all history)")
	n := flag.Int("n", 400, "ensemble size")
	flag.Parse()
	if err := run(*in, *col, *minHistory, *histK, *n); err != nil {
		fmt.Fprintln(os.Stderr, "safety-backtest:", err)
		os.Exit(1)
	}
}

type scoreAcc struct {
	name           string
	brier, logloss float64
	deviance       float64
	n              int
	rel            *scoring.Reliability
}

func newAcc(name string) *scoreAcc { return &scoreAcc{name: name, rel: scoring.NewReliability(10)} }

func (s *scoreAcc) add(pIncident, expected float64, y int) {
	occurred := y > 0
	s.brier += scoring.Brier(pIncident, occurred)
	s.logloss += scoring.LogLoss(pIncident, occurred)
	s.deviance += scoring.PoissonDeviance(expected, y)
	s.rel.Add(pIncident, occurred)
	s.n++
}

func (s *scoreAcc) report() {
	fmt.Printf("  %-16s  Brier %.4f   logloss %.4f   Pois.dev %.3f   cal.err %.3f   (n=%d)\n",
		s.name, s.brier/float64(s.n), s.logloss/float64(s.n), s.deviance/float64(s.n),
		s.rel.CalibrationError(), s.n)
}

func run(in, col string, minHistory, histK, n int) error {
	loaded, err := series.Load(in, col, "cell")
	if err != nil {
		return err
	}
	panel := loaded.Series

	// Sorted month index list (the panel is dense, so any cell carries them all).
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
	fmt.Printf("panel: %d cells, %d months (%s..%s), tier %q\n",
		len(panel), len(months), monthStr(months[0]), monthStr(months[len(months)-1]), col)

	model := newAcc("poisson-factor")
	naive := newAcc("naive-baserate")
	clim := newAcc("climatology")

	for ti := minHistory; ti < len(months); ti++ {
		target := months[ti]
		ty, tm := target/12, target%12+1

		// Split: history strictly before the target month; realised at the target.
		hist := map[string][]forecast.Point{}
		realised := map[string]int{}
		var poolSum, poolN float64
		for cell, pts := range panel {
			var h []forecast.Point
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

		// Hierarchical Poisson model.
		preds := safety.PoissonFactorModel{N: n, Seed: 1, HistK: histK}.PredictAll(hist, ty, tm)
		for cell, y := range realised {
			p := preds[cell]
			model.add(1-p.Rating, p.Expected, y)

			// Naive: per-cell flat mean rate -> Poisson(0) survival.
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

			// Climatology: the London-pooled mean rate for every cell.
			clim.add(1-math.Exp(-poolRate), poolRate, y)
		}
	}

	fmt.Printf("\nexpanding-window backtest (min history %d months, hist-k %d):\n", minHistory, histK)
	fmt.Println("  (lower Brier/logloss/deviance better; cal.err closer to 0 = better-calibrated)")
	model.report()
	naive.report()
	clim.report()

	fmt.Println("\nreliability (forecast P(incident) vs realised frequency):")
	mp, fr, ct := model.rel.Curve()
	for i := range mp {
		fmt.Printf("  p~%.2f  ->  realised %.2f   (n=%.0f)\n", mp[i], fr[i], ct[i])
	}
	return nil
}

func monthStr(k int) string { return fmt.Sprintf("%04d-%02d", k/12, k%12+1) }
