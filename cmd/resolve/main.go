// Command resolve settles a committed safety-rating prediction against realised
// STATS19 collisions once a release covering the target month exists. It scores
// the published quantities per tier — Brier and log-loss on P(incident), Poisson
// deviance on the expected count, and a reliability curve / calibration error —
// and writes the result to data/resolutions/. It refuses to settle a month the
// realised panel does not yet contain, and asserts the prediction was a genuine
// forward one (target strictly after its train-through month).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/umbralcalc/traffic-forecaster/internal/scoring"
	"github.com/umbralcalc/traffic-forecaster/internal/series"
)

// prediction mirrors the committed schema (only the fields resolve needs).
type prediction struct {
	TargetMonth  string `json:"target_month"`
	Model        string `json:"model"`
	TrainThrough string `json:"train_through"`
	Accidents    tier   `json:"accidents"`
	KSI          tier   `json:"ksi"`
}
type tier struct {
	ExpectedTotal float64    `json:"expected_total"`
	TotalP5       float64    `json:"total_p5"`
	TotalP95      float64    `json:"total_p95"`
	Cells         []cellPred `json:"cells"`
}
type cellPred struct {
	Cell     string  `json:"cell"`
	Rating   float64 `json:"rating"`
	Expected float64 `json:"expected"`
}

type tierResult struct {
	Tier            string      `json:"tier"`
	N               int         `json:"cells_scored"`
	Brier           float64     `json:"brier"`
	LogLoss         float64     `json:"logloss"`
	Deviance        float64     `json:"poisson_deviance"`
	CalErr          float64     `json:"calibration_error"`
	RealisedTotal   int         `json:"realised_total"`
	PredTotalP5     float64     `json:"pred_total_p5"`
	PredTotalP95    float64     `json:"pred_total_p95"`
	TotalInInterval bool        `json:"total_within_90pct"`
	Reliability     [][]float64 `json:"reliability"` // [meanP, freq, count] rows
}

type resolution struct {
	TargetMonth    string     `json:"target_month"`
	Model          string     `json:"model"`
	TrainThrough   string     `json:"train_through"`
	RealisedSource string     `json:"realised_source"`
	Accidents      tierResult `json:"accidents"`
	KSI            tierResult `json:"ksi"`
}

func main() {
	pred := flag.String("pred", "", "committed prediction JSON (required)")
	realised := flag.String("realised", filepath.Join("data", "incidents", "accident-burden.csv"), "realised panel CSV (a STATS19 vintage covering the target month)")
	out := flag.String("out", filepath.Join("data", "resolutions"), "resolutions directory")
	flag.Parse()
	if *pred == "" {
		fmt.Fprintln(os.Stderr, "resolve: -pred is required")
		os.Exit(1)
	}
	if err := run(*pred, *realised, *out); err != nil {
		fmt.Fprintln(os.Stderr, "resolve:", err)
		os.Exit(1)
	}
}

func run(predPath, realisedPath, out string) error {
	var p prediction
	raw, err := os.ReadFile(predPath)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return err
	}

	ty, tm, err := series.ParseMonth(p.TargetMonth)
	if err != nil {
		return err
	}
	if ttY, ttM, err := series.ParseMonth(p.TrainThrough); err == nil {
		if series.MonthIndex(ty, tm) <= series.MonthIndex(ttY, ttM) {
			return fmt.Errorf("target %s is not after train-through %s — not a forward prediction (leakage)", p.TargetMonth, p.TrainThrough)
		}
	}

	realAcc, maxMonth, err := realisedMonth(realisedPath, "accidents", ty, tm)
	if err != nil {
		return err
	}
	if realAcc == nil {
		return fmt.Errorf("realised data ends %s — cannot settle %s yet (no STATS19 release covers it)", maxMonth, p.TargetMonth)
	}
	realKSI, _, err := realisedMonth(realisedPath, "ksi", ty, tm)
	if err != nil {
		return err
	}

	res := resolution{
		TargetMonth:    p.TargetMonth,
		Model:          p.Model,
		TrainThrough:   p.TrainThrough,
		RealisedSource: filepath.Base(realisedPath),
		Accidents:      score("accidents", p.Accidents, realAcc),
		KSI:            score("ksi", p.KSI, realKSI),
	}

	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	path := filepath.Join(out, p.TargetMonth+".json")
	data, err := json.MarshalIndent(res, "", " ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	fmt.Printf("settled %s -> %s\n", p.TargetMonth, path)
	for _, r := range []tierResult{res.Accidents, res.KSI} {
		fmt.Printf("  %-10s Brier %.4f  logloss %.4f  Pois.dev %.3f  cal.err %.3f  | total realised %d in 90%% [%.0f,%.0f]=%v\n",
			r.Tier, r.Brier, r.LogLoss, r.Deviance, r.CalErr, r.RealisedTotal, r.PredTotalP5, r.PredTotalP95, r.TotalInInterval)
	}
	return nil
}

func score(name string, t tier, realised map[string]int) tierResult {
	rel := scoring.NewReliability(10)
	var brier, logloss, deviance float64
	var nn, realTotal int
	for _, c := range t.Cells {
		y := realised[c.Cell] // absent => 0 (a real zero in the dense panel)
		occurred := y > 0
		pInc := 1 - c.Rating
		brier += scoring.Brier(pInc, occurred)
		logloss += scoring.LogLoss(pInc, occurred)
		deviance += scoring.PoissonDeviance(c.Expected, y)
		rel.Add(pInc, occurred)
		realTotal += y
		nn++
	}
	if nn > 0 {
		brier /= float64(nn)
		logloss /= float64(nn)
		deviance /= float64(nn)
	}
	mp, fr, ct := rel.Curve()
	curve := make([][]float64, len(mp))
	for i := range mp {
		curve[i] = []float64{round(mp[i], 3), round(fr[i], 3), ct[i]}
	}
	return tierResult{
		Tier: name, N: nn,
		Brier: round(brier, 4), LogLoss: round(logloss, 4), Deviance: round(deviance, 4),
		CalErr:        round(rel.CalibrationError(), 4),
		RealisedTotal: realTotal,
		PredTotalP5:   t.TotalP5, PredTotalP95: t.TotalP95,
		TotalInInterval: float64(realTotal) >= t.TotalP5 && float64(realTotal) <= t.TotalP95,
		Reliability:     curve,
	}
}

// realisedMonth loads the realised counts for the target month from a panel CSV,
// returning nil (and the panel's max month) when the month is not yet present.
func realisedMonth(path, col string, ty, tm int) (map[string]int, string, error) {
	loaded, err := series.Load(path, col, "cell")
	if err != nil {
		return nil, "", err
	}
	target := series.MonthIndex(ty, tm)
	maxIdx := 0
	got := map[string]int{}
	for cell, pts := range loaded.Series {
		for _, p := range pts {
			k := series.MonthIndex(p.Year, p.Month)
			if k > maxIdx {
				maxIdx = k
			}
			if k == target {
				got[cell] = int(p.Value + 0.5)
			}
		}
	}
	if len(got) == 0 {
		return nil, fmt.Sprintf("%04d-%02d", maxIdx/12, maxIdx%12+1), nil
	}
	return got, "", nil
}

func round(v float64, dp int) float64 {
	p := 1.0
	for i := 0; i < dp; i++ {
		p *= 10
	}
	return float64(int(v*p+0.5)) / p
}
