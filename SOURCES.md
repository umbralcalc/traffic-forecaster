# Data sources

This project derives its forecasts from public open data. Raw third-party data
is **not committed** to this repository — it is cited here and re-derived on
demand from the sources below. The only data the repo stores is our own
predictions and resolutions (the proof-of-commit record).

## DfT STATS19 — road collision data (the target and only ground truth)

- Files (public, OGL v3.0): `https://data.dft.gov.uk/road-accidents-safety-data/dft-road-casualty-statistics-collision-{year}.csv`
  and `...-collision-last-5-years.csv` — national, published annually with a
  ~1-year lag, current to the latest published year.
- Used for: the road-safety rating's target — per-collision records with British
  National Grid coordinates (binned to 1km cells), `collision_severity`
  (Fatal/Serious/Slight; KSI = Fatal+Serious) and date, filtered to London. This
  is the sole ground truth; predictions are settled against later releases.
- Licence: Open Government Licence v3.0 (Crown copyright).
- Attribution: *Contains public sector information licensed under the Open
  Government Licence v3.0 (DfT STATS19).*

## DfT road traffic counts (AADF) — held for an exposure-adjusted rating

- Annual modelled link-level traffic counts. Not used in the v1 absolute rating;
  parked per PLAN.md for the future exposure-adjusted ("safety vs busyness") rate,
  i.e. collision risk per vehicle-km.
- Licence: Open Government Licence v3.0 (Crown copyright).

---

*Historical note:* earlier versions of this project used the **TfL Unified API**
road-disruption feed and the **DfT Street Manager** street-works archive to
forecast roadworks-disruption burden. That direction was dropped (see PLAN.md):
works are largely known in advance and have no measurable effect on the collision
rate, and the TfL feed contains no collision data. Both sources have been removed
from the codebase.
