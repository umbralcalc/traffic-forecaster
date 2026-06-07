# traffic-forecaster — Repo Plan

A monthly, honestly-scored forecasting project for London road disruptions, published as a
frozen interactive dashboard. Single prediction stream, one Go/stochadex repo.

## Framing

This repo forecasts **London road disruptions**: per corridor (or borough, where sparse),
the count — and optionally the severity mix — of disruptions in a calendar month. The data
is the TfL Unified API's road disruption feed: a **count target**, daily-queryable,
snapshot-resolved, scored with the **log score** (or discrete CRPS).

It was originally scoped with a second stream (cycle-hire demand, CRPS-scored). That's
dropped: it had no historical archive (collect-it-yourself), a long lead time before the
first honest prediction, and an ongoing always-on collector cost. Disruption forecasting is
the better first target — already queryable (EDA immediately, no waiting), squarely in the
road-congestion framing, and no continuously-running service: the snapshotter is one cheap
daily pull (a scheduled job, not a daemon).

**Methodological note:** as a single stream this repo demonstrates *one* scoring regime
(log-score on counts), not two. That's a deliberate simplification for a first repo. The
second (continuous/CRPS) regime can return later via the health repo or a future continuous
traffic target — it was nice-to-have, not required.

## DfT counts as a cheap companion (hold, don't build yet)

DfT road traffic counts (AADF) are **annual, modelled-at-link-level, and revised** — wrong
for a monthly heartbeat. But they're just an annual file download (no collector, no running
cost), so they make a natural **long-horizon annual marquee prediction** sitting beside the
monthly disruption forecasts. That gives the repo a fast-heartbeat-plus-slow-marquee shape
cheaply, and mirrors the structure the health repo will have. Park it; revisit after the
monthly stream is live.

---

## Repo structure

```
traffic-forecaster/
  cmd/
    pull-disruptions/ # daily disruption snapshotter (scheduled job)
    eda/              # one-shot EDA dumps (CSV/JSON for local notebooks)
    forecast/         # monthly: generate predictions for next period
    resolve/          # monthly: score last period's predictions
    build-dashboard/  # emit the self-contained interactive artifact
  internal/
    tfl/              # API client (auth, rate-limit, retry, typed responses)
    disruptions/      # ingestion, models, scoring
    scoring/          # log-score, discrete CRPS, calibration aggregation
    store/            # append-only data files (the canonical record)
  data/
    raw/              # immutable daily snapshots (timestamped)
    predictions/      # append-only: committed predictions per period
    resolutions/      # append-only: scored outcomes per period
  dashboard/          # static viewer template (data baked in at build)
  config/
    disruptions.yaml  # corridors/boroughs, severity filter, snapshot + void rules
  README.md           # methodology page (versioned alongside predictions)
```

**Commit discipline (free proof-of-commit):** predictions land in one commit; resolutions
in a later commit. The repo's own git log evidences "predicted before knew" with no extra
infrastructure. `data/raw/` snapshots are immutable; never rewrite them.

**stochadex role:** the forecasting model in `disruptions/` is a stochadex configuration —
the engine producing the predictive distribution over monthly counts. The snapshotter and
ingestion are plain Go around the stochadex core.

---

## TfL API — auth & limits

- Register at the API portal for an **app_key** (subscribe to a data plan). Append
  `?app_key=...` to requests. `app_id` is no longer required.
