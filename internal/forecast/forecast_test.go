package forecast

import "testing"

func mkSeries(vals []float64, startYear, startMonth int) []Point {
	pts := make([]Point, len(vals))
	y, m := startYear, startMonth
	for i, v := range vals {
		pts[i] = Point{Year: y, Month: m, Value: v}
		m++
		if m > 12 {
			m = 1
			y++
		}
	}
	return pts
}

func TestSeasonalPicksSameMonth(t *testing.T) {
	// 24 months; same-month-prior-year values are distinctive.
	hist := mkSeries([]float64{
		10, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, // year 1, Jan=10
		20, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, // year 2, Jan=20
	}, 2021, 1)
	target := Point{Year: 2023, Month: 1} // predict a January
	samples := Seasonal{FallbackK: 6}.Predict(hist, target)
	if len(samples) != 2 || samples[0] != 10 || samples[1] != 20 {
		t.Fatalf("seasonal samples = %v, want [10 20]", samples)
	}
}

func TestSeasonalFallsBackWhenNoSameMonth(t *testing.T) {
	hist := mkSeries([]float64{1, 2, 3}, 2021, 1) // Jan,Feb,Mar
	target := Point{Year: 2021, Month: 7}         // July: no prior July
	samples := Seasonal{FallbackK: 2}.Predict(hist, target)
	if len(samples) != 2 || samples[0] != 2 || samples[1] != 3 {
		t.Fatalf("fallback samples = %v, want last 2 [2 3]", samples)
	}
}

func TestRecentWindowAbstainsOnEmpty(t *testing.T) {
	if s := (RecentWindow{K: 6}).Predict(nil, Point{}); s != nil {
		t.Errorf("expected abstain on empty history, got %v", s)
	}
}

func TestPipelineResidual(t *testing.T) {
	// History: realised exceeds pipeline by +5 and +7 (emergency/overrun gap).
	hist := []Point{
		{Year: 2025, Month: 1, Value: 15, Pipeline: 10},
		{Year: 2025, Month: 2, Value: 17, Pipeline: 10},
	}
	target := Point{Year: 2025, Month: 3, Pipeline: 20}
	got := PipelineResidual{FallbackK: 6}.Predict(hist, target)
	// Ensemble = target.Pipeline + {residuals} = 20 + {5,7} = {25, 27}.
	if len(got) != 2 || got[0] != 25 || got[1] != 27 {
		t.Fatalf("pipeline+residual samples = %v, want [25 27]", got)
	}
}

func TestPipelineRatio(t *testing.T) {
	// Past realised/pipeline ratios = 1.5 and 2.0; target pipeline 10 -> {15,20}.
	hist := []Point{
		{Value: 15, Pipeline: 10},
		{Value: 20, Pipeline: 10},
	}
	got := PipelineRatio{FallbackK: 6}.Predict(hist, Point{Pipeline: 10})
	if len(got) != 2 || got[0] != 15 || got[1] != 20 {
		t.Fatalf("pipeline×ratio samples = %v, want [15 20]", got)
	}
}

func TestPipelineDecomp(t *testing.T) {
	// planned-overrun factor 1.2, emergency 3, on target pipeline 10 -> 15.
	hist := []Point{{Value: 9, Pipeline: 5, Planned: 6, Emergency: 3}}
	got := PipelineDecomp{FallbackK: 6}.Predict(hist, Point{Pipeline: 10})
	// 10*(6/5) + 3 = 12 + 3 = 15.
	if len(got) != 1 || got[0] != 15 {
		t.Fatalf("pipeline-decomp samples = %v, want [15]", got)
	}
}

func TestPipelineModelsFallBackWithoutPipeline(t *testing.T) {
	hist := mkSeries([]float64{1, 2, 3, 4}, 2025, 1)
	target := Point{Year: 2025, Month: 5, Pipeline: 0} // no pipeline known
	for _, m := range []Model{
		PipelineResidual{FallbackK: 2}, PipelineRatio{FallbackK: 2}, PipelineDecomp{FallbackK: 2},
	} {
		if got := m.Predict(hist, target); len(got) == 0 {
			t.Errorf("%s: expected fallback samples, got abstain", m.Name())
		}
	}
}

func TestBacktestRespectsMinHistoryAndNoLeakage(t *testing.T) {
	// Strictly increasing series; with min history 3, points 3..N are scored.
	series := map[string][]Point{
		"A": mkSeries([]float64{1, 2, 3, 4, 5, 6}, 2021, 1),
	}
	res := Backtest(series, []Model{RecentWindow{K: 3}}, 3)
	if len(res) != 1 {
		t.Fatalf("want 1 model result")
	}
	if res[0].Scored != 3 { // indices 3,4,5
		t.Errorf("scored = %d, want 3", res[0].Scored)
	}
	if res[0].MeanCRPS <= 0 {
		t.Errorf("increasing series should yield positive CRPS, got %v", res[0].MeanCRPS)
	}
}
