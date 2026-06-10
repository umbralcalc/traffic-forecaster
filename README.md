# traffic-forecaster

A monthly, honestly-scored **London road-safety rating**, published as a frozen
interactive dashboard. One Go + stochadex repo.

This page is the methodology, versioned alongside the predictions it describes.
Data sources and their licences are in [`SOURCES.md`](SOURCES.md); the full design
and the road to here are in [`PLAN.md`](PLAN.md).

## What we forecast

For each **1km British National Grid cell** and each calendar month, a published
**road-safety rating** — the probability the cell sees **no collision** that
month — in two tiers:

- **Headline** — `S = P(no collision of any severity)`.
- **Serious (KSI)** — `P(no killed-or-seriously-injured collision)`, the standard
  UK road-safety metric.

We forecast a distribution, not a point estimate. The rating is what we publish;
we also surface the expected count and the "bad month" (P95) tail, since the
rating saturates toward 0 in the busiest cells.

**Collisions, named precisely.** The target is DfT STATS19 personal-injury road
collisions — genuinely unpredictable events, not roadworks. (Earlier versions
forecast roadworks-disruption burden; we dropped it. Works are ~known in advance
from permit data, and we verified they have no measurable effect on the collision
rate at this resolution — so they are neither the target nor a covariate.)

## The model

A hierarchical **Poisson intensity** model. For cell *i*, month *t*:

```
log λ_it = base_i + season_moy(t) + f_t        S = E_f[ exp(−λ) ]
```

- **base_i** — the cell's baseline rate (deseasonalised, shrunk toward the London
  pool);
- **season** — a month-of-year multiplier pooled across cells;
- **f_t** — a shared **London log-anomaly** coupling every cell in a good/bad
  month (COVID, weather years, trend).

The `exp` link keeps the intensity positive (no clamp, hence no truncation
miscalibration), and forecasts integrate over `f` and Poisson sampling to give a
**jointly coherent** ensemble across cells. See [`internal/safety`](internal/safety).

## The modelling unit (1km, settled empirically)

A resolution sweep ([`cmd/grid-sweep`](cmd/grid-sweep)) over 0.5–4km grids and
local-authority districts showed the model's skill over a naive per-cell base-rate
**rises as cells get finer**: the hierarchy pools sparse cells and wins, while
coarse units are better served by their own mean. **1km** is the chosen unit —
strong skill (especially KSI), excellent calibration, robust to STATS19 geocoding
precision, ~2,900 cells.

## Validation & honesty rules

- **Backtest.** Expanding-window, no-leakage ([`cmd/safety-backtest`](cmd/safety-backtest)),
  scoring the published quantities against a naive base-rate and climatology. The
  model is well-calibrated out-of-sample (reliability on the diagonal) and beats
  naive on the proper scores.
- **Proof of commit.** [`cmd/forecast`](cmd/forecast) commits a month's rating to
  `data/predictions/` *before* the realised data exists; [`cmd/resolve`](cmd/resolve)
  settles it against a later STATS19 release, refusing months not yet published
  and asserting no leakage. The git log evidences that we predicted before we knew.
- **Cadence.** STATS19 is annual with a ~1-year lag, so forward validation is
  annual and slow — committed now, scored ~12–18 months later. We state this
  plainly rather than pretend to a faster loop.

## Scoring

- **Brier and log-loss** on the published P(incident), per cell-month.
- **Poisson deviance** on the expected count.
- **Calibration:** a running reliability curve across everything resolved to date —
  the same plot each month, gaining points over time. The calibration curve *is*
  the deliverable, and it is noise until it isn't.

## Status

Draft. The model unit (1km) and the validation loop are in place; a genuine
forward 2025 prediction is committed and awaits DfT's 2025 release. Next are the
agreed model enhancements (spatial smoothing; a stochadex latent factor for
multi-horizon forecasts) and the dashboard. See [`PLAN.md`](PLAN.md).

## Attribution

Contains public sector information licensed under the Open Government Licence v3.0
(DfT STATS19). Not affiliated with or endorsed by the DfT.