- Licence: **modified OGL** — attribution required; don't market as official TfL; respect
  call limits. Re-use/republication permitted (this is why TfL works where scraping a
  commercial feed wouldn't). Record the attribution string in the methodology page.
- Client must handle: rate-limit backoff, retry with jitter, and **logging the capture
  timestamp** on every record (resolution rules depend on it).

### Verify against the live API before writing much
Two specs below are from the Swagger schema and should be confirmed with a real key:
- Whether `/Road/all/Disruption` returns the full London set in one call or needs
  pagination / per-corridor iteration. (Affects the snapshotter loop.)
- The exact fields and casing returned by the disruption feed with `stripContent=true`.

---

## Endpoints

- `GET /Road` — all TfL-managed roads (corridor ids, e.g. A406, A2).
- `GET /Road/{ids}/Disruption` — active disruptions; filter by `severities`, `categories`;
  `stripContent=true` for lean payloads; `application/geo+json` available.
- `GET /Road/all/Street/Disruption?startDate=&endDate=` — disrupted streets in a window.
- `GET /Road/{ids}/Status?dateRangeNullable.startDate=&...endDate=` — aggregated status
  (coarser categorical; not the primary target).
- `GET /Road/Meta/Severities`, `GET /Road/Meta/Categories` — the valid filter vocabularies.
  **Pull these first**; the resolution criterion is defined in their terms.
- `GET /AccidentStats/{year}` — annual per-incident data (covariate/context, not target).

---

## Snapshotter spec (`cmd/pull-disruptions`)

- **Daily** scheduled pull (disruptions evolve slowly). Can run as a **GitHub Action** on a
  cron schedule — no always-on service, no running cost.
- Pull `/Road/all/Disruption` (or per corridor) with `stripContent=true`. Write one
  immutable daily snapshot to `data/raw/YYYY-MM-DD.json` with `captured_at`.
- Keep the **full lifecycle** per record: `id`, `created`, `lastUpdate`, `startDate`,
  `endDate`, `severity`, `category`, `corridor`. Daily snapshots let you reconstruct how
  records changed — essential for an honest snapshot rule.

---

## Resolution criterion (pre-commit → config/disruptions.yaml + methodology README)

- **Target:** for corridor/borough X, the **count of disruptions with severity ≥ {chosen
  threshold}** whose `startDate` falls in the target month. Optionally also severity mix as
  a categorical sub-prediction.
- **Resolution source:** the daily snapshots; count distinct disruption `id`s meeting the
  filter with `startDate` in-month.
- **Snapshot/revision rule:** count as observed in the snapshot taken **14 days after
  month-end** (lets late-entered and retracted records settle). Decide once; never change —
  changing it corrupts the back-history.
- **Void rule:** if the snapshotter missed > N days in the target month, **void** that
  corridor's prediction for the month and **publish the gap**. Hiding outages is the one
  dishonesty the project exists to refuse.
- **De-dup rule:** records recur across snapshots and can be edited — key on `id`, take the
  settled record at the 14-day snapshot.

---

## EDA queries (`cmd/eda` → dump CSV for local notebooks) — run now, data already queryable

1. **Severity & category vocabularies.** Dump `/Road/Meta/Severities` + `/Road/Meta/Categories`.
   → defines the resolution filter precisely before any prediction.
2. **Corridor base rates.** Over a back-window of daily pulls, count disruptions per corridor
   per week by severity. → the Poisson/neg-binomial base rates; identifies which corridors
   have enough events to forecast (sparse ones aggregate to borough level).
3. **Lifecycle churn.** For a sample of disruption ids, track `lastUpdate` and appearance/
   disappearance across daily snapshots. → quantifies retroactive editing; **validates the
   14-day snapshot rule** (if records still move after 14 days, lengthen it).
4. **Planned vs unplanned split.** By category. → planned works are partly predictable from
   their announced future `startDate` (a known forward covariate); unplanned are the genuine
   forecasting challenge.
5. **Seasonality.** Disruption counts by month and day-of-week over available history.

---

## Model (stochadex)

Per-corridor (per-borough for sparse ones) **count model** — Poisson or negative-binomial
with seasonality and planned-works as a known forward covariate — emitting a predictive
**distribution** over the monthly count. Log-score (or discrete CRPS) against the settled
count. Resist point forecasts; the distribution is the product. Backtest offline against
historical snapshots before publishing anything.

---

## Dashboard (`cmd/build-dashboard`)

- Static, self-contained bundle. Data for the displayed window **baked in at build** — no
  live API calls, no browser storage.
- **Sliding window:** fixed **6 months backward** (resolved: predicted distribution overlaid
  on realised count) + **1 month forward** (the prediction); planned works visible further
  out as a known covariate.
- London map with **corridor/borough shading** by predicted/observed count. Timeline scrubber
  at top. Click a corridor → diagnostic pop-up: predicted distribution, and once resolved,
  the overlay of actual vs predicted, plus that corridor's score.
- Running **calibration plot** across everything resolved to date — the actual product; same
  plot each month, more points over time.
- Each month's build is **frozen and stored in R2** under that month's key (the immutable
  human-readable witness). The repo's `data/` files are the machine-readable canonical record.
- You manually copy the latest build into the blog's `/traffic-forecasts` page.

---

## Build order

1. **Repo skeleton + `internal/tfl` client** (auth, rate-limit, retry, timestamped capture).
   Verify the two live-API specs above.
2. **`cmd/pull-disruptions`** as a scheduled daily job (GitHub Action). Start banking daily
   snapshots — cheap, no service.
3. **EDA** (queries 1–5) from existing queryable data — fastest path to first insight.
4. **Resolution criterion** into `config/disruptions.yaml` + methodology README, **validated
   by EDA** (esp. the 14-day snapshot rule via lifecycle churn).
5. **stochadex count model**; backtest scoring offline.
6. **`cmd/forecast` + `cmd/resolve`** monthly commands (the heartbeat).
7. **`cmd/build-dashboard`** + first frozen R2 snapshot.
8. First published month: predictions only (nothing to resolve yet); honest "the calibration
   curve is noise until it isn't" framing from the outset.

## Open decisions to settle as you build
- **Severity threshold** for the target — set from EDA query 1 + base rates in query 2.
- **Corridor vs borough granularity** — set the cutoff from query 2 (per-corridor where
  dense, per-borough where sparse).
- **Snapshot-lag length (14 days?)** — confirm or lengthen from query 3.
- **DfT annual marquee** — whether/when to add as the long-horizon companion.