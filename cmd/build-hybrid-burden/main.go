// Command build-hybrid-burden builds an adaptive-resolution burden series: the
// busiest grid cells (top-N by realised activity) stay as fine cells, while every
// other work folds into its borough's pooled "rest" unit. This keeps fine spatial
// detail in the dense core — where local coupling resolves and there are enough
// events to calibrate — and coarse, well-populated units in the sparse remainder.
package main

import (
	"bufio"
	"compress/gzip"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/umbralcalc/traffic-forecaster/internal/burden"
	"github.com/umbralcalc/traffic-forecaster/internal/streetmanager"
)

func main() {
	inDir := flag.String("in", filepath.Join("data", "streetmanager"), "directory of *.london.ndjson.gz extracts")
	outDir := flag.String("out-dir", filepath.Join("data", "burden"), "output directory (writes hybrid-n{N}.csv per N)")
	cellKm := flag.Float64("cell-km", 2.0, "grid cell size in kilometres")
	denseTopN := flag.String("dense-top-n", "150", "comma-separated N values: keep the N busiest cells as fine cells")
	from := flag.String("from", "2020-01", "first month, YYYY-MM")
	to := flag.String("to", "2027-12", "last month, YYYY-MM")
	rankTo := flag.String("rank-to", "2026-05", "rank cell activity over months up to here (exclude forward tail)")
	flag.Parse()

	if err := run(*inDir, *outDir, *cellKm*1000, *denseTopN, *from, *to, *rankTo); err != nil {
		fmt.Fprintln(os.Stderr, "build-hybrid-burden:", err)
		os.Exit(1)
	}
}

func run(inDir, outDir string, cellM float64, denseTopNs, from, to, rankTo string) error {
	fromT, _ := monthTime(from, false)
	toT, _ := monthTime(to, true)
	rankToT, _ := monthTime(rankTo, true)

	var ns []int
	for _, s := range strings.Split(denseTopNs, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil {
			return fmt.Errorf("bad dense-top-n %q", s)
		}
		ns = append(ns, n)
	}

	paths, err := filepath.Glob(filepath.Join(inDir, "*.london.ndjson.gz"))
	if err != nil || len(paths) == 0 {
		return fmt.Errorf("no extracts in %s (run ingest-streetmanager -write)", inDir)
	}
	sort.Strings(paths)
	ws := burden.Works{}
	for _, p := range paths {
		if err := readExtract(p, ws); err != nil {
			return err
		}
	}

	cellBorough := ws.CellBoroughs(cellM)
	unitBorough := func(unit string) string {
		if strings.HasSuffix(unit, burden.RestSuffix) {
			return strings.TrimSuffix(unit, burden.RestSuffix)
		}
		return cellBorough[unit]
	}

	// Rank cells by realised activity once; reuse across N variants.
	totals := ws.CellTotals(cellM, fromT, rankToT)
	type ct struct {
		cell string
		w    float64
	}
	cts := make([]ct, 0, len(totals))
	for c, w := range totals {
		cts = append(cts, ct{c, w})
	}
	sort.Slice(cts, func(i, j int) bool { return cts[i].w > cts[j].w })

	for _, denseTopN := range ns {
		dense := map[string]bool{}
		for i := 0; i < denseTopN && i < len(cts); i++ {
			dense[cts[i].cell] = true
		}
		rows := ws.HybridSeries(cellM, dense, fromT, toT)
		pipeline := map[string]float64{}
		for _, p := range ws.HybridPipelineSeries(cellM, dense, fromT, toT) {
			pipeline[p.Month+"|"+p.Key] = p.WeightedDays
		}
		out := filepath.Join(outDir, fmt.Sprintf("hybrid-n%d.csv", denseTopN))
		if err := writeCSV(out, rows, pipeline, unitBorough); err != nil {
			return err
		}
		units, fine, rest := map[string]bool{}, 0, 0
		for _, r := range rows {
			if !units[r.Key] {
				units[r.Key] = true
				if strings.HasSuffix(r.Key, burden.RestSuffix) {
					rest++
				} else {
					fine++
				}
			}
		}
		fmt.Printf("wrote %s: %d units (%d fine + %d rest)\n", out, len(units), fine, rest)
	}
	fmt.Printf("\n%s\n", streetmanager.Attribution)
	return nil
}

func readExtract(path string, ws burden.Works) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer zr.Close()
	sc := bufio.NewScanner(zr)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		var r streetmanager.Record
		if json.Unmarshal(sc.Bytes(), &r) == nil {
			ws.Apply(r)
		}
	}
	return sc.Err()
}

func writeCSV(out string, rows []burden.Row, pipeline map[string]float64, unitBorough func(string) string) error {
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	defer w.Flush()
	w.Write([]string{"month", "unit", "borough", "works", "work_days", "weighted_days", "planned_days", "emergency_days", "pipeline_weighted_days"})
	for _, r := range rows {
		w.Write([]string{
			r.Month, r.Key, unitBorough(r.Key), strconv.Itoa(r.Works),
			f2(r.WorkDays), f2(r.WeightedDays), f2(r.PlannedDays), f2(r.EmergencyDays),
			f2(pipeline[r.Month+"|"+r.Key]),
		})
	}
	return w.Error()
}

func monthTime(s string, end bool) (time.Time, error) {
	t, err := time.Parse("2006-01", s)
	if err != nil {
		return time.Time{}, err
	}
	if end {
		return t.AddDate(0, 1, 0).Add(-time.Second), nil
	}
	return t, nil
}

func f2(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }
