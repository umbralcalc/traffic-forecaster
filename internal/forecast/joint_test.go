package forecast

import (
	"math/rand/v2"
	"testing"
)

func TestResampleDraws(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 1))
	out := Resample([]float64{1, 2, 3}, 10, rng)
	if len(out) != 10 {
		t.Fatalf("len = %d, want 10", len(out))
	}
	for _, v := range out {
		if v != 1 && v != 2 && v != 3 {
			t.Errorf("resample produced out-of-set value %v", v)
		}
	}
	if Resample(nil, 5, rng) != nil {
		t.Error("empty input should resample to nil")
	}
}

func TestIndependentJointResamplesToN(t *testing.T) {
	hist := map[string][]Point{"A": mkSeries([]float64{1, 2, 3, 4, 5, 6, 7, 8}, 2025, 1)}
	tg := map[string]Point{"A": {Year: 2025, Month: 9}}
	out := IndependentJoint{Model: RecentWindow{K: 6}, N: 40, Seed: 1}.PredictAll(hist, tg)
	if len(out["A"]) != 40 {
		t.Fatalf("ensemble size = %d, want 40", len(out["A"]))
	}
}

func ramp(n, startYear, startMonth int) []Point {
	vals := make([]float64, n)
	for i := range vals {
		vals[i] = 10 + float64(i%5) // mild structure
	}
	return mkSeries(vals, startYear, startMonth)
}

func TestBacktestJointAndTotalRun(t *testing.T) {
	series := map[string][]Point{
		"A": ramp(24, 2024, 1),
		"B": ramp(24, 2024, 1),
	}
	jm := IndependentJoint{Model: RecentWindow{K: 6}, N: 50, Seed: 1}

	marg := BacktestJoint(series, jm, 8)
	if marg.Scored == 0 || marg.MeanCRPS < 0 {
		t.Errorf("joint marginal backtest: %+v", marg)
	}
	tot := BacktestJointTotal(series, jm, 8)
	if tot.Scored == 0 || tot.MeanCRPS < 0 {
		t.Errorf("joint total backtest: %+v", tot)
	}
}
