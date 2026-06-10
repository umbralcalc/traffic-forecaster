// Command forecast commits the road-safety rating for future months: it trains
// the Poisson model on collision history up to a chosen month and writes, per
// target month, the per-cell rating S = P(no collision) plus the expected count
// and the London total, for both tiers (all collisions + KSI). The committed
// files in data/predictions/ are the proof-of-commit record — frozen before the
// realised STATS19 data for those months exists, then settled later by
// cmd/resolve. Predictions are summaries (no raw ensembles) so they stay small
// enough to commit to git.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/umbralcalc/traffic-forecaster/internal/safety"
	"github.com/umbralcalc/traffic-forecaster/internal/series"
)

type cellPred struct {
	Cell     string  `json:"cell"`
	Rating   float64 `json:"rating"`   // S = P(no collision this month)
	Expected float64 `json:"expected"` // E[count]
	P95      int     `json:"p95"`      // 95th-percentile "bad month" count
}

type tierPred struct {
	ExpectedTotal float64    `json:"expected_total"`
	TotalP5       float64    `json:"total_p5"`
	TotalP50      float64    `json:"total_p50"`
	TotalP95      float64    `json:"total_p95"`
	Cells         []cellPred `json:"cells"`
}

type prediction struct {
	TargetMonth  string   `json:"target_month"`
	MadeAt       string   `json:"made_at"`
	Model        string   `json:"model"`
	CellKm       float64  `json:"cell_km"`
	N            int      `json:"n"`
	Seed         uint64   `json:"seed"`
	TrainThrough string   `json:"train_through"`
	TrainSource  string   `json:"train_source"`
	SettleNote   string   `json:"settle_note"`
	Accidents    tierPred `json:"accidents"`
	KSI          tierPred `json:"ksi"`
}

func main() {
	in := flag.String("in", filepath.Join("data", "incidents", "accident-burden.csv"), "collision panel CSV")
	trainThrough := flag.String("train-through", "", "last month to train on YYYY-MM (default: last month in panel)")
	from := flag.String("from", "", "first target month YYYY-MM (default: month after train-through)")
	to := flag.String("to", "", "last target month YYYY-MM (default: 12 months after train-through)")
	cellKm := flag.Float64("cell-km", 1.0, "cell size the panel was built at (recorded in the prediction)")
	n := flag.Int("n", 400, "ensemble size")
	seed := flag.Uint64("seed", 1, "RNG seed")
	out := flag.String("out", filepath.Join("data", "predictions"), "predictions directory")
	flag.Parse()
	if err := run(*in, *trainThrough, *from, *to, *cellKm, *n, *seed, *out); err != nil {
		fmt.Fprintln(os.Stderr, "forecast:", err)
		os.Exit(1)
	}
}

func run(in, trainThrough, from, to string, cellKm float64, n int, seed uint64, out string) error {
	all, err := series.Load(in, "accidents", "cell")
	if err != nil {
		return err
	}
	ksi, err := series.Load(in, "ksi", "cell")
	if err != nil {
		return err
	}

	// Default train-through = the last month present in the panel.
	lastIdx := 0
	for _, pts := range all.Series {
		for _, p := range pts {
			if k := series.MonthIndex(p.Year, p.Month); k > lastIdx {
				lastIdx = k
			}
		}
	}
	if trainThrough != "" {
		ty, tm, err := series.ParseMonth(trainThrough)
		if err != nil {
			return err
		}
		lastIdx = series.MonthIndex(ty, tm)
	}
	trainThrough = monthStr(lastIdx)

	// Default target range = the 12 months following train-through.
	fromIdx, toIdx := lastIdx+1, lastIdx+12
	if from != "" {
		y, m, err := series.ParseMonth(from)
		if err != nil {
			return err
		}
		fromIdx = series.MonthIndex(y, m)
	}
	if to != "" {
		y, m, err := series.ParseMonth(to)
		if err != nil {
			return err
		}
		toIdx = series.MonthIndex(y, m)
	}
	if fromIdx <= lastIdx {
		return fmt.Errorf("target %s is not after train-through %s — that is not a forward prediction", monthStr(fromIdx), trainThrough)
	}

	histAll := historyThrough(all.Series, lastIdx)
	histKSI := historyThrough(ksi.Series, lastIdx)

	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	for idx := fromIdx; idx <= toIdx; idx++ {
		ty, tm := idx/12, idx%12+1
		pred := prediction{
			TargetMonth:  monthStr(idx),
			MadeAt:       time.Now().UTC().Format(time.RFC3339),
			Model:        safety.PoissonFactorModel{}.Name(),
			CellKm:       cellKm,
			N:            n,
			Seed:         seed,
			TrainThrough: trainThrough,
			TrainSource:  filepath.Base(in),
			SettleNote:   "settle against the first STATS19 release containing " + monthStr(idx),
			Accidents:    tierFor(histAll, ty, tm, n, seed),
			KSI:          tierFor(histKSI, ty, tm, n, seed),
		}
		path := filepath.Join(out, pred.TargetMonth+".json")
		data, err := json.Marshal(pred) // compact: these are committed proof artifacts
		if err != nil {
			return err
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return err
		}
		fmt.Printf("wrote %s  (acc total ~%.0f, KSI total ~%.0f; trained <= %s)\n",
			path, pred.Accidents.ExpectedTotal, pred.KSI.ExpectedTotal, trainThrough)
	}
	return nil
}

func tierFor(hist map[string][]series.Point, ty, tm, n int, seed uint64) tierPred {
	preds := safety.PoissonFactorModel{N: n, Seed: seed}.PredictAll(hist, ty, tm)
	cells := make([]string, 0, len(preds))
	for c := range preds {
		cells = append(cells, c)
	}
	sort.Strings(cells)

	total := make([]float64, n)
	var expTotal float64
	out := tierPred{Cells: make([]cellPred, 0, len(cells))}
	for _, c := range cells {
		p := preds[c]
		out.Cells = append(out.Cells, cellPred{Cell: c, Rating: round(p.Rating, 4), Expected: round(p.Expected, 4), P95: p.P95Count})
		expTotal += p.Expected
		for k, v := range p.Ensemble {
			total[k] += float64(v)
		}
	}
	sort.Float64s(total)
	out.ExpectedTotal = round(expTotal, 1)
	out.TotalP5 = quantile(total, 0.05)
	out.TotalP50 = quantile(total, 0.50)
	out.TotalP95 = quantile(total, 0.95)
	return out
}

func historyThrough(panel map[string][]series.Point, lastIdx int) map[string][]series.Point {
	out := make(map[string][]series.Point, len(panel))
	for cell, pts := range panel {
		var h []series.Point
		for _, p := range pts {
			if series.MonthIndex(p.Year, p.Month) <= lastIdx {
				h = append(h, p)
			}
		}
		out[cell] = h
	}
	return out
}

func quantile(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	return sorted[int(q*float64(len(sorted)-1))]
}

func round(v float64, dp int) float64 {
	p := 1.0
	for i := 0; i < dp; i++ {
		p *= 10
	}
	return float64(int(v*p+0.5)) / p
}

func monthStr(idx int) string { return fmt.Sprintf("%04d-%02d", idx/12, idx%12+1) }
