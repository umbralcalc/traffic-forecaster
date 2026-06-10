// Command resolve scores a previously-committed prediction against the realised
// burden for that month (once the Street Manager archive has settled). It writes
// a resolution file (per-unit CRPS/PIT, the London-total score, and a running
// calibration summary) to data/resolutions/. Committing it in a later commit than
// the prediction is the proof that we predicted before we knew.
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

	"github.com/umbralcalc/traffic-forecaster/internal/scoring"
	"github.com/umbralcalc/traffic-forecaster/internal/series"
)

// prediction mirrors the fields cmd/forecast writes that we need to score.
type prediction struct {
	TargetMonth string `json:"target_month"`
	LondonTotal struct {
		Ensemble []float64 `json:"ensemble"`
	} `json:"london_total"`
	Units []struct {
		Unit     string    `json:"unit"`
		Ensemble []float64 `json:"ensemble"`
	} `json:"units"`
}

type unitScore struct {
	Unit     string  `json:"unit"`
	Realised float64 `json:"realised"`
	CRPS     float64 `json:"crps"`
	PIT      float64 `json:"pit"`
}

type resolution struct {
	TargetMonth    string      `json:"target_month"`
	ResolvedAt     string      `json:"resolved_at"`
	NUnits         int         `json:"n_units"`
	MeanCRPS       float64     `json:"mean_crps"`
	CalUniformity  float64     `json:"calibration_uniformity"`
	CalibrationPIT []float64   `json:"calibration_pit_bins"`
	Total          unitScore   `json:"london_total"`
	Units          []unitScore `json:"units"`
}

func main() {
	in := flag.String("in", filepath.Join("data", "burden", "hybrid-n150.csv"), "hybrid burden series CSV (with the realised target month)")
	month := flag.String("month", "", "month to resolve YYYY-MM (required)")
	predDir := flag.String("predictions", filepath.Join("data", "predictions"), "predictions directory")
	out := flag.String("out", filepath.Join("data", "resolutions"), "resolutions directory")
	flag.Parse()

	if err := run(*in, *month, *predDir, *out); err != nil {
		fmt.Fprintln(os.Stderr, "resolve:", err)
		os.Exit(1)
	}
}

func run(in, month, predDir, out string) error {
	if month == "" {
		return fmt.Errorf("-month is required (YYYY-MM)")
	}
	ty, tm, err := series.ParseMonth(month)
	if err != nil {
		return err
	}
	targetIdx := series.MonthIndex(ty, tm)

	// Realised burden per unit at the target month (absent row => 0 works).
	loaded, err := series.Load(in, "weighted_days", "unit")
	if err != nil {
		return err
	}
	realised := map[string]float64{}
	for unit, pts := range loaded.Series {
		for _, p := range pts {
			if series.MonthIndex(p.Year, p.Month) == targetIdx {
				realised[unit] = p.Value
			}
		}
	}

	var pred prediction
	pb, err := os.ReadFile(filepath.Join(predDir, month+".json"))
	if err != nil {
		return fmt.Errorf("reading prediction: %w", err)
	}
	if err := json.Unmarshal(pb, &pred); err != nil {
		return err
	}

	res := resolution{TargetMonth: month, ResolvedAt: time.Now().UTC().Format(time.RFC3339)}
	cal := scoring.NewCalibration(10)
	var sumCRPS, realisedTotal float64
	for _, u := range pred.Units {
		if len(u.Ensemble) == 0 {
			continue
		}
		y := realised[u.Unit] // 0 if the unit had no works that month
		crps := scoring.CRPS(u.Ensemble, y)
		pit := scoring.PIT(u.Ensemble, y)
		sumCRPS += crps
		realisedTotal += y
		cal.Add(pit)
		res.Units = append(res.Units, unitScore{Unit: u.Unit, Realised: round2(y), CRPS: round2(crps), PIT: round2(pit)})
	}
	sort.Slice(res.Units, func(i, j int) bool { return res.Units[i].Unit < res.Units[j].Unit })
	res.NUnits = len(res.Units)
	if res.NUnits > 0 {
		res.MeanCRPS = round2(sumCRPS / float64(res.NUnits))
	}
	res.CalUniformity = round2(cal.Uniformity())
	res.CalibrationPIT = roundAll(cal.Bins())
	res.Total = unitScore{
		Unit: "LONDON_TOTAL", Realised: round2(realisedTotal),
		CRPS: round2(scoring.CRPS(pred.LondonTotal.Ensemble, realisedTotal)),
		PIT:  round2(scoring.PIT(pred.LondonTotal.Ensemble, realisedTotal)),
	}

	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	path := filepath.Join(out, month+".json")
	data, _ := json.MarshalIndent(res, "", " ")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %s: %d units resolved\n", path, res.NUnits)
	fmt.Printf("  mean per-unit CRPS: %.2f   calibration (0=ideal): %.2f\n", res.MeanCRPS, res.CalUniformity)
	fmt.Printf("  London total: realised %.0f  CRPS %.0f  PIT %.2f\n", res.Total.Realised, res.Total.CRPS, res.Total.PIT)
	return nil
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

func roundAll(xs []float64) []float64 {
	out := make([]float64, len(xs))
	for i, v := range xs {
		out[i] = round2(v)
	}
	return out
}
