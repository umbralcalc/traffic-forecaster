package burdenmodel

import (
	"fmt"
	"math"
	"math/rand/v2"
	"sort"

	"github.com/umbralcalc/traffic-forecaster/internal/forecast"
	"gonum.org/v1/gonum/mat"
)

// SpatialFactorModel extends the common-factor model with a nearest-neighbour
// coupling: after removing the shared London factor, the idiosyncratic residual
// is modelled as a spatial autoregression e = spill·W·e + u over the borough
// adjacency graph, so neighbouring boroughs' shocks correlate. Forecasts apply
// the resulting spread M·u (M = (I − spill·W)⁻¹) on top of the common factor.
type SpatialFactorModel struct {
	ResidualK int
	N         int
	FallbackK int
	Seed      uint64
	// Adjacency maps each entity to its neighbours. Defaults to the borough graph
	// when nil; pass a grid adjacency (see GridAdjacency) for cell-level series.
	Adjacency map[string][]string
}

func (m SpatialFactorModel) adjacency() map[string][]string {
	if m.Adjacency != nil {
		return m.Adjacency
	}
	return boroughAdjacency
}

func (SpatialFactorModel) Name() string { return "hier-spatial" }

func (m SpatialFactorModel) PredictAll(
	histories map[string][]forecast.Point,
	targets map[string]forecast.Point,
) map[string][]float64 {
	out := make(map[string][]float64, len(targets))
	n := m.N
	if n <= 0 {
		n = 100
	}

	resid := map[string]map[int]float64{}
	var modeled []string
	for b, hist := range histories {
		t := targets[b]
		if t.Pipeline <= 0 {
			continue
		}
		rm := map[int]float64{}
		for _, p := range hist {
			if p.Pipeline > 0 {
				rm[p.Year*12+p.Month-1] = p.Value - p.Pipeline
			}
		}
		if len(rm) >= 6 {
			resid[b] = rm
			modeled = append(modeled, b)
		}
	}
	sort.Strings(modeled)
	months := alignedMonths(resid, modeled)
	if m.ResidualK > 0 && len(months) > m.ResidualK {
		months = months[len(months)-m.ResidualK:]
	}

	if len(modeled) >= 3 && len(months) >= 4 {
		d := decompose(resid, modeled, months, targets)
		w := weightMatrix(modeled, m.adjacency())
		spill := estimateSpill(w, d.e, len(modeled), len(months))
		mFlat, sigma, ok := spatialSpread(w, d.e, spill, len(modeled), len(months))
		seed := m.Seed + uint64(months[len(months)-1]+1)
		var runs [][]float64
		if ok {
			runs = JointEnsembleSpatial(d.pipeline, d.baseline, d.loading, sigma, d.commonSigma, mFlat, n, seed)
		} else {
			// Singular system (e.g. spill too high): fall back to common-only.
			sig := make([]float64, len(modeled))
			for i := range d.e {
				sig[i] = math.Sqrt(pvariance(d.e[i]))
			}
			runs = JointEnsemble(d.pipeline, d.baseline, d.loading, sig, d.commonSigma, n, seed)
		}
		for i, b := range modeled {
			col := make([]float64, len(runs))
			for k := range runs {
				col[k] = runs[k][i]
			}
			out[b] = col
		}
	}

	for b := range targets {
		if _, done := out[b]; done {
			continue
		}
		ens := forecast.SeasonalRecent{K: m.FallbackK}.Predict(histories[b], targets[b])
		seed := m.Seed + forecast.HashSeed(b)
		out[b] = forecast.Resample(ens, n, rand.New(rand.NewPCG(seed, seed)))
	}
	return out
}

// weightMatrix builds the row-normalised adjacency weight matrix (flattened n*n)
// over the modeled boroughs: W[i,j] = 1/deg(i) if j is a neighbour of i present
// in the set, else 0. Boroughs with no present neighbours get a zero row.
func weightMatrix(modeled []string, adjacency map[string][]string) []float64 {
	idx := make(map[string]int, len(modeled))
	for i, b := range modeled {
		idx[b] = i
	}
	n := len(modeled)
	w := make([]float64, n*n)
	for i, b := range modeled {
		var nbrs []int
		for _, a := range adjacency[b] {
			if j, ok := idx[a]; ok {
				nbrs = append(nbrs, j)
			}
		}
		if len(nbrs) == 0 {
			continue
		}
		val := 1.0 / float64(len(nbrs))
		for _, j := range nbrs {
			w[i*n+j] = val
		}
	}
	return w
}

// GridAdjacency builds an 8-neighbour adjacency map over grid cells (ids "i_j"
// as produced by burden.CellID): two cells are neighbours when their indices
// differ by at most one in each axis. Only cells in the input set are linked.
func GridAdjacency(cells []string) map[string][]string {
	type ij struct{ i, j int }
	coord := make(map[string]ij, len(cells))
	present := make(map[ij]string, len(cells))
	for _, c := range cells {
		var i, j int
		if _, err := fmt.Sscanf(c, "%d_%d", &i, &j); err != nil {
			continue
		}
		coord[c] = ij{i, j}
		present[ij{i, j}] = c
	}
	adj := make(map[string][]string, len(cells))
	for c, p := range coord {
		var nbrs []string
		for di := -1; di <= 1; di++ {
			for dj := -1; dj <= 1; dj++ {
				if di == 0 && dj == 0 {
					continue
				}
				if n, ok := present[ij{p.i + di, p.j + dj}]; ok {
					nbrs = append(nbrs, n)
				}
			}
		}
		adj[c] = nbrs
	}
	return adj
}

// estimateSpill fits the spatial autoregression coefficient by a pooled
// regression of each borough's idiosyncratic residual on its neighbour-mean
// (through the origin), clamped to [0, 0.85] for a stable, invertible system.
func estimateSpill(w []float64, e [][]float64, n, T int) float64 {
	var num, den float64
	for t := 0; t < T; t++ {
		for i := 0; i < n; i++ {
			var nm float64
			for j := 0; j < n; j++ {
				nm += w[i*n+j] * e[j][t]
			}
			num += nm * e[i][t]
			den += nm * nm
		}
	}
	if den == 0 {
		return 0
	}
	spill := num / den
	if spill < 0 {
		spill = 0
	}
	if spill > 0.85 {
		spill = 0.85
	}
	return spill
}

// spatialSpread returns the flattened M = (I − spill·W)⁻¹ and the per-borough
// innovation std sigma_i = std(u_i), u = (I − spill·W)·e, so that M·(sigma∘z)
// reproduces the calibrated spatial covariance. ok=false if M is singular.
func spatialSpread(w []float64, e [][]float64, spill float64, n, T int) (mFlat, sigma []float64, ok bool) {
	a := mat.NewDense(n, n, nil)
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			v := -spill * w[i*n+j]
			if i == j {
				v += 1
			}
			a.Set(i, j, v)
		}
	}
	var inv mat.Dense
	if err := inv.Inverse(a); err != nil {
		return nil, nil, false
	}
	// Innovations u = A·e (per month), then sigma_i = std over months.
	sigma = make([]float64, n)
	for i := 0; i < n; i++ {
		u := make([]float64, T)
		for t := 0; t < T; t++ {
			var s float64
			for j := 0; j < n; j++ {
				s += a.At(i, j) * e[j][t]
			}
			u[t] = s
		}
		sigma[i] = math.Sqrt(pvariance(u))
	}
	mFlat = make([]float64, n*n)
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			mFlat[i*n+j] = inv.At(i, j)
		}
	}
	return mFlat, sigma, true
}
