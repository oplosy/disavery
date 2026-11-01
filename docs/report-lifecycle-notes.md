# Report Lifecycle Notes

A sketch of how a report moves through the system.

1. **Requested** — a trigger (manual or scheduled) creates a report request.
2. **Generated** — the backend collects inputs and writes the output into `reports/`.
3. **Consumed** — downstream tooling or humans read the artifact.
4. **Expired** — old artifacts get archived or cleaned up per retention policy.

## Notes

- Keep generation idempotent: re-running the same request should produce the same artifact.
- If a step fails, log enough context to replay it manually.

This document is a map, not a contract; code remains the source of truth.
