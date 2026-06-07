// Command eda prints an exploratory read of the banked disruption snapshots and
// the live severity vocabulary. It answers the go/no-go questions: how large and
// how concentrated the disruption stock is, the planned/unplanned split, how
// much forward visibility the feed gives, and how long disruptions last.
//
// With a single snapshot this is necessarily cross-sectional (the standing stock
// right now); the time-series queries from PLAN.md (weekly base rates, lifecycle
// churn, seasonality) need an accumulated archive and are reported as pending.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/umbralcalc/traffic-forecaster/internal/disruptions"
	"github.com/umbralcalc/traffic-forecaster/internal/store"
	"github.com/umbralcalc/traffic-forecaster/internal/tfl"
)

func main() {
	rawDir := flag.String("raw", filepath.Join("data", "raw"), "directory of daily snapshots")
	flag.Parse()
	if err := run(*rawDir); err != nil {
		fmt.Fprintln(os.Stderr, "eda:", err)
		os.Exit(1)
	}
}

func run(rawDir string) error {
	paths, err := filepath.Glob(filepath.Join(rawDir, "*.json*"))
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return fmt.Errorf("no snapshots in %s; run cmd/pull-disruptions first", rawDir)
	}
	sort.Strings(paths)
	fmt.Printf("snapshots available: %d (%s .. %s)\n", len(paths),
		snapDate(paths[0]), snapDate(paths[len(paths)-1]))
	if len(paths) < 2 {
		fmt.Println("NOTE: only one snapshot — all figures below are cross-sectional (today's stock),")
		fmt.Println("      not flow/seasonality. Time-series EDA pending an accumulated archive.")
	}

	// Live severity vocabulary, for the resolution filter and a cross-check.
	if client := tfl.NewClient(os.Getenv("TFL_APP_KEY"), time.Now().UnixNano()); true {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if sevs, err := client.Severities(ctx); err == nil {
			fmt.Println("\n== severity vocabulary (live /Road/Meta/Severities) ==")
			for _, s := range sevs {
				rank, _ := disruptions.SeverityRank(s.Description)
				fmt.Printf("  level=%-2d rank=%d  %s\n", s.SeverityLevel, rank, s.Description)
			}
		} else {
			fmt.Printf("\n(could not fetch live severities: %v)\n", err)
		}
	}

	// Analyse the latest snapshot in detail.
	latest := paths[len(paths)-1]
	snap, err := store.Read(latest)
	if err != nil {
		return err
	}
	ds, skipped := disruptions.Decode(snap)
	fmt.Printf("\n== latest snapshot: %s (captured %s) ==\n", snapDate(latest), snap.CapturedAt.Format(time.RFC3339))
	fmt.Printf("records: %d decoded, %d skipped\n", len(ds), skipped)

	analyseSeverity(ds)
	analysePlanned(ds)
	analyseGeography(ds)
	analyseDuration(ds, snap.CapturedAt)
	analyseForwardVisibility(ds, snap.CapturedAt)
	analyseTargetSparsity(ds)
	return nil
}

func analyseSeverity(ds []tfl.RoadDisruption) {
	fmt.Println("\n== severity mix (the target is a count above some threshold) ==")
	bySev := map[string]int{}
	for _, d := range ds {
		bySev[d.Severity]++
	}
	for _, kv := range sortedCounts(bySev) {
		rank, known := disruptions.SeverityRank(kv.key)
		flag := ""
		if !known {
			flag = "  (UNRECOGNISED)"
		}
		fmt.Printf("  %-22s %3d   (rank %d)%s\n", kv.key, kv.n, rank, flag)
	}
}

func analysePlanned(ds []tfl.RoadDisruption) {
	fmt.Println("\n== planned vs unplanned (drives how much is forecastable vs deterministic) ==")
	var planned, unplanned int
	byCat := map[string]int{}
	for _, d := range ds {
		byCat[d.Category]++
		if disruptions.IsPlanned(d.Category) {
			planned++
		} else {
			unplanned++
		}
	}
	total := len(ds)
	fmt.Printf("  planned   : %3d (%.0f%%)\n", planned, pct(planned, total))
	fmt.Printf("  unplanned : %3d (%.0f%%)\n", unplanned, pct(unplanned, total))
	fmt.Println("  by category:")
	for _, kv := range sortedCounts(byCat) {
		tag := "unplanned"
		if disruptions.IsPlanned(kv.key) {
			tag = "planned"
		}
		fmt.Printf("    %-30s %3d  [%s]\n", kv.key, kv.n, tag)
	}
}

