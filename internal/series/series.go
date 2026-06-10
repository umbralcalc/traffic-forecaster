// Package series loads a burden CSV (as written by the build-*-burden commands)
// into the per-entity Point series the forecaster consumes. Unlike the backtest
// loader it keeps ALL months, including the forward-pipeline tail, so a forecast
// can read the target month's known pipeline.
package series

import (
	"encoding/csv"
	"fmt"
	"os"
	"strconv"

	"github.com/umbralcalc/traffic-forecaster/internal/forecast"
)

// Loaded is a burden series plus the unit→borough grouping (when a borough
// column is present).
type Loaded struct {
	Series map[string][]forecast.Point
	Group  map[string]string
}

// Load reads the CSV at path, taking valueCol as the burden value and entityCol
// as the spatial unit. It also reads pipeline_weighted_days / planned_days /
// emergency_days and an optional borough column when present.
func Load(path, valueCol, entityCol string) (*Loaded, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	r := csv.NewReader(f)
	header, err := r.Read()
	if err != nil {
		return nil, err
	}
	idx := map[string]int{}
	for i, h := range header {
		idx[h] = i
	}
	for _, need := range []string{"month", entityCol, valueCol} {
		if _, ok := idx[need]; !ok {
			return nil, fmt.Errorf("CSV %s missing column %q", path, need)
		}
	}
	boroughCol, hasBorough := idx["borough"]

	out := &Loaded{Series: map[string][]forecast.Point{}, Group: map[string]string{}}
	for {
		rec, err := r.Read()
		if err != nil {
			break
		}
		y, m, err := ParseMonth(rec[idx["month"]])
		if err != nil {
			continue
		}
		v, err := strconv.ParseFloat(rec[idx[valueCol]], 64)
		if err != nil {
			continue
		}
		ent := rec[idx[entityCol]]
		if hasBorough && boroughCol < len(rec) {
			out.Group[ent] = rec[boroughCol]
		}
		out.Series[ent] = append(out.Series[ent], forecast.Point{
			Year: y, Month: m, Value: v,
			Pipeline:  optFloat(rec, idx, "pipeline_weighted_days"),
			Planned:   optFloat(rec, idx, "planned_days"),
			Emergency: optFloat(rec, idx, "emergency_days"),
		})
	}
	return out, nil
}

// MonthIndex is the canonical sortable month key (months since year 0).
func MonthIndex(year, month int) int { return year*12 + (month - 1) }

// ParseMonth parses "YYYY-MM".
func ParseMonth(s string) (year, month int, err error) {
	if len(s) != 7 || s[4] != '-' {
		return 0, 0, fmt.Errorf("bad month %q", s)
	}
	y, e1 := strconv.Atoi(s[:4])
	m, e2 := strconv.Atoi(s[5:])
	if e1 != nil || e2 != nil || m < 1 || m > 12 {
		return 0, 0, fmt.Errorf("bad month %q", s)
	}
	return y, m, nil
}

func optFloat(rec []string, idx map[string]int, name string) float64 {
	if ci, ok := idx[name]; ok && ci < len(rec) {
		v, _ := strconv.ParseFloat(rec[ci], 64)
		return v
	}
	return 0
}
