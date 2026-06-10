// Command build-accident-burden streams the DfT STATS19 road-collision dataset,
// filters to London, and writes a severity-weighted accident-burden series at the
// same 2km BNG grid cells used for works — the genuinely-unplanned (no-pipeline)
// incident core of the forecast. Output is derived external data: gitignored,
// cited in SOURCES.md.
package main

import (
	"bufio"
	"encoding/csv"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/umbralcalc/traffic-forecaster/internal/burden"
)

// DfT STATS19 collision file (OGL v3.0). The "last-5-years" file is a single
// convenient national download; per-year files follow the same schema.
const defaultURL = "https://data.dft.gov.uk/road-accidents-safety-data/dft-road-casualty-statistics-collision-last-5-years.csv"

// Greater London in British National Grid metres (generous bounding box).
const (
	minE, maxE = 500000.0, 565000.0
	minN, maxN = 155000.0, 205000.0
)

// severityWeight maps STATS19 collision_severity (1=Fatal,2=Serious,3=Slight) to
// a provisional burden weight.
func severityWeight(code string) float64 {
	switch code {
	case "1":
		return 1.0 // Fatal
	case "2":
		return 0.4 // Serious
	case "3":
		return 0.1 // Slight
	}
	return 0
}

func main() {
	url := flag.String("url", defaultURL, "STATS19 collision CSV URL")
	out := flag.String("out", filepath.Join("data", "incidents", "accident-burden.csv"), "output CSV path")
	cellKm := flag.Float64("cell-km", 2.0, "grid cell size in km")
	timeout := flag.Duration("timeout", 20*time.Minute, "overall deadline")
	flag.Parse()
	if err := run(*url, *out, *cellKm*1000, *timeout); err != nil {
		fmt.Fprintln(os.Stderr, "build-accident-burden:", err)
		os.Exit(1)
	}
}

func run(url, out string, cellM float64, timeout time.Duration) error {
	client := &http.Client{Timeout: timeout}
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("User-Agent", "traffic-forecaster (+https://github.com/umbralcalc/traffic-forecaster)")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: status %d", url, resp.StatusCode)
	}

	r := csv.NewReader(bufio.NewReaderSize(resp.Body, 1<<20))
	r.ReuseRecord = true
	r.FieldsPerRecord = -1
	header, err := r.Read()
	if err != nil {
		return err
	}
	col := map[string]int{}
	for i, h := range header {
		col[h] = i
	}
	ei, ni := col["location_easting_osgr"], col["location_northing_osgr"]
	si, di := col["collision_severity"], col["date"]
	if ei == 0 && ni == 0 {
		return fmt.Errorf("STATS19 columns not found in header")
	}

	// cell -> month -> (count, weighted)
	type cell struct {
		count    int
		weighted float64
	}
	grid := map[string]map[string]*cell{}
	var total, london int
	for {
		rec, err := r.Read()
		if err != nil {
			break
		}
		total++
		e, err1 := strconv.ParseFloat(rec[ei], 64)
		n, err2 := strconv.ParseFloat(rec[ni], 64)
		if err1 != nil || err2 != nil || e < minE || e > maxE || n < minN || n > maxN {
			continue
		}
		month, ok := monthOf(rec[di])
		if !ok {
			continue
		}
		w := severityWeight(rec[si])
		london++
		cid := burden.CellID(e, n, cellM)
		bm := grid[cid]
		if bm == nil {
			bm = map[string]*cell{}
			grid[cid] = bm
		}
		c := bm[month]
		if c == nil {
			c = &cell{}
			bm[month] = c
		}
		c.count++
		c.weighted += w
	}

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
	w.Write([]string{"month", "cell", "accidents", "weighted_days", "pipeline_weighted_days"})
	var rows int
	months := map[string]bool{}
	for cid, bm := range grid {
		for month, c := range bm {
			w.Write([]string{month, cid, strconv.Itoa(c.count),
				strconv.FormatFloat(c.weighted, 'f', 2, 64), "0"})
			rows++
			months[month] = true
		}
	}
	fmt.Printf("STATS19: %d collisions, %d in London; wrote %s: %d rows, %d cells, %d months\n",
		total, london, out, rows, len(grid), len(months))
	fmt.Println("\nContains public sector information licensed under the Open Government Licence v3.0 (DfT STATS19).")
	return nil
}

// monthOf parses a STATS19 date "DD/MM/YYYY" to "YYYY-MM".
func monthOf(s string) (string, bool) {
	if len(s) != 10 || s[2] != '/' || s[5] != '/' {
		return "", false
	}
	return s[6:10] + "-" + s[3:5], true
}
