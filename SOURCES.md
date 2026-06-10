# Data sources

This project derives its forecasts from public open data. Raw third-party data
is **not committed** to this repository — it is cited here and re-derived on
demand from the sources below. The only data the repo stores is our own daily
disruption snapshots (`data/raw/`, the canonical record for resolution) and our
predictions/resolutions.

## TfL Unified API — road disruption feed (forecast ground truth)

- Endpoint: `https://api.tfl.gov.uk/Road/all/Disruption?stripContent=true`
- Vocabularies: `/Road/Meta/Severities`, `/Road/Meta/Categories`
- Used for: the realised disruption the forecast is scored against (accidents,
  congestion, planned events and works as TfL reports them live), captured as
  immutable daily snapshots.
- Licence: TfL open data, a modified Open Government Licence. Attribution
  required; this project is not affiliated with or endorsed by TfL.
- Attribution: *Powered by TfL Open Data. Contains OS data © Crown copyright and
  database rights. Geomni UK Map data © and database rights.*

## DfT Street Manager — street works open data (planned-works covariate)

- Archive (public, no registration): `https://opendata.manage-roadworks.service.gov.uk/{type}/{YYYY}/{MM}.zip`
  (types: `permit`, `activity`, `section_58`; monthly, from 2020).
- Used for: the planned and emergency roadworks pipeline — proposed/actual dates,
  work category, traffic-management type — England-wide, filtered to London
  highway authorities. The forward signal that makes a burden forecast valuable.
- Licence: Open Government Licence v3.0 (Crown copyright).
- Attribution: *Contains public sector information licensed under the Open
  Government Licence v3.0 (DfT Street Manager).*

## DfT STATS19 — road collision data (the unplanned/incident core)

- Files (public, OGL v3.0): `https://data.dft.gov.uk/road-accidents-safety-data/dft-road-casualty-statistics-collision-{year}.csv`
  and `...-collision-last-5-years.csv` — national, published annually, current to
  the latest published year (~2023).
- Used for: the genuinely-unplanned **accident burden** term — per-collision
  records with British National Grid coordinates (same 2km cells as works),
  `collision_severity` (Fatal/Serious/Slight) and date, filtered to London. No
  forward "pipeline" exists for accidents — that is the point.
- Licence: Open Government Licence v3.0 (Crown copyright).
- Attribution: *Contains public sector information licensed under the Open
  Government Licence v3.0 (DfT STATS19).*

## DfT road traffic counts (AADF) — held for a future long-horizon marquee

- Annual modelled link-level counts; not used in the monthly forecast. Parked
  per PLAN.md as a possible annual companion prediction.
