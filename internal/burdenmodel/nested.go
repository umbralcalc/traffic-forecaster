package burdenmodel

import (
	"math"
	"math/rand/v2"
	"sort"

	"github.com/umbralcalc/traffic-forecaster/internal/forecast"
)

// NestedFactorModel is a three-level hierarchical forecaster for the adaptive
// (hybrid) units: London → borough → unit. A single London factor L drives
// per-borough factors B_b = α_b·L + η_b, and each unit (fine cell or borough
// "·rest") loads on its borough factor: residual_u = γ_u·B_b + ε_u, with the
// nearest-neighbour spatial coupling applied to the cell idiosyncratics ε.
//
// The borough factor is estimated from the borough-scale aggregate, so every
// unit "sees the world" through the same B_b → L the borough-only model uses,
// and the units of a borough share B_b — so they co-move correctly and
// aggregating them up reproduces the borough view (a consistent refinement).
// The shared B_b also partially pools thin per-cell residuals toward the
// well-estimated borough level, which should steady fine-cell calibration.
type NestedFactorModel struct {
	ResidualK int
	N         int
	FallbackK int
	Seed      uint64
	Group     map[string]string   // unit -> borough
	Adjacency map[string][]string // grid adjacency among fine cells (nil -> none)
}

func (NestedFactorModel) Name() string { return "hier-nested" }

func (m NestedFactorModel) PredictAll(
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
	for u, hist := range histories {
		if targets[u].Pipeline <= 0 {
			continue
		}
		rm := map[int]float64{}
		for _, p := range hist {
			if p.Pipeline > 0 {
				rm[p.Year*12+p.Month-1] = p.Value - p.Pipeline
			}
		}
		if len(rm) >= 6 {
			resid[u] = rm
			modeled = append(modeled, u)
		}
	}
	sort.Strings(modeled)
	months := alignedMonths(resid, modeled)
	if m.ResidualK > 0 && len(months) > m.ResidualK {
		months = months[len(months)-m.ResidualK:]
	}

	if len(modeled) >= 3 && len(months) >= 4 {
		out = m.simulate(modeled, months, resid, targets, n)
	}
	for u := range targets {
		if _, done := out[u]; done {
			continue
		}
		ens := forecast.SeasonalRecent{K: m.FallbackK}.Predict(histories[u], targets[u])
		seed := m.Seed + forecast.HashSeed(u)
		out[u] = forecast.Resample(ens, n, rand.New(rand.NewPCG(seed, seed)))
	}
	return out
}

func (m NestedFactorModel) adjacency() map[string][]string {
	if m.Adjacency != nil {
		return m.Adjacency
	}
	return boroughAdjacency
}

func (m NestedFactorModel) simulate(
	modeled []string, months []int,
	resid map[string]map[int]float64, targets map[string]forecast.Point, n int,
) map[string][]float64 {
	nU, T := len(modeled), len(months)

	// Per-unit baseline + demeaned residual d.
	baseline := make([]float64, nU)
	pipeline := make([]float64, nU)
	d := make([][]float64, nU)
	for i, u := range modeled {
		xs := make([]float64, T)
		for j, t := range months {
			xs[j] = resid[u][t]
		}
		mu := mean(xs)
		baseline[i], pipeline[i] = mu, targets[u].Pipeline
		dd := make([]float64, T)
		for j := range xs {
			dd[j] = xs[j] - mu
		}
		d[i] = dd
	}

	// Group units by borough.
	bIdx := map[string]int{}
	var boroughs []string
	uB := make([]int, nU)
	for i, u := range modeled {
		b := m.Group[u]
		if b == "" {
			b = "(none)"
		}
		gi, ok := bIdx[b]
		if !ok {
			gi = len(boroughs)
			bIdx[b] = gi
			boroughs = append(boroughs, b)
		}
		uB[i] = gi
	}
	nB := len(boroughs)

	// Borough factor B_b(t) = mean demeaned residual of its units.
	B := make([][]float64, nB)
	cnt := make([]int, nB)
	for bi := range B {
		B[bi] = make([]float64, T)
	}
	for i := range modeled {
		for j := 0; j < T; j++ {
			B[uB[i]][j] += d[i][j]
		}
		cnt[uB[i]]++
	}
	for bi := range B {
		if cnt[bi] > 0 {
			for j := range B[bi] {
				B[bi][j] /= float64(cnt[bi])
			}
		}
	}

	// London factor L(t) = mean borough factor; borough loadings α_b + shock η_b.
	L := make([]float64, T)
	for j := 0; j < T; j++ {
		var s float64
		for bi := range B {
			s += B[bi][j]
		}
		L[j] = s / float64(nB)
	}
	varL := pvariance(L)
	sigmaL := math.Sqrt(varL)
	alpha := make([]float64, nB)
	sigmaEta := make([]float64, nB)
	for bi := range B {
		if varL > 0 {
			alpha[bi] = covariance(B[bi], L) / varL
		}
		eta := make([]float64, T)
		for j := range L {
			eta[j] = B[bi][j] - alpha[bi]*L[j]
		}
		sigmaEta[bi] = math.Sqrt(pvariance(eta))
	}

	// Per-unit loading on its borough factor + idiosyncratic ε.
	gamma := make([]float64, nU)
	eps := make([][]float64, nU)
	for i := range modeled {
		bi := uB[i]
		vB := pvariance(B[bi])
		if vB > 0 {
			gamma[i] = covariance(d[i], B[bi]) / vB
		}
		e := make([]float64, T)
		for j := range B[bi] {
			e[j] = d[i][j] - gamma[i]*B[bi][j]
		}
		eps[i] = e
	}

	// Spatial coupling on the cell idiosyncratics (rest units have no neighbours).
	nbrs := buildNeighbours(modeled, m.adjacency())
	spill := spillSparse(nbrs, eps, T)
	innovSigma := innovSigmaSparse(nbrs, eps, spill)

	// Nested simulation: L -> B_b -> unit.
	seed := m.Seed + uint64(months[len(months)-1]+1)
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	runs := make([][]float64, n)
	z := make([]float64, nU)
	s := make([]float64, nU)
	scratch := make([]float64, nU)
	bDraw := make([]float64, nB)
	for k := 0; k < n; k++ {
		l := rng.NormFloat64() * sigmaL
		for bi := 0; bi < nB; bi++ {
			bDraw[bi] = alpha[bi]*l + rng.NormFloat64()*sigmaEta[bi]
		}
		for i := 0; i < nU; i++ {
			z[i] = rng.NormFloat64() * innovSigma[i]
		}
		sarSolve(z, s, scratch, nbrs, spill, sarIters)
		row := make([]float64, nU)
		for i := 0; i < nU; i++ {
			if v := pipeline[i] + baseline[i] + gamma[i]*bDraw[uB[i]] + s[i]; v > 0 {
				row[i] = v
			}
		}
		runs[k] = row
	}

	out := make(map[string][]float64, nU)
	for i, u := range modeled {
		col := make([]float64, n)
		for k := range runs {
			col[k] = runs[k][i]
		}
		out[u] = col
	}
	return out
}
