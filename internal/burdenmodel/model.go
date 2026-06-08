package burdenmodel

import (
	"github.com/umbralcalc/traffic-forecaster/internal/forecast"
)

// StochadexBurden is a forecast.Model backed by the stochadex burden simulation.
// It centres on the target month's known planned pipeline and adds a
// compound-Poisson emergency shock calibrated from the trailing window of
// realised residual gaps (realised − pipeline) — the parametric, simulated
// counterpart of the empirical pipeline+residual baseline.
type StochadexBurden struct {
	ResidualK int    // trailing months of residuals used for calibration
	N         int    // ensemble size
	FallbackK int    // window for the SeasonalRecent fallback
	Seed      uint64 // base RNG seed (combined with the target month)
}

func (StochadexBurden) Name() string { return "stochadex-burden" }

func (m StochadexBurden) Predict(history []forecast.Point, target forecast.Point) []float64 {
	if target.Pipeline <= 0 {
		return forecast.SeasonalRecent{K: m.FallbackK}.Predict(history, target)
	}

	// Trailing-window residual gaps from months that actually had a pipeline.
	gaps := make([]float64, 0, len(history))
	for _, h := range history {
		if h.Pipeline <= 0 {
			continue
		}
		if g := h.Value - h.Pipeline; g > 0 {
			gaps = append(gaps, g)
		}
	}
	if m.ResidualK > 0 && len(gaps) > m.ResidualK {
		gaps = gaps[len(gaps)-m.ResidualK:]
	}

	params, ok := CalibrateShock(gaps)
	if !ok {
		return forecast.SeasonalRecent{K: m.FallbackK}.Predict(history, target)
	}

	n := m.N
	if n <= 0 {
		n = 100
	}
	// Deterministic per target month so the backtest is reproducible.
	seed := m.Seed + uint64(target.Year*12+target.Month)
	return Ensemble(target.Pipeline, params, n, seed)
}
