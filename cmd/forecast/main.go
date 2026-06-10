// Command forecast produces the committed predictive distribution for a target
// month: it reads the hybrid burden series, runs the hier-spatial model on the
// history up to the last realised month plus the target month's known permit
// pipeline, and writes a prediction file (per-unit ensembles + the London total)
// to data/predictions/. Committing this file is the proof-of-commit record.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/umbralcalc/traffic-forecaster/internal/burdenmodel"
	"github.com/umbralcalc/traffic-forecaster/internal/forecast"
	"github.com/umbralcalc/traffic-forecaster/internal/series"
)

type unitPrediction struct {
	Unit     string    `json:"unit"`
	Borough  string    `json:"borough"`
	Pipeline float64   `json:"pipeline"`
	Mean     float64   `json:"mean"`
	Quantile q         `json:"quantiles"`
	Ensemble []float64 `json:"ensemble"`
}

type q struct {
	P5, P25, P50, P75, P95 float64
}

type prediction struct {
	TargetMonth   string           `json:"target_month"`
	MadeAt        string           `json:"made_at"`
	Model         string           `json:"model"`
	ConfigVersion int              `json:"config_version"`
	Params        map[string]any   `json:"params"`
	LondonTotal   unitPrediction   `json:"london_total"`
	Units         []unitPrediction `json:"units"`
}

func main() {
	in := flag.String("in", filepath.Join("data", "burden", "hybrid-n150.csv"), "hybrid burden series CSV")
	month := flag.String("month", "", "target month YYYY-MM (required)")
	lastRealised := flag.String("last-realised", "", "last realised month YYYY-MM (default: month before target)")
	out := flag.String("out", filepath.Join("data", "predictions"), "predictions directory")
	residualK := flag.Int("residual-k", 18, "trailing residual window (months)")
	n := flag.Int("n", 200, "ensemble size")
	seed := flag.Uint64("seed", 1, "RNG seed")
	flag.Parse()

	if err := run(*in, *month, *lastRealised, *out, *residualK, *n, *seed); err != nil {
		fmt.Fprintln(os.Stderr, "forecast:", err)
		os.Exit(1)
	}
}

func run(in, month, lastRealised, out string, residualK, n int, seed uint64) error {
	if month == "" {
		return fmt.Errorf("-month is required (YYYY-MM)")
	}
	ty, tm, err := series.ParseMonth(month)
	if err != nil {
		return err
	}
	targetIdx := series.MonthIndex(ty, tm)
	if lastRealised == "" {
		lastRealised = prevMonth(ty, tm)
	}
	ly, lm, err := series.ParseMonth(lastRealised)
	if err != nil {
		return err
	}
	lastIdx := series.MonthIndex(ly, lm)

	loaded, err := series.Load(in, "weighted_days", "unit")
	if err != nil {
		return err
	}

	histories := map[string][]forecast.Point{}
	targets := map[string]forecast.Point{}
	for unit, pts := range loaded.Series {
		var hist []forecast.Point
		var pipeline float64
		for _, p := range pts {
			idx := series.MonthIndex(p.Year, p.Month)
			switch {
			case idx <= lastIdx:
				hist = append(hist, p)
			case idx == targetIdx:
				pipeline = p.Pipeline // the target month's known forward pipeline
			}
		}
		histories[unit] = hist
		targets[unit] = forecast.Point{Year: ty, Month: tm, Pipeline: pipeline}
	}

	units := keys(loaded.Series)
	adjacency := burdenmodel.GridAdjacency(units)
	model := burdenmodel.SpatialFactorModel{
		ResidualK: residualK, N: n, FallbackK: 12, Seed: seed, Adjacency: adjacency,
	}
	ensembles := model.PredictAll(histories, targets)

	pred := prediction{
		TargetMonth:   month,
		MadeAt:        time.Now().UTC().Format(time.RFC3339),
		Model:         model.Name(),
		ConfigVersion: 2,
		Params: map[string]any{
			"residual_k": residualK, "n": n, "cell_km": 2, "seed": seed,
			"last_realised": lastRealised,
		},
	}
	total := make([]float64, n)
	for _, unit := range units {
		ens := ensembles[unit]
		if len(ens) != n {
			continue // unscorable unit (shouldn't happen); skip from total
		}
		for k := 0; k < n; k++ {
			total[k] += ens[k]
		}
		pred.Units = append(pred.Units, summarise(unit, loaded.Group[unit], targets[unit].Pipeline, ens))
	}
	sort.Slice(pred.Units, func(i, j int) bool { return pred.Units[i].Unit < pred.Units[j].Unit })
	pred.LondonTotal = summarise("LONDON_TOTAL", "", 0, total)

	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	path := filepath.Join(out, month+".json")
	data, err := json.MarshalIndent(pred, "", " ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %s: %d units, target %s (history <= %s)\n", path, len(pred.Units), month, lastRealised)
	fmt.Printf("London total burden: mean %.0f  p5 %.0f  p50 %.0f  p95 %.0f\n",
		pred.LondonTotal.Mean, pred.LondonTotal.Quantile.P5, pred.LondonTotal.Quantile.P50, pred.LondonTotal.Quantile.P95)
	return nil
}

func summarise(unit, borough string, pipeline float64, ens []float64) unitPrediction {
	s := append([]float64(nil), ens...)
	sort.Float64s(s)
	var sum float64
	for _, v := range s {
		sum += v
	}
	r := make([]float64, len(s))
	for i, v := range s {
		r[i] = math.Round(v*100) / 100
	}
	return unitPrediction{
		Unit: unit, Borough: borough, Pipeline: round2(pipeline),
		Mean: round2(sum / float64(len(s))),
		Quantile: q{
			P5: round2(quantile(s, 0.05)), P25: round2(quantile(s, 0.25)),
			P50: round2(quantile(s, 0.50)), P75: round2(quantile(s, 0.75)),
			P95: round2(quantile(s, 0.95)),
		},
		Ensemble: r,
	}
}

func quantile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	return sorted[int(p*float64(len(sorted)-1))]
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

func keys(m map[string][]forecast.Point) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func prevMonth(y, m int) string {
	m--
	if m == 0 {
		m, y = 12, y-1
	}
	return fmt.Sprintf("%04d-%02d", y, m)
}
