# traffic-forecaster — Repo Plan

A monthly, honestly-scored forecasting project for London road disruption,
published as a frozen interactive dashboard. Single repo, Go + stochadex.

> **Status (2026-06-07).** The live APIs have been verified and an exploratory
> pass run. The target has moved from a *count of newly-starting disruptions* to
> **disruption burden** (see below), and a major forward-looking data source —
> **DfT Street Manager** — has been validated. This document supersedes the
> original count-flow plan.

## What changed since v1

- **Target → disruption burden**, not a count of new disruptions. The EDA showed
  the count-flow target was weak: planned roadworks dominate (~94%), severe events
  are rare, and at any meaningful severity threshold the per-borough monthly count
  is almost always 0 or 1 (sparse, low-information).
- **The TfL disruption feed has no forward visibility** (zero future-dated starts)
  and no creation timestamp — so "planned works as a forward covariate" is not
  obtainable from it. We bring forward signal in from **DfT Street Manager**, whose
  public archive carries permits with future `proposed_start_date` (validated:
  ~12k London works dated ahead, up to 16 months out).
- **Burden decomposes by data source**, which matches the structure cleanly:
  planned + emergency *works* come from Street Manager; *non-works incidents*
  (accidents, congestion, planned events) come only from the TfL feed.
- **Two scoring regimes return.** Burden is continuous → **CRPS**; we also keep a
  discrete count sub-target → **log score**. The original "single regime" caveat
  is lifted.
- **The cold-start is largely solved** for the dominant works component: Street
  Manager gives ~6 years of historic London roadworks, so the works model can be
  backtested offline now. Only the non-works incident tail and the live scored
  ground truth must accrue forward from our own snapshots.
- **Data-in-git policy fixed:** commit only our own snapshots/predictions/
  resolutions; cite large external archives, never copy them in.

---

## Framing

The repo forecasts **monthly road-disruption burden per London borough** (corridor
as a sparse secondary view). "Burden" is a severity/traffic-management-weighted
sum of disruption-days — how disrupted a borough actually is over the month, not
how many new disruptions begin. This turns the data's defining properties
(long-running works, a persistent stock rather than a flow) from a liability into
the signal.

Burden is modelled as an additive decomposition:

```
burden(borough, month) = planned-works  +  emergency-works  +  non-works-incidents
                         └─────── DfT Street Manager ───────┘   └─ TfL feed ─┘
                          near-deterministic    reactive,         the genuine
                          given the permit      historic-rate     stochastic tail
                          pipeline              learnable
```

- **planned works** — known ahead from the Street Manager permit pipeline; the
  uncertainty is overruns, cancellations, and provisional→granted transitions.
- **emergency works** — Street Manager "Immediate" categories; little forward
  notice, modelled as a borough/seasonal rate.
- **non-works incidents** — accidents, congestion, planned events; absent from
  Street Manager, present only in the TfL disruption feed. The hardest term.

This is a deliberate first repo: one stream, one published artifact, but now with
both a continuous (CRPS) and a discrete (log-score) scoring regime.

---

## Data sources

Raw third-party data is **never committed** — it is cited in `SOURCES.md` and
re-derived on demand. Only our own snapshots, predictions, and resolutions live in
git (they are small, unique, and the proof-of-commit record).

### TfL Unified API — disruption feed (the scored ground truth)

- `GET /Road/all/Disruption?stripContent=true` — full London set in **one
  unpaginated call** (~80 active records, ~270 KB). Captured as immutable daily
  snapshots; the burden the forecast is scored against is computed from these.
- Vocabularies: `GET /Road/Meta/Severities`, `GET /Road/Meta/Categories`.
- Licence: TfL open data (modified OGL) — attribution required, not official TfL,
  respect call limits. Keyless = 50 req/min (ample for one daily pull); an
  `app_key` raises it to 500 req/min.

### DfT Street Manager — street works (planned/emergency works backbone + history)

- Public S3 archive, **no registration**:
  `https://opendata.manage-roadworks.service.gov.uk/{type}/{YYYY}/{MM}.zip`
  (`type` ∈ permit | activity | section_58; monthly, from 2020).
- Carries `proposed_start_date`/`proposed_end_date` (forward-dated), actual
  start/stop events, `work_category` (planned vs Immediate/emergency),
  `traffic_management_type` (burden weight), `highway_authority` (London filter),
  USRN, coordinates. England-wide; ~15% of records are London.
- Licence: **Open Government Licence v3.0** (Crown copyright). Attribution string
  recorded in `SOURCES.md` and on derived outputs.
- **Format gotcha:** these are "streaming" zips (local headers + raw DEFLATE +
  trailing data descriptor, *no central directory*) — `unzip` and Python
  `zipfile` both reject them. `internal/streetmanager` walks the local headers and
  inflates manually. Each entry is one JSON event; a month is ~1 GB / ~1M events.

### DfT road traffic counts (AADF) — held

Annual, modelled, revised — wrong for a monthly heartbeat, but a free annual file
download. Parked as a possible long-horizon annual marquee (mirrors the planned
health repo). Revisit after the monthly stream is live.

---

## What we verified against the live APIs (2026-06-07)

**TfL disruption feed (cross-sectional, one snapshot, n=80):**
- No pagination; full set in one call. **No `created` field** → first-appearance
  is only reconstructable from our own daily snapshots.
- Field casing differs from naive expectations: `startDateTime`/`endDateTime`,
  `lastModifiedTime`, `corridorIds` (array), `subCategory`, `location`.
- **Planned-dominated** (~94% `Works`); only ~5 unplanned records.
- **Severe events rare** (Serious=4, Severe/Closure=0; bulk Minimal+Moderate).
- **No forward visibility** — 0 future-dated starts; 35/80 still-open with end
  dates out to 2028 (forward signal only via *continuation* of long works).
- **Borough beats corridor** — `location` parses to a borough on 100% of records;
  `corridorIds` empty on ~50%. Borough is the primary key.
- Severity ordering *is* usable: `severityLevel` 5–10 is monotonic (5 Closure =
  worst → 10 No Exceptional Delays), 0 No Issues a separate baseline. Implemented
  as `disruptions.SeverityRank`.
- Feed `category` vocabulary differs from `Meta/Categories` — don't build the
  filter off the Meta endpoint without checking the `categories` query param.

**Street Manager (permit/2026-05, full month streamed in ~63 s):**
- 992,233 events; **151,467 London (15.3%); all 33 London authorities present.**
- Full permit lifecycle (SUBMITTED → GRANTED → WORK_START → WORK_STOP →
  CANCELLED/REFUSED) → both *planned* (proposed dates) and *realised* (start→stop)
  windows are reconstructable. Events are lifecycle rows → **dedup by permit/work
  reference** before computing burden.
- `work_category` splits the decomposition for free: Minor/Standard/Major/Major
  (PAA) = planned; "Immediate - urgent"/"Immediate - emergency" ≈ 33% = emergency.
- `traffic_management_type` (Road closure, Lane closure, Multi-way signals, …) =
  the burden-weight covariate.
- **Forward signal confirmed:** 12,207 London works with `proposed_start_date`
  after today, furthest +16 months. The full 6-yr London history is ~70 min of
  one-off streaming — a real offline backtest set.

---

## Repo structure

```
traffic-forecaster/
  cmd/
    pull-disruptions/    # daily disruption snapshotter (scheduled GitHub Action)   [built]
    eda/                 # cross-sectional EDA over snapshots + live vocab           [built]
    ingest-streetmanager/# stream a month, filter to London, report                 [built]
    forecast/            # monthly: generate the burden predictive distribution      [todo]
    resolve/             # monthly: score last period's predictions                  [todo]
    build-dashboard/     # emit the self-contained interactive artifact              [todo]
  internal/
    tfl/                 # TfL API client (retry/backoff, timestamped capture)        [built]
    store/               # gzipped immutable snapshot read/write                      [built]
    disruptions/         # severity ranking, borough parse, planned split             [built]
    streetmanager/       # streaming-zip parser + London filter                       [built]
    burden/              # burden metric, per-work window reconstruction, dedup        [todo]
    scoring/             # CRPS (burden) + log-score (counts), calibration            [todo]
  data/
    raw/                 # immutable daily disruption snapshots (committed)
    streetmanager/       # London extracts (GITIGNORED — cite, don't commit)
    predictions/         # append-only committed predictions per period
    resolutions/         # append-only scored outcomes per period
  config/
    disruptions.yaml     # boroughs, severity threshold, burden weights, void rules
  dashboard/             # static viewer template (data baked in at build)
  SOURCES.md             # data source citations + OGL attribution
  README.md              # methodology page (versioned alongside predictions)
```

