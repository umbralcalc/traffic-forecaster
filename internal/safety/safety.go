// Package safety builds the road-safety rating: a hierarchical Poisson model of
// monthly collision counts per 1km cell, published as S = P(no collision this
// month) = exp(-lambda).
//
// The intensity is decomposed multiplicatively (log-additively):
//
//		lambda_it = base_i * season_moy(t) * exp(f_t)
//
//	  - base_i      cell baseline rate (deseasonalised mean, shrunk toward a prior:
//	                the global pool, or — with SpatialR>0 — the local neighbourhood
//	                rate, so sparse cells borrow strength from the risk surface)
//	  - season_mo   month-of-year multiplier (pooled across cells, mean ~1)
//	  - f_t         shared London log-anomaly — the common factor that couples every
//	                cell in a good/bad month (COVID collapse, weather years, trend)
//
// Forecasts integrate over f ~ N(0, sigmaF^2) and Poisson sampling, so the
// predictive ensemble is jointly coupled (a bad London month lifts all cells at
// once). Unlike the earlier Gaussian-burden model the link is exp(), so the
// intensity is always positive — no max(0,.) clamp, and none of the truncation
// miscalibration that clamp caused.
package safety

import (
	"fmt"
	"math"
	"math/rand/v2"
	"sort"

	"github.com/umbralcalc/traffic-forecaster/internal/series"
)

// Prediction is the per-cell predictive summary for one target month.
type Prediction struct {
	Rating   float64 // P(no collision) = mean over the ensemble of exp(-lambda)
	Expected float64 // E[count] = mean ensemble intensity
	P95Count int     // 95th-percentile sampled count (the "bad month" tail)
	Ensemble []int   // sampled counts (jointly coupled through the shared factor)
}

// PoissonFactorModel fits the decomposition above by method of moments and
// samples the predictive ensemble for a target month.
type PoissonFactorModel struct {
	N        int     // ensemble size (default 200)
	Seed     uint64  // base RNG seed
	HistK    int     // trailing months used for the level/anomaly (0 = all history)
	ShrinkA  float64 // pseudo-exposure shrinking each cell's base toward its prior
	SpatialR int     // neighbourhood radius (cells) for the spatial prior; 0 = global pool
	// SpatialDecay is the Gaussian bandwidth (in cells) for weighting neighbours by
	// distance within the radius; 0 = uniform box (every neighbour weighted equally).
	SpatialDecay float64
}

func (m PoissonFactorModel) Name() string {
	if m.SpatialR > 0 {
		if m.SpatialDecay > 0 {
			return fmt.Sprintf("poisson-factor+sp(r%d,a%g,d%g)", m.SpatialR, m.shrink(), m.SpatialDecay)
		}
		return fmt.Sprintf("poisson-factor+sp(r%d,a%g)", m.SpatialR, m.shrink())
	}
	return "poisson-factor"
}

// shrink is the effective pseudo-exposure (defaults to 1).
func (m PoissonFactorModel) shrink() float64 {
	if m.ShrinkA == 0 {
		return 1.0
	}
	return m.ShrinkA
}

