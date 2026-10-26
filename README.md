# Drill history

Written by the drills workflow; the badge in the README reads badge.json.

## Trend

| Scenario | Tier | Runs | Passed | Last result | Last run (UTC) | Measures |
|---|---|---|---|---|---|---|
| s1-site-loss | pilot-light | 5 | 5 | PASS | 2026-10-25 09:28 | rpo: median 11s, max 13s, target 1m0s, met 5/5<br>rto: median 2m19s, max 2m26s, target 15m0s, met 5/5 |
| s1-site-loss | warm-standby | 5 | 5 | PASS | 2026-10-25 09:36 | rpo: median 580ms, max 890ms, target 5s, met 5/5<br>rto: median 48s, max 52s, target 2m0s, met 5/5 |
| s2-drop-table | — | 5 | 5 | PASS | 2026-10-25 09:16 | rpo: median 600ms, max 850ms, target 5s, met 5/5<br>rto: median 1m12s, max 1m22s, target 15m0s, met 5/5 |
| s3-bad-migration | — | 6 | 6 | PASS | 2026-10-25 09:18 | rpo: median 32s, max 32s, target 1m0s, met 6/6<br>rto: median 1m20s, max 1m25s, target 15m0s, met 6/6 |
| s4-ransomware | — | 6 | 5 | PASS | 2026-10-25 09:20 | rpo: median 20s, max 26s, target 1m0s, met 5/5<br>rto: median 1m30s, max 1m37s, target 30m0s, met 5/5 |
| s5-secret-loss | — | 5 | 5 | PASS | 2026-10-26 07:56 | rpo: median 16s, max 21s, target 1m0s, met 5/5<br>rto: median 2m15s, max 2m27s, target 20m0s, met 5/5 |
| s6-restore-test | — | 20 | 20 | PASS | 2026-10-26 07:55 | recovery_time: median 48s, max 1m4s, target 15m0s, met 20/20 |
| s7-failback | pilot-light | 12 | 10 | PASS | 2026-10-26 07:59 | downtime: median 2.5s, max 3s, target 15m0s, met 10/10<br>rpo: median 0s, max 0s, target 1m0s, met 10/10 |
| s7-failback | warm-standby | 5 | 5 | PASS | 2026-10-25 09:38 | downtime: median 4.5s, max 6s, target 2m0s, met 5/5<br>rpo: median 0s, max 0s, target 5s, met 5/5 |

## DR tiers (site loss, S1)

| Tier | Target RPO | Actual RPO, median | Target RTO | Actual RTO, median | Runs passed | Always-on DR nodes | Est. monthly cost |
|---|---|---|---|---|---|---|---|
| pilot-light | 1m0s | 11s (max 13s) | 15m0s | 2m19s (max 2m26s) | 5/5 | 0 | $0 |
| warm-standby | 5s | 580ms (max 890ms) | 2m0s | 48s (max 52s) | 5/5 | 3 | $72 |
