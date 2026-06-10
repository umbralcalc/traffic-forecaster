# traffic-forecaster

A monthly, honestly-scored forecast of **London roadworks-disruption burden** at
adaptive spatial resolution, published as a frozen interactive dashboard. One
Go + stochadex repo.

This page is the methodology, versioned alongside the predictions it describes.
The machine-readable contract is [`config/disruptions.yaml`](config/disruptions.yaml);
data sources and their licences are in [`SOURCES.md`](SOURCES.md); the full design
and the road to here are in [`PLAN.md`](PLAN.md).

## What we forecast

For each spatial **unit** and each calendar month, a **predictive distribution**
over its *roadworks-disruption burden* — how much the area's roads are disrupted
by street works that month. We forecast the distribution, not a point estimate;
the distribution is the product.

**Roadworks, named precisely.** The scored target is street-works disruption from
DfT Street Manager (planned + emergency works). Non-works incidents (accidents,
congestion) are a planned *v2* term from the TfL feed — which we already bank as
daily snapshots — not part of v1.

## Adaptive spatial resolution

A pure borough grid is too coarse to see local structure; a pure fine grid leaves
most cells too sparse to forecast honestly. So the unit set is **adaptive**:

- the **busiest 150 cells** (2 km British National Grid) stay as **fine cells**;
- every other work folds into its borough's pooled **"·rest"** unit.

≈183 units — fine detail in the busy core, well-populated units in the sparse
remainder. This is the resolution at which the nearest-neighbour coupling
resolves *and* per-unit calibration stays defensible.

## How burden is defined (the scored target)

For a work with traffic-management type *t* active *d* days within a month, it
contributes `weight(t) × d` to its unit's burden (road closure 1.0 … no-incursion
0.05). A unit's monthly burden is the sum over all works. Realised from each
work's actual (else proposed) window; see [`internal/burden`](internal/burden).

## The model

A hierarchical forecaster (`hier-spatial`):
- a **shared London common factor** couples all units (correct co-movement, so
  the London-wide total has the right variance);
- a **nearest-neighbour spatial coupling** links adjacent fine cells;
- the **vintaged forward permit pipeline** — works already filed for the target
  month, as known at forecast time — is the central covariate.

It emits a predictive **ensemble** per unit. Backtested offline over **6 years**
of Street Manager history before publishing anything.

## Resolution & honesty rules

- **Settle window.** A month resolves from its **settled Street Manager monthly
  archive** (target month + a ~45-day buffer) so late-filed and altered permits
  and actual start/stop events are in. Fixed in advance; never changed.
- **Void rule.** A unit-month whose archive is missing or truncated is **voided
  and the gap published** — hiding an outage is the one dishonesty this project
  refuses.
- **Proof of commit.** Predictions land in one commit; resolutions in a later
  commit. The git log itself evidences that we predicted before we knew.

## Scoring

- **Primary:** CRPS on the per-unit burden, averaged over unit-months.
- **Joint:** CRPS on the London-wide total (sum over units) — this is where the
  coupling earns its keep, and where independent models are over-confident.
- **Calibration:** a running PIT histogram across everything resolved to date —
  the same plot each month, gaining points over time. Early on it is noise; the
  honest framing is that the calibration curve *is* the deliverable, and it is
  noise until it isn't.

## Status

Draft. Weights, the dense-cell count, and the settle window are provisional and
may be tuned **only** before the first published month; after that they freeze.
The daily TfL snapshotter is live (banking the v2 incident signal); the forecast
and resolve commands and the dashboard are in development. See
[`PLAN.md`](PLAN.md) for the build order.

## Attribution

Contains public sector information licensed under the Open Government Licence v3.0
(DfT Street Manager). Powered by TfL Open Data. Not affiliated with or endorsed by
the DfT or TfL.