// PredictAll fits on the supplied per-cell histories (counts in Point.Value) and
// returns the predictive distribution for target month (ty, tm).
func (m PoissonFactorModel) PredictAll(
	histories map[string][]series.Point, ty, tm int,
) map[string]Prediction {
	n := m.N
	if n <= 0 {
		n = 200
	}
	shrink := m.shrink()

	// Optional trailing window: keep only the last HistK calendar months present
	// anywhere in the panel (the panel is dense, so the month set is shared).
	cutoff := math.MinInt
	if m.HistK > 0 {
		var months []int
		seen := map[int]bool{}
		for _, pts := range histories {
			for _, p := range pts {
				k := p.Year*12 + p.Month - 1
				if !seen[k] {
					seen[k] = true
					months = append(months, k)
				}
			}
		}
		sort.Ints(months)
		if len(months) > m.HistK {
			cutoff = months[len(months)-m.HistK]
		}
	}
	inWindow := func(p series.Point) bool { return p.Year*12+p.Month-1 >= cutoff }

	// --- stage 1: pooled month-of-year seasonal multiplier (mean 1) ---
	moSum := map[int]float64{}
	moCells := map[int]int{}
	var grand float64
	var grandN int
	for _, pts := range histories {
		for _, p := range pts {
			if !inWindow(p) {
				continue
			}
			moSum[p.Month] += p.Value
			moCells[p.Month]++
			grand += p.Value
			grandN++
		}
	}
	overall := 0.0 // mean count per cell-month
	if grandN > 0 {
		overall = grand / float64(grandN)
	}
	season := map[int]float64{}
	for mo := 1; mo <= 12; mo++ {
		if moCells[mo] > 0 && overall > 0 {
			season[mo] = (moSum[mo] / float64(moCells[mo])) / overall
		} else {
			season[mo] = 1
		}
	}

	// --- stage 2: per-cell base rate (deseasonalised, shrunk toward a prior) ---
	// Raw deseasonalised counts and seasonal exposure per cell.
	cnt := make(map[string]float64, len(histories))
	seasExp := make(map[string]float64, len(histories))
	for cell, pts := range histories {
		var c, se float64
		for _, p := range pts {
			if !inWindow(p) {
				continue
			}
			c += p.Value
			se += season[p.Month]
		}
		cnt[cell] = c
		seasExp[cell] = se
	}
	// Prior each cell shrinks toward: with SpatialR>0, the local neighbourhood rate
	// (borrow strength from the surrounding risk surface — a quiet cell stays safe,
	// a sparse cell ringed by busy roads is lifted); otherwise the global pool.
	prior := make(map[string]float64, len(histories))
	if m.SpatialR > 0 {
		prior = neighbourPriors(cnt, seasExp, m.SpatialR, m.SpatialDecay, overall)
	} else {
		for cell := range histories {
			prior[cell] = overall
		}
	}
	base := make(map[string]float64, len(histories))
	for cell := range histories {
		base[cell] = (cnt[cell] + shrink*prior[cell]) / (seasExp[cell] + shrink)
	}

	// --- stage 3: shared London log-anomaly f_t and its spread sigmaF ---
	// f_t = log( observed total / expected total ) across all cells in month t.
	obs := map[int]float64{}
	exp := map[int]float64{}
	for cell, pts := range histories {
		for _, p := range pts {
			if !inWindow(p) {
				continue
			}
			k := p.Year*12 + p.Month - 1
			obs[k] += p.Value
			exp[k] += base[cell] * season[p.Month]
		}
	}
	var fs []float64
	for k, o := range obs {
		if e := exp[k]; e > 0 {
			fs = append(fs, math.Log((o+0.5)/(e+0.5)))
		}
	}
	sigmaF := math.Sqrt(pvariance(fs))

	// --- stage 4: predictive ensemble for the target month ---
	g := season[tm]
	seed := m.Seed + uint64(ty*12+tm)
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	// Shared factor draws (one per ensemble member, common to every cell — this is
	// what couples the cells jointly).
	f := make([]float64, n)
	for k := range f {
		f[k] = rng.NormFloat64() * sigmaF
	}

	out := make(map[string]Prediction, len(histories))
	for cell := range histories {
		mu := base[cell] * g
		ens := make([]int, n)
		var ratingSum, lamSum float64
		for k := 0; k < n; k++ {
			lam := mu * math.Exp(f[k])
			ratingSum += math.Exp(-lam) // P(0 | lam), Rao-Blackwellised
			lamSum += lam
			ens[k] = poissonSample(rng, lam)
		}
		sorted := append([]int(nil), ens...)
		sort.Ints(sorted)
		out[cell] = Prediction{
			Rating:   ratingSum / float64(n),
			Expected: lamSum / float64(n),
			P95Count: sorted[int(0.95*float64(n-1))],
			Ensemble: ens,
		}
	}
	return out
}

// neighbourPriors returns, for each "i_j" cell, the deseasonalised collision rate
// over its Chebyshev-radius-r neighbours (excluding itself): summed neighbour
// counts over summed neighbour seasonal exposure. Pooling counts (not averaging
// per-cell rates) is the Gamma-Poisson local rate — busier neighbours carry more
// information. With decay>0 each neighbour is weighted by a Gaussian of its
// Euclidean distance (bandwidth `decay` cells) so nearer cells count for more;
// decay=0 is a uniform box. Cells with no observed neighbours fall back to the
// global rate.
func neighbourPriors(cnt, seasExp map[string]float64, r int, decay, fallback float64) map[string]float64 {
	type ij struct{ i, j int }
	coord := make(map[string]ij, len(cnt))
	present := make(map[ij]string, len(cnt))
	for cell := range cnt {
		var i, j int
		if _, err := fmt.Sscanf(cell, "%d_%d", &i, &j); err != nil {
			continue
		}
		coord[cell] = ij{i, j}
		present[ij{i, j}] = cell
	}
	out := make(map[string]float64, len(cnt))
	for cell := range cnt {
		c, ok := coord[cell]
		if !ok {
			out[cell] = fallback // unparseable id: no neighbourhood
			continue
		}
		var sc, se float64
		for di := -r; di <= r; di++ {
			for dj := -r; dj <= r; dj++ {
				if di == 0 && dj == 0 {
					continue
				}
				nb, ok := present[ij{c.i + di, c.j + dj}]
				if !ok {
					continue
				}
				w := 1.0
				if decay > 0 {
					d2 := float64(di*di + dj*dj)
					w = math.Exp(-d2 / (2 * decay * decay))
				}
				sc += w * cnt[nb]
				se += w * seasExp[nb]
			}
		}
		if se > 0 {
			out[cell] = sc / se
		} else {
			out[cell] = fallback
		}
	}
	return out
}

// poissonSample draws from Poisson(lambda): Knuth for small means, a rounded
// normal approximation for large means (where Knuth's loop is slow).
func poissonSample(rng *rand.Rand, lambda float64) int {
	if lambda <= 0 {
		return 0
	}
	if lambda > 30 {
		v := lambda + math.Sqrt(lambda)*rng.NormFloat64()
		if v < 0 {
			return 0
		}
		return int(v + 0.5)
	}
	l := math.Exp(-lambda)
	k, p := 0, 1.0
	for {
		k++
		p *= rng.Float64()
		if p <= l {
			return k - 1
		}
	}
}

// pvariance is the population variance (0 for fewer than two points).
func pvariance(xs []float64) float64 {
	if len(xs) < 2 {
		return 0
	}
	var mean float64
	for _, x := range xs {
		mean += x
	}
	mean /= float64(len(xs))
	var ss float64
	for _, x := range xs {
		d := x - mean
		ss += d * d
	}
	return ss / float64(len(xs))
}
