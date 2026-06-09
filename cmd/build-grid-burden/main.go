// Command build-grid-burden reconstructs the monthly burden series at grid-cell
// resolution (square BNG cells of a chosen size) from local Street Manager
// London extracts. It mirrors cmd/build-burden but attributes each located work
// to a grid cell instead of a borough — the finer spatial unit at which local
// coupling resolves. Output is gitignored derived data.
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
	"time"

	"github.com/umbralcalc/traffic-forecaster/internal/burden"
	"github.com/umbralcalc/traffic-forecaster/internal/streetmanager"
)

func main() {
	inDir := flag.String("in", filepath.Join("data", "streetmanager"), "directory of *.london.ndjson.gz extracts")
	out := flag.String("out", filepath.Join("data", "burden", "grid-burden.csv"), "output CSV path")
	cellKm := flag.Float64("cell-km", 2.0, "grid cell size in kilometres")
	from := flag.String("from", "2020-01", "first month to include, YYYY-MM")
	to := flag.String("to", "2027-12", "last month to include, YYYY-MM")
	flag.Parse()

	if err := run(*inDir, *out, *cellKm*1000, *from, *to); err != nil {
		fmt.Fprintln(os.Stderr, "build-grid-burden:", err)
		os.Exit(1)
	}
}

func run(inDir, out string, cellM float64, from, to string) error {
	fromT, err := monthTime(from, false)
	if err != nil {
		return err
	}
	toT, err := monthTime(to, true)
	if err != nil {
		return err
	}
	paths, err := filepath.Glob(filepath.Join(inDir, "*.london.ndjson.gz"))
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return fmt.Errorf("no extracts in %s; run `ingest-streetmanager -write` first", inDir)
	}
	sort.Strings(paths)

	ws := burden.Works{}
	var events, located int
	for _, p := range paths {
		n, loc, err := readExtract(p, ws)
		if err != nil {
			return fmt.Errorf("reading %s: %w", p, err)
		}
		events += n
		located += loc
	}
	fmt.Printf("loaded %d events (%d located) across %d files -> %d works\n", events, located, len(paths), len(ws))

	rows := ws.CellSeries(cellM, fromT, toT)
	pipeline := map[string]float64{}
	for _, p := range ws.CellPipelineSeries(cellM, fromT, toT) {
		pipeline[p.Month+"|"+p.Key] = p.WeightedDays
	}
	if err := writeCSV(out, rows, pipeline); err != nil {
		return err
	}
	summarise(rows, out, cellM)
	fmt.Printf("\n%s\n", streetmanager.Attribution)
	return nil
}

func readExtract(path string, ws burden.Works) (n, located int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return 0, 0, err
	}
	defer zr.Close()
	sc := bufio.NewScanner(zr)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		var r streetmanager.Record
		if json.Unmarshal(sc.Bytes(), &r) != nil {
			continue
		}
		ws.Apply(r)
		n++
	}
	for _, w := range ws {
		if w.Located {
			located++
		}
	}
	return n, located, sc.Err()
}

func writeCSV(out string, rows []burden.Row, pipeline map[string]float64) error {
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
	w.Write([]string{"month", "cell", "works", "work_days", "weighted_days", "planned_days", "emergency_days", "pipeline_weighted_days"})
	for _, r := range rows {
		w.Write([]string{
			r.Month, r.Key, strconv.Itoa(r.Works),
			f2(r.WorkDays), f2(r.WeightedDays), f2(r.PlannedDays), f2(r.EmergencyDays),
			f2(pipeline[r.Month+"|"+r.Key]),
		})
	}
	return w.Error()
}

func summarise(rows []burden.Row, out string, cellM float64) {
	if len(rows) == 0 {
		fmt.Println("no rows produced")
		return
	}
	months := map[string]bool{}
	cells := map[string]bool{}
	for _, r := range rows {
		months[r.Month] = true
		cells[r.Key] = true
	}
	fmt.Printf("\nwrote %s: %d rows, %d months (%s..%s), %d cells (%.0fkm)\n",
		out, len(rows), len(months), rows[0].Month, rows[len(rows)-1].Month, len(cells), cellM/1000)
}

func monthTime(s string, end bool) (time.Time, error) {
	t, err := time.Parse("2006-01", s)
	if err != nil {
		return time.Time{}, fmt.Errorf("month must be YYYY-MM, got %q", s)
	}
	if end {
		return t.AddDate(0, 1, 0).Add(-time.Second), nil
	}
	return t, nil
}

func f2(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }
