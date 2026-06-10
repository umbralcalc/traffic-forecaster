// Command backtest runs an expanding-window CRPS backtest of the baseline burden
// forecasters over the works-burden series (built by cmd/build-burden). It is the
// honest floor: the CRPS and calibration numbers any stochadex model must beat.
//
// Only realised months are scored; the forward-pipeline tail of the series
// (months beyond the last archived month) is excluded since those works have not
// happened yet.
package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/umbralcalc/traffic-forecaster/internal/burdenmodel"
	"github.com/umbralcalc/traffic-forecaster/internal/forecast"
)

func main() {
	in := flag.String("in", filepath.Join("data", "burden", "works-burden.csv"), "burden series CSV")
	col := flag.String("col", "weighted_days", "burden column to forecast")
	entity := flag.String("entity", "borough", "entity column (borough or cell)")
	cellKm := flag.Float64("cell-km", 0, "if >0, treat entities as grid cells and use grid adjacency")
	maxEntities := flag.Int("max-entities", 0, "keep only the N busiest entities (0 = all)")
	noPipeline := flag.Bool("no-pipeline", false, "no forward covariate (e.g. accidents): forecast base rate + structure")
	seasonal := flag.Bool("seasonal", false, "also score a seasonal-baseline variant of the spatial model")
	lastRealised := flag.String("last-realised", "2026-05", "last fully-realised month, YYYY-MM (forward tail excluded)")
	minHistory := flag.Int("min-history", 13, "months of history required before scoring a point")
	flag.Parse()

	if err := run(*in, *col, *entity, *cellKm, *maxEntities, *noPipeline, *seasonal, *lastRealised, *minHistory); err != nil {
		fmt.Fprintln(os.Stderr, "backtest:", err)
		os.Exit(1)
	}
}

func run(in, col, entity string, cellKm float64, maxEntities int, noPipeline, seasonal bool, lastRealised string, minHistory int) error {
	series, group, entities, months, err := loadSeries(in, col, entity, lastRealised, maxEntities)
	if err != nil {
		return err
	}
	fmt.Printf("series: %d entities (%s), %d realised months (<= %s), forecasting %q\n",
		entities, entity, months, lastRealised, col)

	// Spatial adjacency: grid neighbours for cells, the borough graph otherwise.
	var adjacency map[string][]string
	if cellKm > 0 {
		keys := make([]string, 0, len(series))
		for k := range series {
			keys = append(keys, k)
		}
		adjacency = burdenmodel.GridAdjacency(keys)
	}
	spatial := func(n int) burdenmodel.SpatialFactorModel {
		return burdenmodel.SpatialFactorModel{ResidualK: 18, N: n, FallbackK: 12, Seed: 1, Adjacency: adjacency, NoPipeline: noPipeline}
	}
	common := func(n int) burdenmodel.CommonFactorModel {
		return burdenmodel.CommonFactorModel{ResidualK: 18, N: n, FallbackK: 12, Seed: 1, NoPipeline: noPipeline}
	}
	// Independent per-unit baseline: no forward covariate -> SeasonalRecent.
	var indepModel forecast.Model = forecast.PipelineResidual{FallbackK: 12, ResidualK: 18}
	if noPipeline {
		indepModel = forecast.SeasonalRecent{K: 12}
	}

	models := []forecast.Model{
		forecast.ClimatologyMean{},
		forecast.RecentWindow{K: 12},
		forecast.Seasonal{FallbackK: 12},
		forecast.SeasonalRecent{K: 12},
		forecast.PipelineResidual{FallbackK: 12, ResidualK: 0},  // all-history (biased by trend)
		forecast.PipelineResidual{FallbackK: 12, ResidualK: 18}, // trailing window (trend-aware)
		forecast.PipelineRatio{FallbackK: 12},
	}
	// StochadexBurden runs N stochadex simulations per entity-month; fine at
	// borough scale but ~3.8M tiny sims at grid scale. It's the per-borough
	// marginal model — the grid story is the joint models — so skip it for grids.
	if cellKm == 0 {
		models = append(models, burdenmodel.StochadexBurden{ResidualK: 18, N: 100, FallbackK: 12, Seed: 1})
	}
	tic := time.Now()
	lap := func(label string) {
		fmt.Fprintf(os.Stderr, "[timing] %-22s %v\n", label, time.Since(tic))
		tic = time.Now()
	}

	results := forecast.Backtest(series, models, minHistory)
	lap("per-cell empirical")
	// Joint (cross-borough) models are scored together via BacktestJoint.
	results = append(results, forecast.BacktestJoint(series, common(100), minHistory))
	lap("joint common (marginal)")
	results = append(results, forecast.BacktestJoint(series, spatial(100), minHistory))
	lap("joint spatial (marginal)")
	spatialSeasonal := func(n int) burdenmodel.SpatialFactorModel {
		s := spatial(n)
		s.Seasonal = true
		return s
	}
	if seasonal {
		results = append(results, forecast.BacktestJoint(series, spatialSeasonal(100), minHistory))
		lap("joint spatial+seasonal (marginal)")
	}
	nested := func(n int) burdenmodel.NestedFactorModel {
		return burdenmodel.NestedFactorModel{ResidualK: 18, N: n, FallbackK: 12, Seed: 1, Group: group, Adjacency: adjacency}
	}
	if len(group) > 0 {
		results = append(results, forecast.BacktestJoint(series, nested(100), minHistory))
		lap("joint nested (marginal)")
	}
	sort.Slice(results, func(i, j int) bool { return results[i].MeanCRPS < results[j].MeanCRPS })

	fmt.Printf("\nexpanding-window backtest (min history %d months):\n", minHistory)
	fmt.Printf("  %-18s %10s %9s %9s %s\n", "model", "mean CRPS", "scored", "cal.unif", "")
	best := results[0].MeanCRPS
	for _, r := range results {
		marker := ""
		if r.MeanCRPS == best {
			marker = "  <- best"
		}
		fmt.Printf("  %-18s %10.1f %9d %9.2f%s\n", r.Name, r.MeanCRPS, r.Scored, r.CalUnif, marker)
	}
	// Joint metric: London-wide TOTAL burden, where cross-borough coupling pays
	// off. Compare the common-factor model against the best independent model.
	fmt.Println("\nLondon-total burden CRPS (joint metric — coupling should beat independent):")
	totSpatial := forecast.BacktestJointTotal(series, spatial(200), minHistory)
	lap("total spatial")
	totCommon := forecast.BacktestJointTotal(series, common(200), minHistory)
	lap("total common")
	totIndep := forecast.BacktestJointTotal(series,
		forecast.IndependentJoint{Model: indepModel, N: 200, Seed: 1}, minHistory)
	lap("total independent")
	totals := []forecast.ModelResult{totSpatial, totCommon, totIndep}
	if seasonal {
		totals = append(totals, forecast.BacktestJointTotal(series, spatialSeasonal(200), minHistory))
		lap("total spatial+seasonal")
	}
	if len(group) > 0 {
		totals = append(totals, forecast.BacktestJointTotal(series, nested(200), minHistory))
		lap("total nested")
	}
	for _, r := range totals {
		fmt.Printf("  %-34s mean CRPS %9.1f   cal.unif %6.2f\n", r.Name, r.MeanCRPS, r.CalUnif)
	}

	fmt.Println("\nPIT histograms (10 bins, each ~0.10 if calibrated; U-shape=overconfident, ∩=underconfident, slope=biased):")
	for _, r := range results {
		if r.Scored == 0 {
			continue
		}
		fmt.Printf("  %-18s ", r.Name)
		for _, b := range r.PITBins {
			fmt.Printf("%4.0f", b*100)
		}
		fmt.Println()
	}
	fmt.Println("\n(lower CRPS better; cal.unif closer to 0 = better-calibrated PIT;")
	fmt.Println(" climatology-mean is a point forecast — the floor distributions should beat.)")
	return nil
}

func loadSeries(path, col, entityCol, lastRealised string, maxEntities int) (map[string][]forecast.Point, map[string]string, int, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, 0, 0, fmt.Errorf("%w (run cmd/build-burden first)", err)
	}
	defer f.Close()

	r := csv.NewReader(f)
	header, err := r.Read()
	if err != nil {
		return nil, nil, 0, 0, err
	}
	idx := map[string]int{}
	for i, h := range header {
		idx[h] = i
	}
	for _, need := range []string{"month", entityCol, col} {
		if _, ok := idx[need]; !ok {
			return nil, nil, 0, 0, fmt.Errorf("CSV missing column %q", need)
		}
	}
	boroughCol, hasBorough := idx["borough"]

	series := map[string][]forecast.Point{}
	group := map[string]string{} // unit -> borough (from optional column)
	monthSet := map[string]bool{}
	for {
		rec, err := r.Read()
		if err != nil {
			break
		}
		month := rec[idx["month"]]
		if month > lastRealised {
			continue // exclude the forward-pipeline tail
		}
		y, m, err := parseMonth(month)
		if err != nil {
			continue
		}
		v, err := strconv.ParseFloat(rec[idx[col]], 64)
		if err != nil {
			continue
		}
		ent := rec[idx[entityCol]]
		if hasBorough && boroughCol < len(rec) {
			group[ent] = rec[boroughCol]
		}
		series[ent] = append(series[ent], forecast.Point{
			Year: y, Month: m, Value: v,
			Pipeline:  optFloat(rec, idx, "pipeline_weighted_days"),
			Planned:   optFloat(rec, idx, "planned_days"),
			Emergency: optFloat(rec, idx, "emergency_days"),
		})
		monthSet[month] = true
	}

	// Optionally keep only the busiest entities (by total burden) — useful to
	// bound cost on the dense, forecastable subset of a sparse grid.
	if maxEntities > 0 && len(series) > maxEntities {
		type tot struct {
			ent string
			sum float64
		}
		tots := make([]tot, 0, len(series))
		for ent, pts := range series {
			var s float64
			for _, p := range pts {
				s += p.Value
			}
			tots = append(tots, tot{ent, s})
		}
		sort.Slice(tots, func(i, j int) bool { return tots[i].sum > tots[j].sum })
		kept := map[string][]forecast.Point{}
		for _, t := range tots[:maxEntities] {
			kept[t.ent] = series[t.ent]
		}
		series = kept
	}
	return series, group, len(series), len(monthSet), nil
}

// optFloat reads a named column if present, returning 0 when absent or blank.
func optFloat(rec []string, idx map[string]int, name string) float64 {
	if ci, ok := idx[name]; ok && ci < len(rec) {
		v, _ := strconv.ParseFloat(rec[ci], 64)
		return v
	}
	return 0
}

func parseMonth(s string) (int, int, error) {
	if len(s) != 7 || s[4] != '-' {
		return 0, 0, fmt.Errorf("bad month %q", s)
	}
	y, err1 := strconv.Atoi(s[:4])
	m, err2 := strconv.Atoi(s[5:])
	if err1 != nil || err2 != nil || m < 1 || m > 12 {
		return 0, 0, fmt.Errorf("bad month %q", s)
	}
	return y, m, nil
}
