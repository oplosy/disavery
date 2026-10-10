# Drill history

Written by the drills workflow; the badge in the README reads badge.json.

## Trend

| Scenario | Tier | Runs | Passed | Last result | Last run (UTC) | Measures |
|---|---|---|---|---|---|---|
| s1-site-loss | pilot-light | 1 | 1 | PASS | 2026-10-09 10:24 | rpo: median 12s, max 12s, target 1m0s, met 1/1<br>rto: median 2m24s, max 2m24s, target 15m0s, met 1/1 |
| s1-site-loss | warm-standby | 1 | 1 | PASS | 2026-10-10 08:41 | rpo: median 330ms, max 330ms, target 5s, met 1/1<br>rto: median 49s, max 49s, target 2m0s, met 1/1 |
| s6-restore-test | — | 2 | 2 | PASS | 2026-10-10 08:38 | recovery_time: median 1m4s, max 1m4s, target 15m0s, met 2/2 |
| s7-failback | pilot-light | 3 | 1 | PASS | 2026-10-09 10:27 | downtime: median 3s, max 3s, target 15m0s, met 1/1<br>rpo: median 0s, max 0s, target 1m0s, met 1/1 |
| s7-failback | warm-standby | 1 | 1 | PASS | 2026-10-10 08:43 | downtime: median 6s, max 6s, target 2m0s, met 1/1<br>rpo: median 0s, max 0s, target 5s, met 1/1 |

## DR tiers (site loss, S1)

| Tier | Target RPO | Actual RPO, median | Target RTO | Actual RTO, median | Runs passed | Always-on DR nodes | Est. monthly cost |
|---|---|---|---|---|---|---|---|
| pilot-light | 1m0s | 12s (max 12s) | 15m0s | 2m24s (max 2m24s) | 1/1 | 0 | $0 |
| warm-standby | 5s | 330ms (max 330ms) | 2m0s | 49s (max 49s) | 1/1 | 3 | $72 |
