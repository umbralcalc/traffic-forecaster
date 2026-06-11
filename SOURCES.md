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

## DfT road traffic counts (AADF) — investigated, not used

- Annual modelled link-level traffic counts (region files at
  `storage.googleapis.com/dft-statistics/road-traffic/downloads/aadf/region_id/`).
- Evaluated as an exposure covariate and dropped: only ~27% of London
  cells have a count point, the signal is confounded (high-flow motorways have *low*
  collision density), and 5 years of per-cell history already captures the rate. Not
  part of the model.
- Licence: Open Government Licence v3.0 (Crown copyright).

## Active Travel Academy — London LTN dataset (investigated, not used)

- LTN locations + implementation dates (GeoJSON): used to test whether road-safety
  interventions are a forward-causal covariate. An event-study showed the apparent
  effect was siting selection, not causation, so it is not in the model.
- Source: https://blog.westminster.ac.uk/ata/projects/london-ltn-dataset/

---

*Historical note:* earlier versions of this project used the **TfL Unified API**
road-disruption feed and the **DfT Street Manager** street-works archive to
forecast roadworks-disruption burden. That direction was dropped:
works are largely known in advance and have no measurable effect on the collision
rate, and the TfL feed contains no collision data. Both sources have been removed
from the codebase.
