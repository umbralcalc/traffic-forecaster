package scoring

import "math"

// Brier is the squared error of a probability forecast p against a binary outcome
// (true = event happened): (p - y)^2. Lower is better; a forecast equal to the
// base rate scores the variance of the outcome.
func Brier(p float64, occurred bool) float64 {
	y := 0.0
	if occurred {
		y = 1
	}
	d := p - y
	return d * d
}

// LogLoss is the negative log-likelihood of a binary outcome under probability p,
// clamped away from 0/1 so a confident miss stays finite.
func LogLoss(p float64, occurred bool) float64 {
	const eps = 1e-12
	if p < eps {
		p = eps
	}
	if p > 1-eps {
		p = 1 - eps
	}
	if occurred {
		return -math.Log(p)
	}
	return -math.Log(1 - p)
}

// PoissonDeviance is the unit deviance of a Poisson mean forecast mu against an
// observed count y: 2*(y*log(y/mu) - (y - mu)). Zero when mu == y; the natural
// (proper) score for count forecasts.
func PoissonDeviance(mu float64, y int) float64 {
	if mu <= 0 {
		mu = 1e-12
	}
	fy := float64(y)
	term := -(fy - mu)
	if y > 0 {
		term += fy * math.Log(fy/mu)
	}
	return 2 * term
}

// Reliability accumulates a binary-forecast calibration curve: forecasts are
// bucketed by predicted probability and compared to the realised event frequency.
type Reliability struct {
	bins          int
	sumP, sumY, n []float64
}

// NewReliability creates a reliability tracker with the given number of equal-width
// probability bins over [0,1].
func NewReliability(bins int) *Reliability {
	if bins < 1 {
		bins = 10
	}
	return &Reliability{bins: bins, sumP: make([]float64, bins), sumY: make([]float64, bins), n: make([]float64, bins)}
}

// Add records a forecast probability and its outcome.
func (r *Reliability) Add(p float64, occurred bool) {
	b := int(p * float64(r.bins))
	if b >= r.bins {
		b = r.bins - 1
	}
	if b < 0 {
		b = 0
	}
	r.sumP[b] += p
	if occurred {
		r.sumY[b]++
	}
	r.n[b]++
}

// Curve returns, per non-empty bin, the mean forecast probability, the realised
// event frequency, and the count.
func (r *Reliability) Curve() (meanP, freq []float64, count []float64) {
	for b := 0; b < r.bins; b++ {
		if r.n[b] == 0 {
			continue
		}
		meanP = append(meanP, r.sumP[b]/r.n[b])
		freq = append(freq, r.sumY[b]/r.n[b])
		count = append(count, r.n[b])
	}
	return
}

// CalibrationError is the count-weighted mean absolute gap between forecast
// probability and realised frequency across bins (0 = perfectly calibrated).
func (r *Reliability) CalibrationError() float64 {
	meanP, freq, count := r.Curve()
	var num, den float64
	for i := range meanP {
		num += count[i] * math.Abs(meanP[i]-freq[i])
		den += count[i]
	}
	if den == 0 {
		return 0
	}
	return num / den
}
