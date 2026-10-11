# Drill history

Written by the drills workflow; the badge in the README reads badge.json.

## Trend

| Scenario | Tier | Runs | Passed | Last result | Last run (UTC) | Measures |
|---|---|---|---|---|---|---|
| s1-site-loss | pilot-light | 2 | 2 | PASS | 2026-10-11 11:06 | rpo: median 12s, max 12s, target 1m0s, met 2/2<br>rto: median 2m24s, max 2m24s, target 15m0s, met 2/2 |
| s1-site-loss | warm-standby | 2 | 2 | PASS | 2026-10-11 11:13 | rpo: median 580ms, max 580ms, target 5s, met 2/2<br>rto: median 52s, max 52s, target 2m0s, met 2/2 |
| s2-drop-table | — | 1 | 1 | PASS | 2026-10-11 10:55 | rpo: median 850ms, max 850ms, target 5s, met 1/1<br>rto: median 1m7s, max 1m7s, target 15m0s, met 1/1 |
| s3-bad-migration | — | 1 | 1 | PASS | 2026-10-11 10:57 | rpo: median 32s, max 32s, target 1m0s, met 1/1<br>rto: median 1m12s, max 1m12s, target 15m0s, met 1/1 |
| s4-ransomware | — | 1 | 1 | PASS | 2026-10-11 10:59 | rpo: median 15s, max 15s, target 1m0s, met 1/1<br>rto: median 1m20s, max 1m20s, target 30m0s, met 1/1 |
| s5-secret-loss | — | 1 | 1 | PASS | 2026-10-11 11:01 | rpo: median 21s, max 21s, target 1m0s, met 1/1<br>rto: median 2m5s, max 2m5s, target 20m0s, met 1/1 |
| s6-restore-test | — | 3 | 3 | PASS | 2026-10-11 10:55 | recovery_time: median 49s, max 1m4s, target 15m0s, met 3/3 |
| s7-failback | pilot-light | 5 | 3 | PASS | 2026-10-11 11:09 | downtime: median 2.5s, max 3s, target 15m0s, met 3/3<br>rpo: median 0s, max 0s, target 1m0s, met 3/3 |
| s7-failback | warm-standby | 2 | 2 | PASS | 2026-10-11 11:15 | downtime: median 6s, max 6s, target 2m0s, met 2/2<br>rpo: median 0s, max 0s, target 5s, met 2/2 |

## DR tiers (site loss, S1)

| Tier | Target RPO | Actual RPO, median | Target RTO | Actual RTO, median | Runs passed | Always-on DR nodes | Est. monthly cost |
|---|---|---|---|---|---|---|---|
| pilot-light | 1m0s | 12s (max 12s) | 15m0s | 2m24s (max 2m24s) | 2/2 | 0 | $0 |
| warm-standby | 5s | 580ms (max 580ms) | 2m0s | 52s (max 52s) | 2/2 | 3 | $72 |
