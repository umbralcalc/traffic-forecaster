// Command grid-sweep settles the fundamental modelling-unit question for the
// safety rating: it streams STATS19 once, then bins the same London collisions at
// several grid resolutions (and by local authority district) and backtests the
// Poisson safety model on each. Lower log-loss / Poisson deviance with good
// calibration wins. This is an exploration tool — it prints a comparison table
// and writes nothing.
package main

import (
	"bufio"
	"encoding/csv"
	"flag"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/umbralcalc/traffic-forecaster/internal/grid"
	"github.com/umbralcalc/traffic-forecaster/internal/safety"
	"github.com/umbralcalc/traffic-forecaster/internal/series"
)

const defaultURL = "https://data.dft.gov.uk/road-accidents-safety-data/dft-road-casualty-statistics-collision-last-5-years.csv"

const (
	minE, maxE = 500000.0, 565000.0
	minN, maxN = 155000.0, 205000.0
)

type rec struct {
	e, n float64
	ksi  bool
	ym   int // year*12 + month-1
	la   string
}

func main() {
	url := flag.String("url", defaultURL, "STATS19 collision CSV URL")
	minHistory := flag.Int("min-history", 24, "months of history before scoring")
	n := flag.Int("n", 400, "ensemble size")
	timeout := flag.Duration("timeout", 20*time.Minute, "download deadline")
	flag.Parse()
	if err := run(*url, *minHistory, *n, *timeout); err != nil {
		fmt.Fprintln(os.Stderr, "grid-sweep:", err)
		os.Exit(1)
	}
}

func run(url string, minHistory, n int, timeout time.Duration) error {
	recs, err := load(url, timeout)
	if err != nil {
		return err
	}
	fmt.Printf("loaded %d London collisions\n\n", len(recs))

	type cfg struct {
		name  string
		keyOf func(rec) string
	}
	cfgs := []cfg{
		{"grid-0.5km", gridKey(500)},
		{"grid-1km", gridKey(1000)},
		{"grid-2km", gridKey(2000)},
		{"grid-3km", gridKey(3000)},
		{"grid-4km", gridKey(4000)},
		{"LA-district", func(r rec) string { return r.la }},
	}

	// Raw Brier/log-loss are not comparable across aggregation levels (they are
	// minimised where the event is near-certain or near-impossible). The honest
	// signal is SKILL vs the naive per-unit base-rate at the same resolution —
	// where does the hierarchical structure (season + shared factor) add value —
	// plus calibration (which should hold at every resolution).
	for _, tier := range []string{"accidents", "ksi"} {
		fmt.Printf("===== tier %q (skill = %% the model beats the naive base-rate) =====\n", tier)
		fmt.Printf("  %-12s %6s %9s %9s %10s %10s %8s\n",
			"unit", "units", "dev.model", "dev.naive", "dev.skill", "ll.skill", "cal.err")
		for _, c := range cfgs {
			panel := buildPanel(recs, c.keyOf, tier == "ksi")
			model, naive, _ := safety.Backtest(panel, safety.PoissonFactorModel{N: n, Seed: 1}, minHistory)
			devSkill := 100 * (naive.Deviance - model.Deviance) / naive.Deviance
			llSkill := 100 * (naive.LogLoss - model.LogLoss) / naive.LogLoss
			fmt.Printf("  %-12s %6d %9.3f %9.3f %9.1f%% %9.1f%% %8.3f\n",
				c.name, len(panel), model.Deviance, naive.Deviance, devSkill, llSkill, model.CalErr)
		}
		fmt.Println()
	}
	return nil
}

func gridKey(cellM float64) func(rec) string {
	return func(r rec) string { return grid.CellID(r.e, r.n, cellM) }
}

// buildPanel groups collisions by unit key and month into a DENSE panel (zeros
// filled for every unit-month in the observed range).
func buildPanel(recs []rec, keyOf func(rec) string, ksiOnly bool) map[string][]series.Point {
	counts := map[string]map[int]int{} // key -> ym -> count
	ymSet := map[int]bool{}
	for _, r := range recs {
		if ksiOnly && !r.ksi {
			continue
		}
		k := keyOf(r)
		if k == "" {
			continue
		}
		if counts[k] == nil {
			counts[k] = map[int]int{}
		}
		counts[k][r.ym]++
		ymSet[r.ym] = true
	}
	// KSI-only filtering drops some months for sparse units; use the full observed
	// range from the all-tier so the panel stays a complete grid.
	var yms []int
	for ym := range ymSet {
		yms = append(yms, ym)
	}
	sort.Ints(yms)

	panel := make(map[string][]series.Point, len(counts))
	for k, byYM := range counts {
		pts := make([]series.Point, 0, len(yms))
		for _, ym := range yms {
			pts = append(pts, series.Point{Year: ym / 12, Month: ym%12 + 1, Value: float64(byYM[ym])})
		}
		panel[k] = pts
	}
	return panel
}

func load(url string, timeout time.Duration) ([]rec, error) {
	client := &http.Client{Timeout: timeout}
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("User-Agent", "traffic-forecaster (+https://github.com/umbralcalc/traffic-forecaster)")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: status %d", url, resp.StatusCode)
	}
	r := csv.NewReader(bufio.NewReaderSize(resp.Body, 1<<20))
	r.ReuseRecord = true
	r.FieldsPerRecord = -1
	header, err := r.Read()
	if err != nil {
		return nil, err
	}
	col := map[string]int{}
	for i, h := range header {
		col[h] = i
	}
	ei, ni := col["location_easting_osgr"], col["location_northing_osgr"]
	si, di := col["collision_severity"], col["date"]
	lai, hasLA := col["local_authority_ons_district"]

	var out []rec
	for {
		row, err := r.Read()
		if err != nil {
			break
		}
		e, err1 := strconv.ParseFloat(row[ei], 64)
		n, err2 := strconv.ParseFloat(row[ni], 64)
		if err1 != nil || err2 != nil || e < minE || e > maxE || n < minN || n > maxN {
			continue
		}
		ym, ok := ymOf(row[di])
		if !ok {
			continue
		}
		la := ""
		if hasLA && lai < len(row) {
			la = row[lai]
		}
		out = append(out, rec{e: e, n: n, ksi: row[si] == "1" || row[si] == "2", ym: ym, la: la})
	}
	return out, nil
}

// ymOf parses STATS19 "DD/MM/YYYY" into year*12 + month-1.
func ymOf(s string) (int, bool) {
	if len(s) != 10 || s[2] != '/' || s[5] != '/' {
		return 0, false
	}
	y, e1 := strconv.Atoi(s[6:10])
	m, e2 := strconv.Atoi(s[3:5])
	if e1 != nil || e2 != nil || m < 1 || m > 12 {
		return 0, false
	}
	return y*12 + m - 1, true
}
