# traffic-forecaster

A monthly, honestly-scored forecast of **road-disruption burden across London
boroughs**, published as a frozen interactive dashboard. One Go + stochadex repo.

This page is the methodology, versioned alongside the predictions it describes.
The machine-readable contract is [`config/disruptions.yaml`](config/disruptions.yaml);
data sources and their licences are in [`SOURCES.md`](SOURCES.md); the full design
is in [`PLAN.md`](PLAN.md).

## What we forecast

For each London borough and each calendar month, a **predictive distribution**
over its *disruption burden* — a severity-weighted measure of how disrupted the
borough's roads are over the month. We forecast the distribution, not a point
estimate; the distribution is the product.

Burden decomposes by where the signal lives:

```
burden = planned works + emergency works + non-works incidents
         └──── DfT Street Manager ────┘   └──── TfL feed ────┘
          known ahead via the permit       accidents, congestion,
          pipeline (forward-dated)         events — the uncertain tail
```

## How burden is defined (the scored target)

Burden is computed from our own **daily snapshots of the TfL road-disruption
feed** (the immutable record in `data/raw/`):

- For a borough on a given day, the **daily value** is the sum, over disruptions
  present in that day's snapshot and attributed to the borough, of a **severity
  weight** (Closure/Severe/Serious/Moderate/Minimal → 1.0 … 0.1).
- The **monthly burden** is `days_in_month × mean(daily value over the month's
  captured days)`. Using a mean over captured days degrades gracefully if a few
  daily snapshots are missing, rather than silently undercounting.

This presence-based definition sidesteps the feed's lack of a creation timestamp
and its editing of records. Street Manager permit data is a **model input**, never
the thing we score against.

## Resolution & honesty rules

- **Settle window.** A month resolves from its daily snapshots **14 days after
  month-end**, giving late-entered and retracted records time to settle. The rule
  is fixed in advance; we never change it retroactively.
- **Void rule.** Because burden integrates over daily presence, a missing
  snapshot day biases every borough. If a month's snapshot coverage falls below
  **80%**, that month is **voided and the gap is published**. Hiding an outage is
  the one dishonesty this project refuses.
- **Proof of commit.** Predictions land in one commit; resolutions in a later
  commit. The git log itself evidences that we predicted before we knew.
- **Immutability.** `data/raw/` snapshots are never rewritten.

## Scoring

- **Primary:** CRPS on the continuous burden, averaged over borough-months.
- **Secondary (reported):** log score on a discrete sub-target — the count of
  active Serious-or-worse disruptions.
- **Calibration:** a running PIT histogram across everything resolved to date —
  the same plot each month, gaining points over time. Early on it is noise; the
  honest framing is that the calibration curve *is* the deliverable, and it is
  noise until it isn't.

## Status

Draft. Weights and the settle window are provisional and may be tuned **only**
before the first published month; after that they freeze. The daily snapshotter
is live and banking the record; the burden model and dashboard are in
development. See [`PLAN.md`](PLAN.md) for the build order.

## Attribution

Powered by TfL Open Data. Contains public sector information licensed under the
Open Government Licence v3.0 (DfT Street Manager). Not affiliated with or endorsed
by TfL or the DfT.
