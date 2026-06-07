// Command build-burden reconstructs the monthly per-borough works-burden series
// from local Street Manager London extracts (produced by
// `ingest-streetmanager -write`). It reduces lifecycle events to per-work
// windows, sums weighted work-days per borough-month, and writes a compact CSV.
//
// The output is derived from Street Manager (external) data, so it is gitignored
// and regenerated on demand rather than committed.
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
	out := flag.String("out", filepath.Join("data", "burden", "works-burden.csv"), "output CSV path")
	from := flag.String("from", "2020-01", "first month to include, YYYY-MM")
	to := flag.String("to", "2027-12", "last month to include, YYYY-MM")
	flag.Parse()

	if err := run(*inDir, *out, *from, *to); err != nil {
		fmt.Fprintln(os.Stderr, "build-burden:", err)
		os.Exit(1)
	}
}

func run(inDir, out, from, to string) error {
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
		return fmt.Errorf("no extracts in %s; run `ingest-streetmanager -write -month YYYY-MM` first", inDir)
	}
	sort.Strings(paths)

	ws := burden.Works{}
	var events int
	for _, p := range paths {
		n, err := readExtract(p, ws)
		if err != nil {
			return fmt.Errorf("reading %s: %w", p, err)
		}
		events += n
		fmt.Printf("  %-48s %9d events\n", filepath.Base(p), n)
	}
	fmt.Printf("loaded %d events across %d files -> %d distinct works\n", events, len(paths), len(ws))

	rows := ws.Series(fromT, toT)
	// Forward pipeline estimate (vintaged) joined on (month, borough).
	pipeline := map[string]float64{}
	for _, p := range ws.PipelineSeries(fromT, toT) {
		pipeline[p.Month+"|"+p.Borough] = p.WeightedDays
	}
	if err := writeCSV(out, rows, pipeline); err != nil {
		return err
	}
	summarise(rows, out)
	fmt.Printf("\n%s\n", streetmanager.Attribution)
	return nil
}

func readExtract(path string, ws burden.Works) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return 0, err
	}
	defer zr.Close()

	sc := bufio.NewScanner(zr)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024) // records carry geometry; allow long lines
	var n int
	for sc.Scan() {
		var r streetmanager.Record
		if json.Unmarshal(sc.Bytes(), &r) != nil {
			continue
		}
		ws.Apply(r)
		n++
	}
	return n, sc.Err()
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
	w.Write([]string{"month", "borough", "works", "work_days", "weighted_days", "planned_days", "emergency_days", "pipeline_weighted_days"})
	for _, r := range rows {
		w.Write([]string{
			r.Month, r.Borough,
			strconv.Itoa(r.Works),
			f2(r.WorkDays), f2(r.WeightedDays), f2(r.PlannedDays), f2(r.EmergencyDays),
			f2(pipeline[r.Month+"|"+r.Borough]),
		})
	}
	return w.Error()
}

func summarise(rows []burden.Row, out string) {
	if len(rows) == 0 {
		fmt.Println("no rows produced (check the month range and that extracts overlap it)")
		return
	}
	months := map[string]bool{}
	boroughs := map[string]bool{}
	var totWeighted, totPlanned, totEmergency float64
	for _, r := range rows {
		months[r.Month] = true
		boroughs[r.Borough] = true
		totWeighted += r.WeightedDays
		totPlanned += r.PlannedDays
		totEmergency += r.EmergencyDays
	}
	fmt.Printf("\nwrote %s: %d rows, %d months (%s..%s), %d boroughs\n",
		out, len(rows), len(months), rows[0].Month, rows[len(rows)-1].Month, len(boroughs))
	totDays := totPlanned + totEmergency
	if totDays > 0 {
		fmt.Printf("work-day mix: planned %.0f%%  emergency %.0f%%\n",
			100*totPlanned/totDays, 100*totEmergency/totDays)
	}

	top := append([]burden.Row(nil), rows...)
	sort.Slice(top, func(i, j int) bool { return top[i].WeightedDays > top[j].WeightedDays })
	fmt.Println("busiest borough-months by weighted work-days:")
	for i := 0; i < len(top) && i < 10; i++ {
		r := top[i]
		fmt.Printf("  %s  %-22s weighted=%8.0f  works=%-5d planned/emerg=%.0f/%.0f\n",
			r.Month, r.Borough, r.WeightedDays, r.Works, r.PlannedDays, r.EmergencyDays)
	}
}

func monthTime(s string, end bool) (time.Time, error) {
	t, err := time.Parse("2006-01", s)
	if err != nil {
		return time.Time{}, fmt.Errorf("month must be YYYY-MM, got %q", s)
	}
	if end {
		return t.AddDate(0, 1, 0).Add(-time.Second), nil // last instant of the month
	}
	return t, nil
}

func f2(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }
