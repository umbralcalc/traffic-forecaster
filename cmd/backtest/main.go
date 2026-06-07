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

	"github.com/umbralcalc/traffic-forecaster/internal/forecast"
)

func main() {
	in := flag.String("in", filepath.Join("data", "burden", "works-burden.csv"), "burden series CSV")
	col := flag.String("col", "weighted_days", "burden column to forecast")
	lastRealised := flag.String("last-realised", "2026-05", "last fully-realised month, YYYY-MM (forward tail excluded)")
	minHistory := flag.Int("min-history", 13, "months of history required before scoring a point")
	flag.Parse()

	if err := run(*in, *col, *lastRealised, *minHistory); err != nil {
		fmt.Fprintln(os.Stderr, "backtest:", err)
		os.Exit(1)
	}
}

func run(in, col, lastRealised string, minHistory int) error {
	series, entities, months, err := loadSeries(in, col, lastRealised)
	if err != nil {
		return err
	}
	fmt.Printf("series: %d entities, %d realised months (<= %s), forecasting %q\n",
		entities, months, lastRealised, col)

	models := []forecast.Model{
		forecast.ClimatologyMean{},
		forecast.RecentWindow{K: 12},
		forecast.Seasonal{FallbackK: 12},
		forecast.SeasonalRecent{K: 12},
		forecast.PipelineResidual{FallbackK: 12},
	}
	results := forecast.Backtest(series, models, minHistory)
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
	fmt.Println("\n(lower CRPS better; cal.unif closer to 0 = better-calibrated PIT;")
	fmt.Println(" climatology-mean is a point forecast — the floor distributions should beat.)")
	return nil
}

func loadSeries(path, col, lastRealised string) (map[string][]forecast.Point, int, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("%w (run cmd/build-burden first)", err)
	}
	defer f.Close()

	r := csv.NewReader(f)
	header, err := r.Read()
	if err != nil {
		return nil, 0, 0, err
	}
	idx := map[string]int{}
	for i, h := range header {
		idx[h] = i
	}
	for _, need := range []string{"month", "borough", col} {
		if _, ok := idx[need]; !ok {
			return nil, 0, 0, fmt.Errorf("CSV missing column %q", need)
		}
	}

	series := map[string][]forecast.Point{}
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
		var cov float64
		if ci, ok := idx["pipeline_weighted_days"]; ok && ci < len(rec) {
			cov, _ = strconv.ParseFloat(rec[ci], 64) // forward pipeline covariate (0 if blank)
		}
		borough := rec[idx["borough"]]
		series[borough] = append(series[borough], forecast.Point{Year: y, Month: m, Value: v, Covariate: cov})
		monthSet[month] = true
	}
	return series, len(series), len(monthSet), nil
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
