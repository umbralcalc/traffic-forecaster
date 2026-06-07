// Package scoring implements the proper scoring rules the forecast is judged by.
// It is deliberately model-agnostic: a forecast is an ensemble of samples from a
// predictive distribution, so the same rules score the baselines here and any
// stochadex model later.
package scoring

import (
	"math"
	"sort"
)

// CRPS is the Continuous Ranked Probability Score of an ensemble forecast
// against observation y (lower is better). It uses the standard estimator
//
//	CRPS = mean_i |x_i - y|  -  1/2 * mean_{i,j} |x_i - x_j|
//
// evaluated in O(n log n) via the sorted-sample identity
// sum_{i,j}|x_i-x_j| = 2 * sum_i (2i - m - 1) x_(i)  (i 1-indexed, ascending).
// For a point forecast (all samples equal c) this reduces to |c - y|.
func CRPS(samples []float64, y float64) float64 {
	m := len(samples)
	if m == 0 {
		return math.NaN()
	}
	s := append([]float64(nil), samples...)
	sort.Float64s(s)
	var absErr, weighted float64
	for i, x := range s { // i is 0-indexed; 1-indexed rank is i+1
		absErr += math.Abs(x - y)
		weighted += float64(2*(i+1)-m-1) * x
	}
	// weighted/m^2 == 0.5 * mean_{i,j}|x_i - x_j|
	return absErr/float64(m) - weighted/float64(m*m)
}

// PIT returns the probability-integral-transform value of y under the ensemble:
// the fraction of samples <= y. For a calibrated forecaster these are uniform on
// [0,1] across many observations.
func PIT(samples []float64, y float64) float64 {
	m := len(samples)
	if m == 0 {
		return math.NaN()
	}
	var n int
	for _, x := range samples {
		if x <= y {
			n++
		}
	}
	return float64(n) / float64(m)
}

// Calibration accumulates PIT values into equal-width bins for a reliability
// readout. A well-calibrated forecaster fills the bins roughly evenly.
type Calibration struct {
	bins  []int
	total int
}

// NewCalibration returns a Calibration with the given number of [0,1] bins.
func NewCalibration(bins int) *Calibration {
	if bins < 1 {
		bins = 10
	}
	return &Calibration{bins: make([]int, bins)}
}

// Add records one PIT value.
func (c *Calibration) Add(pit float64) {
	if math.IsNaN(pit) {
		return
	}
	idx := int(pit * float64(len(c.bins)))
	if idx >= len(c.bins) {
		idx = len(c.bins) - 1
	}
	if idx < 0 {
		idx = 0
	}
	c.bins[idx]++
	c.total++
}

// Uniformity is the chi-square-like deviation from a flat histogram, normalised
// so 0 means perfectly uniform (well-calibrated); larger is worse. Useful as a
// single comparable calibration number.
func (c *Calibration) Uniformity() float64 {
	if c.total == 0 {
		return math.NaN()
	}
	expected := float64(c.total) / float64(len(c.bins))
	var chi float64
	for _, n := range c.bins {
		d := float64(n) - expected
		chi += d * d / expected
	}
	return chi / float64(len(c.bins))
}

// Bins returns the per-bin fractions of the total (each ~1/bins if calibrated).
func (c *Calibration) Bins() []float64 {
	out := make([]float64, len(c.bins))
	if c.total == 0 {
		return out
	}
	for i, n := range c.bins {
		out[i] = float64(n) / float64(c.total)
	}
	return out
}
