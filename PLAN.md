# traffic-forecaster — Repo Plan

A monthly, honestly-scored **London road-safety rating**, published as a frozen
interactive dashboard. Single repo, Go + stochadex.

> **Status (2026-06-10).** Major reframe. The project is no longer about
> *roadworks-disruption burden*; it now forecasts **road collisions** and
> publishes them as a public **road-safety rating**. The works/disruption
> pipeline has been removed. This document supersedes the burden plan.

## What changed (and why)

The build went through burden → works-as-known-plan → and finally to the thing
with genuine forecasting value:

- **Works burden was ~73% known plan.** The Street Manager permit pipeline tells
  you most of next month's roadworks in advance, so "forecasting" it adds little.
  The real uncertainty is in the *unplanned* events.
- **Pivoted to collisions (DfT STATS19).** Genuinely unpredictable, geolocated,
  injury-graded, ~128k London collisions over 5 years — a real, backtestable
  forecasting target with public value.
- **Then dropped works entirely — target *and* covariate.** Roadworks change road
  topology, so they *might* shift the collision rate. We tested it directly (works
  and collisions on the same 2km cells): the within-cell, season-adjusted coupling
  is **null** (partial r ≈ +0.002–0.005; the eye-catching London-total +0.45 is
  the shared COVID collapse/recovery, which first-differencing kills). So works
  are gone from the product.
- **Reframed the target as a safety rating.** Instead of a severity-weighted
  burden, we model a collision **intensity** λ per cell-month and publish
  `S = P(no collision this month) = exp(−λ)` — a number in [0,1] that *is* the
  rating. This is better-specified (a log-link Poisson is the right model for
  sparse counts; no `max(0,·)` clamp, so the truncation miscalibration that broke
  the old Gaussian model cannot recur) and directly scorable.
- **Settled the modelling unit empirically: 1km grid** (was an inherited 2km
  hybrid). A resolution sweep showed the model's skill over a naive base-rate
  *rises* as cells get finer (the hierarchy pools sparse cells; coarse units are
  better served by their own mean). 1km is the sweet spot — strong skill,
  excellent calibration, robust to STATS19 geocoding precision.
- **The TfL disruption feed is not the target and cannot be.** Its `/Road/all/
  Disruption` feed reports current traffic disruptions (mostly roadworks), has no
  collision category, and its nearest proxy (~2 emergency-incident records/day) is
  severe-only and conflates non-collisions. STATS19 is the sole ground truth. The
  daily snapshotter has been removed.

---

## The product

For each **1km British National Grid cell** and each calendar month, a published
**road-safety rating** in two tiers:

- **Headline** — `S = P(no collision of any severity this month)`.
- **Serious (KSI)** — `P(no killed-or-seriously-injured collision this month)`,
  the standard UK road-safety metric.

We model the underlying intensity λ and publish the rating; we also surface the
expected count and the "bad month" tail (P95), because the rating saturates toward
0 in the busiest cells (there, the expected count is the informative quantity).

The rating is **absolute** risk to start (directly ground-truthable). An
exposure-adjusted "safety vs busyness" variant (risk per vehicle-km, via DfT AADF)
is a later refinement, not v1.

---

## The target (definition)

For cell *i* and month *t*, collisions are modelled as Poisson with a
log-additive intensity:

```
log λ_it = base_i + season_moy(t) + f_t
S_it     = E_f[ exp(−λ_it) ]          # the published rating
```

- **base_i** — cell baseline log-rate (deseasonalised mean, shrunk toward the
  London pool).
- **season_moy** — month-of-year multiplier, pooled across cells.
- **f_t** — a shared **London log-anomaly**: the common factor that couples every
  cell in a good/bad month (COVID collapse, weather years, secular trend).

Forecasts integrate over `f ~ N(0, σ_f²)` and Poisson sampling, so the predictive
ensemble is **jointly coherent** across cells (a bad London month lifts every cell
together — needed to aggregate ratings up to boroughs/roads correctly).

**Ground truth:** DfT STATS19, the only valid source. **Cadence caveat:** STATS19
is published annually with a ~1-year lag, so genuine forward validation is annual
and slow (commit now, settle ~12–18 months later). This is structural, not a gap.

---

## Data sources

Raw third-party data is **never committed** — cited in `SOURCES.md`, re-derived on
demand. Only our own predictions and resolutions live in git (small, unique, the
proof-of-commit record).

### DfT STATS19 — road collision data (the target and only ground truth)

- Public CSVs, OGL v3.0: `.../dft-road-casualty-statistics-collision-{year}.csv`
  and `...-collision-last-5-years.csv`. National, annual, ~1-year lag.
- Per-collision British National Grid coordinates, `collision_severity`
  (Fatal/Serious/Slight), date. Filtered to London; binned to 1km cells; KSI =
  severity ∈ {1,2}.

### DfT road traffic counts (AADF) — held for exposure

- Annual modelled link-level traffic counts. Parked for the future
  exposure-adjusted ("safety vs busyness") rating, not the v1 absolute rating.

---

## Repo structure

