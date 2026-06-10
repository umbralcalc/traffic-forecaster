package burdenmodel

import (
	"fmt"
	"math"
	"math/rand/v2"
	"sort"

	"github.com/umbralcalc/traffic-forecaster/internal/forecast"
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
	// NoPipeline forecasts a target with no forward covariate (e.g. accidents):
	// the central estimate is the per-unit base rate plus the common/spatial
	// structure, and units gate on history length rather than pipeline presence.
	NoPipeline bool
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
		// With NoPipeline (e.g. accidents — no forward plan) the residual is the
		// value itself and units gate on history length, not pipeline presence.
		if !m.NoPipeline && targets[b].Pipeline <= 0 {
			continue
		}
		rm := map[int]float64{}
		for _, p := range hist {
			if m.NoPipeline || p.Pipeline > 0 {
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
		// Sparse spatial autoregression over the neighbour graph — no dense
		// inverse, so it scales to the full grid (hundreds of cells).
		nbrs := buildNeighbours(modeled, m.adjacency())
		spill := spillSparse(nbrs, d.e, len(months))
		sigma := innovSigmaSparse(nbrs, d.e, spill)
		seed := m.Seed + uint64(months[len(months)-1]+1)
		runs := jointEnsembleSparse(d.pipeline, d.baseline, d.loading, sigma, d.commonSigma, nbrs, spill, n, seed)
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

// --- sparse spatial autoregression (scales to the full grid) ---

// sarIters is the number of fixed-point iterations to solve (I − spill·W)s = eps.
// With spill ≤ 0.85 and a row-stochastic W, the error decays like spill^iters.
const sarIters = 64

// buildNeighbours returns, per modeled entity, the indices of its neighbours
// that are present in the set (a sparse adjacency).
func buildNeighbours(modeled []string, adjacency map[string][]string) [][]int {
	idx := make(map[string]int, len(modeled))
	for i, b := range modeled {
		idx[b] = i
	}
	nbrs := make([][]int, len(modeled))
	for i, b := range modeled {
		for _, a := range adjacency[b] {
			if j, ok := idx[a]; ok {
				nbrs[i] = append(nbrs[i], j)
			}
		}
	}
	return nbrs
}

// neighbourMean is the row-normalised neighbour average (W·v); entities with no
// neighbours map to 0.
func neighbourMean(nbrs [][]int, v, out []float64) {
	for i, ns := range nbrs {
		if len(ns) == 0 {
			out[i] = 0
			continue
		}
		var s float64
		for _, j := range ns {
			s += v[j]
		}
		out[i] = s / float64(len(ns))
	}
}

// sarSolve returns s = (I − spill·W)⁻¹ eps via the fixed point s ← eps + spill·W·s,
// exploiting W's sparsity (≤8 neighbours) — no dense inverse.
func sarSolve(eps, s, scratch []float64, nbrs [][]int, spill float64, iters int) {
	copy(s, eps)
	for k := 0; k < iters; k++ {
		neighbourMean(nbrs, s, scratch)
		for i := range s {
			s[i] = eps[i] + spill*scratch[i]
		}
	}
}

// spillSparse fits the SAR coefficient by pooled regression of the idiosyncratic
// residual on its neighbour-mean (through the origin), clamped to [0, 0.85].
func spillSparse(nbrs [][]int, e [][]float64, T int) float64 {
	n := len(e)
	col := make([]float64, n)
	nm := make([]float64, n)
	var num, den float64
	for t := 0; t < T; t++ {
		for i := 0; i < n; i++ {
			col[i] = e[i][t]
		}
		neighbourMean(nbrs, col, nm)
		for i := 0; i < n; i++ {
			num += nm[i] * col[i]
			den += nm[i] * nm[i]
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

// innovSigmaSparse returns the per-entity innovation std sigma_i = std(u_i),
// u = (I − spill·W)·e, so sarSolve(sigma∘z) reproduces the spatial covariance.
func innovSigmaSparse(nbrs [][]int, e [][]float64, spill float64) []float64 {
	n := len(e)
	if n == 0 {
		return nil
	}
	T := len(e[0])
	col := make([]float64, n)
	nm := make([]float64, n)
	u := make([][]float64, n)
	for i := range u {
		u[i] = make([]float64, T)
	}
	for t := 0; t < T; t++ {
		for i := 0; i < n; i++ {
			col[i] = e[i][t]
		}
		neighbourMean(nbrs, col, nm)
		for i := 0; i < n; i++ {
			u[i][t] = col[i] - spill*nm[i]
		}
	}
	sigma := make([]float64, n)
	for i := 0; i < n; i++ {
		sigma[i] = math.Sqrt(pvariance(u[i]))
	}
	return sigma
}

// jointEnsembleSparse samples the hierarchical model with the sparse spatial
// solve: burden_i = max(0, pipeline_i + baseline_i + loading_i·F + s_i), where
// s = (I − spill·W)⁻¹(sigma∘z) and F is the shared common factor.
func jointEnsembleSparse(
	pipeline, baseline, loading, sigma []float64,
	commonSigma float64, nbrs [][]int, spill float64, N int, baseSeed uint64,
) [][]float64 {
	width := len(pipeline)
	rng := rand.New(rand.NewPCG(baseSeed, baseSeed^0x9e3779b97f4a7c15))
	runs := make([][]float64, N)
	eps := make([]float64, width)
	s := make([]float64, width)
	scratch := make([]float64, width)
	for k := 0; k < N; k++ {
		f := rng.NormFloat64() * commonSigma
		for i := 0; i < width; i++ {
			eps[i] = rng.NormFloat64() * sigma[i]
		}
		sarSolve(eps, s, scratch, nbrs, spill, sarIters)
		row := make([]float64, width)
		for i := 0; i < width; i++ {
			if v := pipeline[i] + baseline[i] + loading[i]*f + s[i]; v > 0 {
				row[i] = v
			}
		}
		runs[k] = row
	}
	return runs
}
