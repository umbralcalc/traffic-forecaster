# traffic-forecaster

A small, honestly-scored **London road-safety rating**: a monthly per-cell
probability of *no collision*, built from DfT STATS19 and validated out-of-sample.
Go, no dependencies.

This is a finished side-project. It reached a clear, honest conclusion — a
well-calibrated model at the structural ceiling of what the data supports — and is
parked there rather than dressed up. The methodology and, just as much, **what we
learned by ruling things out** are the deliverable. Data sources and licences are
in [`SOURCES.md`](SOURCES.md).

## What it forecasts

For each **1 km British National Grid cell** and each month, a **road-safety
rating** — `S = P(no collision that month)` — in two tiers:

- **Headline** — any-severity collision.
- **Serious (KSI)** — killed-or-seriously-injured, the standard UK metric.

It's a distribution, not a point estimate; the rating is the headline number, with
the expected count and the P95 "bad month" tail alongside (the rating saturates
toward 0 in the busiest cells, where the count is the informative quantity).

## The model

A hierarchical **Poisson intensity**, fit by method of moments
([`internal/safety`](internal/safety)):

```
log λ_it = base_i + season_moy(t) + f_t          S = E_f[ exp(−λ) ]
```

- **base_i** — the cell's long-run rate. For the sparse KSI tier it shrinks toward
  its **spatial neighbourhood** (Gamma-Poisson empirical Bayes), so data-poor cells
  borrow strength; the dense all-severity tier uses its own history.
- **season** — a pooled month-of-year multiplier.
- **f_t** — a shared **London factor**, modelled as **AR(1)**: a bad/good month
  carries momentum into the next.

The `exp` link keeps the intensity positive (no clamp, so none of the truncation
miscalibration a Gaussian model suffers), and forecasts integrate over `f` and
Poisson sampling, giving a **jointly coherent** ensemble across cells.

## How it's validated

- **Backtest** — expanding-window, no-leakage ([`cmd/safety-backtest`](cmd/safety-backtest)).
  Well-calibrated out-of-sample (reliability on the diagonal; calibration error
  ~0.004 headline / ~0.008 KSI), beats a naive per-cell base-rate on the proper
  scores, and the AR(1) factor cuts London-total CRPS ~16–18%.
- **Proof of commit** — [`cmd/forecast`](cmd/forecast) freezes a month's ratings to
  `data/predictions/` *before* the realised data exists; [`cmd/resolve`](cmd/resolve)
  settles them against a later STATS19 release (refusing months not yet published,
  and any that would leak). A genuine forward **2025** prediction is committed and
  will settle when DfT publishes 2025 STATS19 (~H2 2026).

STATS19 is annual with a ~1-year lag, so forward validation is annual and slow —
stated plainly, not papered over.

## What we learned (the honest findings)

The project's value is as much in what *didn't* work as what did:

- **Settled the unit empirically at 1 km** — a resolution sweep showed model skill
  over naive rises as cells get finer; 1 km is the sweet spot for skill, calibration
  and geocoding precision. Finer (0.5 km) mostly upsamples the same surface.
- **Spatial smoothing helps only the sparse tier** — a clear KSI gain; none for the
  dense all-severity tier, which is already well-served by its own history.
- **The shared factor is the temporal signal** — global, not local: AR(1) on `f_t`
  improves the aggregate; there is **no forecastable local space-time structure**
  (the residual is independent Poisson once rate, season and `f_t` are removed).
- **Things that didn't pay off**, each checked before building: roadworks (≈known in
  advance, and *no* measurable effect on the collision rate); traffic exposure
  (DfT AADF — 27% coverage, confounded, loses to history); road-network features
  (lose to history); a spatio-temporal latent field (collapses to this model);
  road-safety interventions (an LTN effect that **dissolved under an event-study** —
  siting selection, not causation).

The recurring lesson: with 5 years of per-cell history, the model is near the
**information ceiling** of the collision data — London collision risk is a stable
spatial surface + seasonality + one global AR(1) factor + Poisson noise. No
covariate we tested beats that, and none justified a more complex model graph.

## Repo layout

```
cmd/
  build-accident-burden/  stream STATS19 → London 1km collision panel (2 tiers)
  safety-backtest/        expanding-window backtest + calibration
  grid-sweep/             resolution experiment (0.5–4km, LA-district)
  forecast/               commit a month's ratings (proof of commit)
  resolve/                settle a committed prediction vs realised STATS19
internal/
  grid/    BNG cell geometry      series/  panel loader + Point
  safety/  the Poisson model      scoring/ Brier, log-loss, Poisson deviance, reliability
data/predictions/         committed forward ratings (the proof-of-commit record)
```

## Not built (deliberately)

A dashboard. The model is validated and the calibration record is in `data/`; a
front-end was descoped to keep this an honest modelling side-project rather than a
product.

## Attribution

Contains public sector information licensed under the Open Government Licence v3.0
(DfT STATS19). Not affiliated with or endorsed by the DfT.