**Commit discipline (free proof-of-commit):** predictions land in one commit,
resolutions in a later commit; the git log evidences "predicted before we knew."
`data/raw/` snapshots are immutable; never rewrite them. The daily Action commits
snapshots on its own schedule, so pull before manual pushes.

**stochadex role:** the burden model in `internal/burden` + `forecast` is a
stochadex configuration producing the predictive distribution; the snapshotter,
ingest, and scoring are plain Go around it.

---

## The target: disruption burden

**Definition (to finalise in `config/disruptions.yaml`):** for borough *b* and
month *m*,

```
burden(b, m) = Σ_days d in m  Σ_active disruptions  weight(severity, traffic_mgmt)
```

i.e. a weighted count of disruption-days, summed over the daily snapshots in the
month. Computing from daily presence sidesteps the missing-`created` problem and
is robust to records being edited. A discrete sub-target (count of active
Serious+ disruptions) is kept for the log-score regime.

**Resolution source:** our daily disruption snapshots (the honestly-scored ground
truth). Street Manager is an *input/covariate* and an *offline backtest* source —
never conflated with the scored truth (permits ≠ realised disruption).

**Snapshot/revision rule:** settle on the snapshot taken **14 days after
month-end**. Validate or lengthen via lifecycle-churn EDA once snapshots accrue;
decide once, never change (changing it corrupts the back-history).

**Void rule:** because burden integrates over daily presence, missing snapshot
days bias it directly. If the snapshotter missed > N days in a borough-month,
**void** that borough-month and **publish the gap**. Hiding outages is the one
dishonesty the project refuses.

**De-dup rule:** key disruptions on `id`; key Street Manager events on permit/work
reference, reconstructing each work's planned and realised window.

---

## Covariates

- **Pipeline (strongest):** permit count & proposed duration starting in the
  month; `work_category`, `traffic_management_type` (closure > signals > minor),
  `is_traffic_sensitive`, carriageway vs footway, promoter (utility vs authority),
  permit status / provisional, **Section 58** restrictions (streets that can't be
  dug → suppress works).
- **Calendar:** month, weekday composition, school/bank holidays, festive TLRN
  works embargoes, major-events calendar (marathon, carnival, NYE).
- **Weather (unplanned tail):** cold snaps → burst mains; heavy rain → flooding /
  emergency works.
- **Structural exposure (per-borough offsets):** TLRN / road length, population,
  traffic volume (DfT AADF), apparatus density.
- **Autoregressive:** recent realised burden, plus long works carrying in from
  prior months.

---

## Model (stochadex)

Per-borough (per-corridor where dense) **burden model** emitting a predictive
distribution, built as the additive decomposition above:

- **planned-works burden** — near-deterministic given the permit pipeline; model
  duration overruns, cancellation, and provisional→granted realisation.
- **emergency-works burden** — borough × seasonal rate from Street Manager
  "Immediate" history.
- **non-works incident burden** — the stochastic term, from the TfL feed history.

Scored with **CRPS** against the settled burden, plus **log score** on the
discrete Serious+ active-count sub-target. Resist point forecasts; the
distribution is the product. **Backtest offline** against Street Manager history
(works terms) and accruing snapshots (incident term) before publishing.

**Network propagation (candidate extension).** Burden is naturally a scalar field
on the road graph, so a strong upgrade is to propagate disruption up/downstream —
a closure on a link raises congestion on its neighbours. Adjacency is buildable
from the geometry we already have (TfL `point`/`geography`, Street Manager
`lineString`/USRN/coordinates, corridor ids). Two constraints govern it, both from
the fact that severity is not a clean ordinal:
- **Severity is two axes, not one.** `Closure` is an *intervention* state; `Severe
  → Minimal` is a *congestion/delay* ladder. The `severity_weights` scalar
  conflates them for scoring; it must not be treated as a faithful ordinal in the
  model.
- **The number→category map is one-way.** Never invert a (propagated) scalar back
  to a category. Carry the categorical label as a separate attribute, and model
  explicit *cause→effect* transitions (a closure manifests as congestion on
  neighbours — `Closure`≢`Gridlocked`), rather than re-labelling a neighbour from
  its scalar.