```
traffic-forecaster/
  cmd/
    build-accident-burden/  # stream STATS19, filter to London, write the 1km panel  [built]
    safety-backtest/        # expanding-window backtest vs naive base-rate            [built]
    grid-sweep/             # resolution experiment (0.5–4km, LA-district)            [built]
    forecast/               # commit the safety rating for future months              [built]
    resolve/                # settle a committed prediction against realised STATS19   [built]
    build-dashboard/        # emit the self-contained interactive artifact            [todo]
  internal/
    grid/                   # British National Grid cell geometry (CellID/Centroid)   [built]
    series/                 # panel CSV loader + the monthly Point type               [built]
    safety/                 # PoissonFactorModel (intensity → rating) + Backtest       [built]
    scoring/                # Brier, log-loss, Poisson deviance, reliability/calib.    [built]
  data/
    incidents/              # 1km collision panel (GITIGNORED — derived from STATS19)
    predictions/            # committed safety-rating predictions per month
    resolutions/            # scored outcomes per month (once STATS19 settles them)
  dashboard/                # static viewer template (data baked in at build)
  SOURCES.md  README.md  PLAN.md
```

**stochadex role:** today the model is a hand-rolled Poisson sampler in
`internal/safety` (the right MVP). stochadex enters with the planned latent-factor
upgrade (below) — simulating the intensity process forward for multi-horizon,
jointly-coherent ensembles.

---

## Validation

- **Backtest (retrospective).** `safety-backtest` runs an expanding-window,
  no-leakage backtest over the panel, scoring the published quantities (Brier and
  log-loss on P(incident), Poisson deviance on counts) against a naive per-cell
  base-rate and a pooled climatology. Result: **well-calibrated** out-of-sample
  (cal.err 0.004 accidents / 0.008 KSI, reliability on the diagonal), beats naive
  on the proper scores (clearly on KSI), crushes climatology.
- **Forward commit/resolve loop (proof-of-commit).** `forecast` commits the rating
  for future months (frozen in `data/predictions/` before the realised data
  exists); `resolve` settles them against a later STATS19 vintage, refusing months
  not yet released and asserting no leakage. A genuine-forward **2025** prediction
  is committed; it settles when DfT publishes 2025 (~H2 2026).

---

## Build order (with status)

1. ✅ Pivot to collisions: STATS19 verified (recency, BNG geometry, severity).
2. ✅ `cmd/build-accident-burden` + `internal/grid` — dense 1km cell × month panel,
   all-severity and KSI tiers, explicit zeros (the Poisson zeros).
3. ✅ `internal/safety.PoissonFactorModel` — hierarchical Poisson intensity →
   rating; expected-behaviour tested.
4. ✅ `internal/scoring` — Brier, log-loss, Poisson deviance, reliability/
   calibration (the scored object *is* the published number).
5. ✅ `cmd/safety-backtest` + `safety.Backtest` — calibration & skill vs baselines.
6. ✅ `cmd/grid-sweep` — settled the modelling unit at **1km** (skill rises as
   cells get finer; coarse loses to its own mean).
7. ✅ Pruned the dead works/TfL pipeline; slimmed internals to the safety product.
8. ✅ `cmd/forecast` + `cmd/resolve` — the forward commit/resolve loop; committed a
   genuine-forward 2025 prediction.
9. ⏭ **Q4 model enhancements** (agreed; see roadmap below).
10. ⏭ `cmd/build-dashboard` + first frozen R2 snapshot (with cell→borough/road
    aggregation for public presentation).
11. ⏭ First published month: ratings only; honest "the calibration curve is noise
    until it isn't" framing from the outset.

---

## Q4 enhancement roadmap (next, agreed)

Ordered by value-per-effort. The first two need **no new data** and are the model
that justifies pushing to 0.5km.

- **A. Spatial smoothing of `base_i` (CAR/ICAR/kernel).** Today a cell borrows
  strength only from the London-wide pool; risk is a *surface*, so a cell should
  borrow from its neighbours. (Distinct from the "coupling is global" finding,
  which was about temporal *shocks* `f_t` — this is the static base-rate field.)
  Sharpens sparse fine cells and unlocks 0.5km (where skill was higher still).
- **B. `f_t` as a latent stochastic process (the stochadex fit).** Replace the iid
  `f_t ~ N(0,σ)` with an AR(1)/OU/random-walk Iteration → a state-space /
  log-Gaussian Cox process. Gives momentum, trend, **multi-horizon forecasts**
  (3/6/12 months) with honest fan-out, and jointly-coherent cell ensembles. Ports
  the width-N vectorised-iteration pattern (from the retired
  `HierarchicalEmergencyIteration`) to a log-link/Poisson emission.
- **C. Exposure (DfT AADF)** → rate per vehicle-km: the "safety vs busyness"
  distinction. Needs the AADF join; makes the rating about danger, not traffic.
- **D. Road-network static features (OS/OSM):** junction density, road-class mix —
  a generalisable base-rate prior (helps cells with little history). Overlaps A.

**Ruled out by evidence:** trend/recency weighting (a trailing-window probe was
null — `f_t` already carries the London level/trend), and weather at monthly
resolution (absorbed by `f_t`).

---

## Open decisions to settle as we build

- **0.5km + spatial smoothing** — adopt once enhancement A lands and the
  finer-grid calibration holds.
- **Public presentation unit** — 1km cells are honest for modelling but arbitrary
  for the public; aggregate to nameable units (boroughs, major roads) for display
  while keeping cells under the hood.
- **Exposure normalisation** — when to add AADF for the "safety vs busyness" rate.
- **Live nowcast proxy (v3)** — whether a future same-week proxy (e.g. a real-time
  incident feed) is worth standing up to shorten the annual settle cadence.
