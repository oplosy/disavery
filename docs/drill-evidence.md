# Scheduled drill evidence flow

The drills GitHub Actions workflow is the repeatable recovery proof. It runs nightly and weekly, and can also be started manually.

## Nightly and weekly runs

- The nightly programme runs the random restore test plus one rotating scenario.
- The Sunday programme runs the full scenario catalog.
- A manual run accepts a programme (nightly or weekly) and a history branch (default drill-history).

Each run brings up the lab with make up, seeds about 500 MB, runs make smoke, and executes scripts/drill-programme.sh. Reports are uploaded as workflow artifacts for 30 days. The workflow also publishes drill history and the badge to the configured history branch, even when a drill fails after it has started.

Use the workflow artifact for the run-specific reports. Use the drill-history branch for the published trend and badge. Neither artifact replaces the runbook's per-scenario report or the declared targets in docs/bia.md.