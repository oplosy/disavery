# Observability Notes

Where to look when something in disavery misbehaves.

## Signals

- Application logs are the first stop; use the module name to filter noise.
- The `reports/` directory contains generated report outputs that often explain "why did this result change".
- `runbooks/` documents the operational procedures for recurring incidents.

## Habits

- Prefer structured, greppable log lines over prose.
- When adding a new subsystem, add a short runbook entry in the same change.
- Metrics without an alert attached are decoration; either wire them up or drop them.