---

## Dashboard (`cmd/build-dashboard`)

- Static, self-contained bundle; data for the displayed window **baked in at
  build** — no live API calls, no browser storage.
- **Sliding window:** 6 months back (predicted burden distribution overlaid on
  realised) + 1 month forward (the prediction); planned works visible further out
  as a known covariate from the Street Manager pipeline.
- London map with **borough shading** by predicted/observed burden; timeline
  scrubber; click a borough → predicted distribution and, once resolved, actual
  vs predicted overlay + that borough's score.
- Running **calibration plot** across everything resolved to date — the real
  product; same plot each month, more points over time.
- Each month's build is **frozen and stored in R2** under that month's key (the
  human-readable witness); `data/` files are the machine-readable canonical
  record. Manually copy the latest build into the blog's `/traffic-forecasts`.

---

## Build order (with status)

1. ✅ Repo skeleton + `internal/tfl` client; live-API specs verified.
2. ✅ `cmd/pull-disruptions` daily snapshotter + GitHub Action (gzipped, immutable).
   *Pending: push to GitHub + first scheduled run to start banking history.*
3. ✅ EDA (`cmd/eda`) — cross-sectional findings above. *Time-series EDA (base
   rates, lifecycle churn, seasonality) pending accumulated snapshots.*
4. ✅ Street Manager ingest (`cmd/ingest-streetmanager`, `internal/streetmanager`)
   — forward planned works validated; pivot to burden confirmed.
5. ⏭ **Burden backtest dataset** — sweep the full London Street Manager history,
   dedup events into per-work windows, compute the monthly borough burden series
   (`internal/burden`).
6. ⏭ **Resolution criterion + burden weights** into `config/disruptions.yaml` +
   methodology README; validate the 14-day rule as churn data arrives.
7. ⏳ **Burden model + offline backtest.** ✅ Scoring + baseline harness
   (`internal/scoring` CRPS/PIT, `internal/forecast` empirical baselines,
   `cmd/backtest`): on the works-burden series, **`seasonal+recent` is the honest
   floor** — mean CRPS ≈139 and best-calibrated, ~halving the point-forecast
   baseline's CRPS. ✅ **Pipeline covariate** (`burden.PipelineSeries` vintaged by
   each work's first-seen filing date; `forecast.PipelineResidual`): centring on
   the known forward permit book + empirical residual **cuts CRPS ~35%** (90.6 vs
   the 138.8 floor) — the forward signal is real and measurable. ✅ **Calibration:**
   burden trends down over the series, so an all-history residual over-predicts;
   a trailing-window residual (`PipelineResidual.ResidualK=18`) fixes both — best
   model `pipeline+res(18m)` reaches **CRPS 72.1, calibration 15.5** (≈48% below
   the baseline floor, near the best-calibrated model). Multiplicative variants
   (ratio/decomp) tried and rejected — the pipeline is biased low, so multiplying
   amplifies. *Next within #7:* the stochadex generative / network-propagation
   model — scored by this same harness. NB the backtest is against the
   Street-Manager works proxy; the live scored target (TfL-feed burden) accrues
   from snapshots.
8. ⏭ `cmd/forecast` + `cmd/resolve` (the monthly heartbeat).
9. ⏭ `cmd/build-dashboard` + first frozen R2 snapshot.
10. ⏭ First published month: predictions only; honest "the calibration curve is
    noise until it isn't" framing from the outset.

---

## Open decisions to settle as you build

- **Burden weighting** — severity × traffic-management weights, and whether to
  threshold (Serious+) or weight continuously. Set from Street Manager + snapshot
  EDA.
- **Granularity** — borough primary (confirmed); corridor as a sparse secondary
  view where dense.
- **Snapshot-lag length (14 days?)** — confirm or lengthen from lifecycle churn
  once snapshots accrue.
- **Ground-truth boundary** — burden scored from the TfL feed; Street Manager for
  backtest/covariate only. Keep them distinct.
- **Forward planned-works freshness** — Street Manager monthly archive (≤1-month
  lag) vs supplementing with the live TfL street feed at forecast time.
- **DfT AADF annual marquee** — whether/when to add as the long-horizon companion.
