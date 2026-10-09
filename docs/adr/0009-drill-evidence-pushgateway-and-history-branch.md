# ADR 0009: Drill evidence in the Pushgateway and on a history branch

- Status: accepted
- Date: 2026-10-08

## Context
A drill writes `reports/<run>/` and appends to `reports/history.jsonl`, both
local to the lab. Spec §8 and §12 also want the results where people and
alerts see them: an alert when the last successful restore test is older than
48 hours, Grafana trends, and a README badge kept up to date by nightly CI
drills, without bot commits on `main`.

CI labs are ephemeral: every nightly run brings a fresh lab up on a runner, so
nothing that lives in the lab survives the run.

## Decision
- `disavery drill run` pushes every result to the lab's Pushgateway in two
  groups per scenario and tier: the last run (result, duration, measured values
  and targets) and the last success. A failed run replaces only the first, so
  it cannot hide when the scenario last passed. Pushing is best effort: a
  failure is a warning and never changes the drill's verdict. The Pushgateway
  persists to a volume, so a restart keeps the history.
- `RestoreTestStale` fires when the last successful S6 is older than 48 hours
  or missing. It is about the drill programme, not the lab, so it carries
  `blocks_drills="false"` and preflight ignores it; otherwise a fresh lab could
  never run its first restore test.
- CI publishes the run's `history.jsonl` lines to the `drill-history` branch,
  with `badge.json` (a shields.io endpoint badge) and a README of trend tables.
  The branch is the only durable record of CI drills; `main` stays free of
  generated commits.
- The badge shows the result and the finishing time of the newest S6 ("PASS ·
  2026-10-08 02:31 UTC") rather than spec §12's "6 h ago": a static file cannot
  age, and a date tells a reader the same.

## Consequences
- Locally, Grafana shows the lab's drills; in CI, the history branch and the
  run artifacts (reports, 30 days) are the evidence.
- A failed or skipped nightly run leaves the badge on the previous result and
  its date, which makes staleness visible to a reader.
- The history branch grows by a few lines per night; at this rate it needs no
  pruning in v1.
