// Command safety-backtest runs an expanding-window backtest of the road-safety
// rating model over the per-cell collision panel (built by cmd/build-accident-
// burden). It scores the published quantities — P(incident) via Brier and
// log-loss, and the expected count via Poisson deviance — against a naive per-cell
// base-rate floor and a pooled climatology, so we can see whether the
// hierarchical Poisson model earns its complexity. Run per tier: -col accidents
// (headline) or -col ksi (serious).
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/umbralcalc/traffic-forecaster/internal/safety"
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

func run(in, col string, minHistory, histK, n int) error {
	loaded, err := series.Load(in, col, "cell")
	if err != nil {
		return err
	}
	fmt.Printf("panel: %d cells, tier %q\n", len(loaded.Series), col)

	model, naive, clim := safety.Backtest(loaded.Series, minHistory, n, histK)
	fmt.Printf("\nexpanding-window backtest (min history %d months, hist-k %d):\n", minHistory, histK)
	fmt.Println("  (lower Brier/logloss/deviance better; cal.err closer to 0 = better-calibrated)")
	for _, s := range []*safety.Scores{model, naive, clim} {
		fmt.Printf("  %-16s  Brier %.4f   logloss %.4f   Pois.dev %.3f   cal.err %.3f   (n=%d)\n",
			s.Name, s.Brier, s.LogLoss, s.Deviance, s.CalErr, s.N)
	}

	fmt.Println("\nreliability (forecast P(incident) vs realised frequency):")
	mp, fr, ct := model.Rel.Curve()
	for i := range mp {
		fmt.Printf("  p~%.2f  ->  realised %.2f   (n=%.0f)\n", mp[i], fr[i], ct[i])
	}
	return nil
}
