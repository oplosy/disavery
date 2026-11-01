# Drill history

Written by the drills workflow; the badge in the README reads badge.json.

## Trend

| Scenario | Tier | Runs | Passed | Last result | Last run (UTC) | Measures |
|---|---|---|---|---|---|---|
| s1-site-loss | pilot-light | 6 | 6 | PASS | 2026-10-27 08:01 | rpo: median 11s, max 13s, target 1m0s, met 6/6<br>rto: median 2m19s, max 2m26s, target 15m0s, met 6/6 |
| s1-site-loss | warm-standby | 6 | 6 | PASS | 2026-10-28 08:44 | rpo: median 580ms, max 890ms, target 5s, met 6/6<br>rto: median 48s, max 52s, target 2m0s, met 6/6 |
| s2-drop-table | — | 6 | 6 | PASS | 2026-10-29 08:38 | rpo: median 600ms, max 850ms, target 5s, met 6/6<br>rto: median 1m12s, max 1m22s, target 15m0s, met 6/6 |
| s3-bad-migration | — | 7 | 7 | PASS | 2026-10-30 08:48 | rpo: median 32s, max 32s, target 1m0s, met 7/7<br>rto: median 1m20s, max 1m25s, target 15m0s, met 7/7 |
| s4-ransomware | — | 7 | 6 | PASS | 2026-10-31 08:34 | rpo: median 22s, max 26s, target 1m0s, met 6/6<br>rto: median 1m30s, max 1m37s, target 30m0s, met 6/6 |
| s5-secret-loss | — | 6 | 6 | PASS | 2026-11-01 09:24 | rpo: median 16s, max 21s, target 1m0s, met 6/6<br>rto: median 2m22s, max 2m27s, target 20m0s, met 6/6 |
| s6-restore-test | — | 26 | 26 | PASS | 2026-11-01 09:24 | recovery_time: median 48s, max 1m4s, target 15m0s, met 26/26 |
| s7-failback | pilot-light | 14 | 12 | PASS | 2026-11-01 09:28 | downtime: median 2.5s, max 3s, target 15m0s, met 12/12<br>rpo: median 0s, max 0s, target 1m0s, met 12/12 |
| s7-failback | warm-standby | 6 | 6 | PASS | 2026-10-28 08:46 | downtime: median 4.5s, max 6s, target 2m0s, met 6/6<br>rpo: median 0s, max 0s, target 5s, met 6/6 |

## DR tiers (site loss, S1)

| Tier | Target RPO | Actual RPO, median | Target RTO | Actual RTO, median | Runs passed | Always-on DR nodes | Est. monthly cost |
|---|---|---|---|---|---|---|---|
| pilot-light | 1m0s | 11s (max 13s) | 15m0s | 2m19s (max 2m26s) | 6/6 | 0 | $0 |
| warm-standby | 5s | 580ms (max 890ms) | 2m0s | 48s (max 52s) | 6/6 | 3 | $72 |
