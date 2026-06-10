package safety

import (
	"math"
	"testing"

	"github.com/umbralcalc/traffic-forecaster/internal/series"
)

// monthly builds a 5-year history for a cell from a per-calendar-month rate, with
// the same count every year (deterministic, so estimates are exact).
func monthly(rate map[int]float64) []series.Point {
	var pts []series.Point
	for y := 2020; y <= 2024; y++ {
		for mo := 1; mo <= 12; mo++ {
			pts = append(pts, series.Point{Year: y, Month: mo, Value: rate[mo]})
		}
	}
	return pts
}

func flat(v float64) map[int]float64 {
	m := map[int]float64{}
	for mo := 1; mo <= 12; mo++ {
		m[mo] = v
	}
	return m
}

func TestRatingFallsWithRate(t *testing.T) {
	// A quiet cell (0.1/month) should rate far safer than a busy one (5/month).
	hist := map[string][]series.Point{
		"quiet": monthly(flat(0.1)),
		"busy":  monthly(flat(5.0)),
	}
	m := PoissonFactorModel{N: 2000, Seed: 1}
	out := m.PredictAll(hist, 2025, 6)
	if out["quiet"].Rating < 0.85 {
		t.Errorf("quiet cell rating = %.3f, want > 0.85", out["quiet"].Rating)
	}
	if out["busy"].Rating > 0.10 {
		t.Errorf("busy cell rating = %.3f, want < 0.10", out["busy"].Rating)
	}
	if out["quiet"].Rating <= out["busy"].Rating {
		t.Error("quiet cell should rate safer than busy cell")
	}
}

func TestExpectedRecoversBaseRate(t *testing.T) {
	// With a flat 2.0/month rate and no shared variance, expected count ~ 2.0.
	hist := map[string][]series.Point{"a": monthly(flat(2.0)), "b": monthly(flat(2.0))}
	out := PoissonFactorModel{N: 4000, Seed: 7}.PredictAll(hist, 2025, 3)
	if e := out["a"].Expected; math.Abs(e-2.0) > 0.2 {
		t.Errorf("expected count = %.3f, want ~2.0", e)
	}
}

func TestSeasonalLowersWinterRating(t *testing.T) {
	// Strong winter peak (Dec=10, else 1). December must rate worse than July.
	rate := flat(1.0)
	rate[12] = 10.0
	hist := map[string][]series.Point{"x": monthly(rate), "y": monthly(rate)}
	m := PoissonFactorModel{N: 3000, Seed: 3}
	dec := m.PredictAll(hist, 2025, 12)["x"]
	jul := m.PredictAll(hist, 2025, 7)["x"]
	if dec.Expected <= jul.Expected {
		t.Errorf("Dec expected %.2f should exceed Jul %.2f", dec.Expected, jul.Expected)
	}
	if dec.Rating >= jul.Rating {
		t.Errorf("Dec rating %.3f should be below Jul %.3f (worse=lower)", dec.Rating, jul.Rating)
	}
}

func TestSharedFactorCouplesCells(t *testing.T) {
	// Two cells whose monthly totals move together year-on-year create a non-zero
	// shared factor; their predictive ensembles must be positively correlated.
	mk := func() []series.Point {
		var pts []series.Point
		for y := 2020; y <= 2024; y++ {
			level := 2.0 + 3.0*float64((y-2020)%2) // alternating good/bad London years
			for mo := 1; mo <= 12; mo++ {
				pts = append(pts, series.Point{Year: y, Month: mo, Value: level})
			}
		}
		return pts
	}
	hist := map[string][]series.Point{"a": mk(), "b": mk()}
	out := PoissonFactorModel{N: 4000, Seed: 5}.PredictAll(hist, 2025, 6)
	a := intsToFloat(out["a"].Ensemble)
	b := intsToFloat(out["b"].Ensemble)
	if c := corr(a, b); c < 0.2 {
		t.Errorf("shared-factor ensemble correlation = %.3f, want > 0.2", c)
	}
}

func TestRatingExceedsNaiveUnderOverdispersion(t *testing.T) {
	// Jensen: with a shared factor, E[exp(-lambda)] >= exp(-E[lambda]). The rating
	// should be at least the naive Poisson rating from the mean intensity.
	mk := func() []series.Point {
		var pts []series.Point
		for y := 2020; y <= 2024; y++ {
			level := 1.0 + 4.0*float64((y-2020)%2)
			for mo := 1; mo <= 12; mo++ {
				pts = append(pts, series.Point{Year: y, Month: mo, Value: level})
			}
		}
		return pts
	}
	out := PoissonFactorModel{N: 8000, Seed: 9}.PredictAll(
		map[string][]series.Point{"a": mk(), "b": mk()}, 2025, 6)["a"]
	naive := math.Exp(-out.Expected)
	if out.Rating < naive-0.02 {
		t.Errorf("rating %.3f below naive %.3f — overdispersion should raise it", out.Rating, naive)
	}
}

func intsToFloat(xs []int) []float64 {
	out := make([]float64, len(xs))
	for i, x := range xs {
		out[i] = float64(x)
	}
	return out
}

func corr(a, b []float64) float64 {
	n := float64(len(a))
	var ma, mb float64
	for i := range a {
		ma += a[i]
		mb += b[i]
	}
	ma /= n
	mb /= n
	var sab, saa, sbb float64
	for i := range a {
		da, db := a[i]-ma, b[i]-mb
		sab += da * db
		saa += da * da
		sbb += db * db
	}
	if saa == 0 || sbb == 0 {
		return 0
	}
	return sab / math.Sqrt(saa*sbb)
}