func analyseGeography(ds []tfl.RoadDisruption) {
	fmt.Println("\n== geography (corridor coverage vs borough coverage) ==")
	var noCorridor, noBorough int
	byBorough := map[string]int{}
	for _, d := range ds {
		if len(d.CorridorIds) == 0 {
			noCorridor++
		}
		bs := disruptions.Boroughs(d.Location)
		if len(bs) == 0 {
			noBorough++
		}
		for _, b := range bs {
			byBorough[b]++
		}
	}
	total := len(ds)
	fmt.Printf("  records with NO corridorIds : %3d (%.0f%%)\n", noCorridor, pct(noCorridor, total))
	fmt.Printf("  records with NO parseable borough: %3d (%.0f%%)\n", noBorough, pct(noBorough, total))
	fmt.Printf("  distinct boroughs: %d\n", len(byBorough))
	fmt.Println("  busiest boroughs (this snapshot):")
	for i, kv := range sortedCounts(byBorough) {
		if i >= 10 {
			break
		}
		fmt.Printf("    %-28s %3d\n", kv.key, kv.n)
	}
}

func analyseDuration(ds []tfl.RoadDisruption, asOf time.Time) {
	fmt.Println("\n== duration (stock vs flow: are these brief events or long-running works?) ==")
	var days []float64
	for _, d := range ds {
		if d.StartDateTime.IsZero() || d.EndDateTime.IsZero() || !d.EndDateTime.After(d.StartDateTime) {
			continue
		}
		days = append(days, d.EndDateTime.Sub(d.StartDateTime).Hours()/24)
	}
	if len(days) == 0 {
		fmt.Println("  (no records with usable start/end dates)")
		return
	}
	sort.Float64s(days)
	fmt.Printf("  usable date ranges: %d\n", len(days))
	fmt.Printf("  duration days  p25=%.0f  median=%.0f  p75=%.0f  max=%.0f\n",
		quantile(days, .25), quantile(days, .5), quantile(days, .75), days[len(days)-1])
}

func analyseForwardVisibility(ds []tfl.RoadDisruption, asOf time.Time) {
	fmt.Println("\n== forward visibility (can we SEE planned works before they start?) ==")
	var futureStart, openNow, endFuture int
	for _, d := range ds {
		if d.StartDateTime.After(asOf) {
			futureStart++
		}
		if d.EndDateTime.After(asOf) {
			endFuture++
		}
		if !d.StartDateTime.After(asOf) && d.EndDateTime.After(asOf) {
			openNow++
		}
	}
	fmt.Printf("  records starting in the FUTURE (pre-announced): %d\n", futureStart)
	fmt.Printf("  records active now (started<=now<end):          %d\n", openNow)
	fmt.Printf("  records with end date in the future:            %d\n", endFuture)
	if futureStart == 0 {
		fmt.Println("  => the feed announces NO future start dates: 'planned works as a forward")
		fmt.Println("     covariate' is only available via CONTINUATION of already-started works.")
	}
}

// analyseTargetSparsity shows how the borough-month count distribution thins out
// as the severity threshold rises — the core forecastability question.
func analyseTargetSparsity(ds []tfl.RoadDisruption) {
	fmt.Println("\n== target sparsity (counts per borough at each severity threshold) ==")
	thresholds := []struct {
		name string
		rank int
	}{
		{"Minimal+ (rank>=2)", 2},
		{"Moderate+ (rank>=3)", 3},
		{"Serious+ (rank>=4)", 4},
	}
	for _, th := range thresholds {
		perBorough := map[string]int{}
		var total int
		for _, d := range ds {
			r, _ := disruptions.SeverityRank(d.Severity)
			if r < th.rank {
				continue
			}
			total++
			for _, b := range disruptions.Boroughs(d.Location) {
				perBorough[b]++
			}
		}
		fmt.Printf("  %-22s total=%-3d boroughs-with-any=%-2d busiest=%d\n",
			th.name, total, len(perBorough), maxCount(perBorough))
	}
	fmt.Println("  (low totals per borough => mostly 0/1 counts => sparse; favours borough")
	fmt.Println("   aggregation and a low threshold, or a coarser target.)")
}

// --- small helpers ---

type kv struct {
	key string
	n   int
}

func sortedCounts(m map[string]int) []kv {
	out := make([]kv, 0, len(m))
	for k, n := range m {
		out = append(out, kv{k, n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].n != out[j].n {
			return out[i].n > out[j].n
		}
		return out[i].key < out[j].key
	})
	return out
}

func maxCount(m map[string]int) int {
	max := 0
	for _, n := range m {
		if n > max {
			max = n
		}
	}
	return max
}

func quantile(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	i := int(q * float64(len(sorted)-1))
	return sorted[i]
}

func pct(n, total int) float64 {
	if total == 0 {
		return 0
	}
	return 100 * float64(n) / float64(total)
}

func snapDate(path string) string {
	b := filepath.Base(path)
	if i := len(b); i > 0 {
		if dot := indexByte(b, '.'); dot > 0 {
			return b[:dot]
		}
	}
	return b
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}
