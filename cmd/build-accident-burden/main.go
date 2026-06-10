// Command build-accident-burden streams the DfT STATS19 road-collision dataset,
// filters to London, and writes a per-cell monthly collision series — the core of
// the road-safety-rating product. Each cell-month carries the all-severity
// collision count and the KSI count (killed or seriously injured, the standard
// road-safety tier); these are the Poisson targets the safety rating is built on.
//
// The cell size defaults to 1km: a resolution sweep (cmd/grid-sweep) showed the
// hierarchical model's skill over a naive base-rate rises as cells get finer
// (pooling beats sparse per-cell means), and 1km keeps strong skill and
// calibration while staying robust to STATS19 geocoding precision.
//
// Output is derived external data: gitignored, cited in SOURCES.md.
package main

import (
	"bufio"
	"encoding/csv"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/umbralcalc/traffic-forecaster/internal/grid"
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
// a provisional weight (kept for the optional weighted column / dashboards).
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

// isKSI reports whether a collision is Killed-or-Seriously-Injured (severity 1 or
// 2) — the standard UK road-safety tier and our 'serious' rating's target.
func isKSI(code string) bool { return code == "1" || code == "2" }

func main() {
	url := flag.String("url", defaultURL, "STATS19 collision CSV URL")
	out := flag.String("out", filepath.Join("data", "incidents", "accident-burden.csv"), "output CSV path")
	cellKm := flag.Float64("cell-km", 1.0, "grid cell size in km")
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

	// cell -> month -> (all count, KSI count, severity-weighted)
	type cell struct {
		count    int
		ksi      int
		weighted float64
	}
	byCell := map[string]map[string]*cell{}
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
		cid := grid.CellID(e, n, cellM)
		bm := byCell[cid]
		if bm == nil {
			bm = map[string]*cell{}
			byCell[cid] = bm
		}
		c := bm[month]
		if c == nil {
			c = &cell{}
			bm[month] = c
		}
		c.count++
		if isKSI(rec[si]) {
			c.ksi++
		}
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
	w.Write([]string{"month", "cell", "accidents", "ksi", "weighted_days"})

	// Emit the DENSE cell × month panel: every cell that ever sees a collision is
	// "at risk" in every month, so a month with none is a real zero observation
	// (the strongest safety signal) — not a missing row. The Poisson intensity
	// model needs those zeros. The observed-month set is the contiguous range,
	// since London as a whole has collisions every month.
	monthSet := map[string]bool{}
	for _, bm := range byCell {
		for month := range bm {
			monthSet[month] = true
		}
	}
	months := make([]string, 0, len(monthSet))
	for m := range monthSet {
		months = append(months, m)
	}
	sort.Strings(months)
	cells := make([]string, 0, len(byCell))
	for cid := range byCell {
		cells = append(cells, cid)
	}
	sort.Strings(cells)

	var rows int
	for _, cid := range cells {
		bm := byCell[cid]
		for _, month := range months {
			c := bm[month]
			if c == nil {
				c = &cell{}
			}
			w.Write([]string{month, cid, strconv.Itoa(c.count), strconv.Itoa(c.ksi),
				strconv.FormatFloat(c.weighted, 'f', 2, 64)})
			rows++
		}
	}
	fmt.Printf("STATS19: %d collisions, %d in London; wrote %s: %d rows (dense), %d cells, %d months\n",
		total, london, out, rows, len(cells), len(months))
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
