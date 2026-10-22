# Drill history

Written by the drills workflow; the badge in the README reads badge.json.

## Trend

| Scenario | Tier | Runs | Passed | Last result | Last run (UTC) | Measures |
|---|---|---|---|---|---|---|
| s1-site-loss | pilot-light | 4 | 4 | PASS | 2026-10-21 07:48 | rpo: median 11s, max 12s, target 1m0s, met 4/4<br>rto: median 2m24s, max 2m26s, target 15m0s, met 4/4 |
| s1-site-loss | warm-standby | 4 | 4 | PASS | 2026-10-22 07:47 | rpo: median 580ms, max 890ms, target 5s, met 4/4<br>rto: median 49s, max 52s, target 2m0s, met 4/4 |
| s2-drop-table | — | 3 | 3 | PASS | 2026-10-18 08:58 | rpo: median 600ms, max 850ms, target 5s, met 3/3<br>rto: median 1m10s, max 1m16s, target 15m0s, met 3/3 |
| s3-bad-migration | — | 4 | 4 | PASS | 2026-10-18 08:59 | rpo: median 32s, max 32s, target 1m0s, met 4/4<br>rto: median 1m19s, max 1m25s, target 15m0s, met 4/4 |
| s4-ransomware | — | 4 | 3 | PASS | 2026-10-19 07:57 | rpo: median 15s, max 26s, target 1m0s, met 3/3<br>rto: median 1m31s, max 1m37s, target 30m0s, met 3/3 |
| s5-secret-loss | — | 3 | 3 | PASS | 2026-10-20 07:44 | rpo: median 12s, max 21s, target 1m0s, met 3/3<br>rto: median 2m24s, max 2m27s, target 20m0s, met 3/3 |
| s6-restore-test | — | 15 | 15 | PASS | 2026-10-22 07:44 | recovery_time: median 48s, max 1m4s, target 15m0s, met 15/15 |
| s7-failback | pilot-light | 9 | 7 | PASS | 2026-10-21 07:52 | downtime: median 2.5s, max 3s, target 15m0s, met 7/7<br>rpo: median 0s, max 0s, target 1m0s, met 7/7 |
| s7-failback | warm-standby | 4 | 4 | PASS | 2026-10-22 07:48 | downtime: median 4.5s, max 6s, target 2m0s, met 4/4<br>rpo: median 0s, max 0s, target 5s, met 4/4 |

## DR tiers (site loss, S1)

| Tier | Target RPO | Actual RPO, median | Target RTO | Actual RTO, median | Runs passed | Always-on DR nodes | Est. monthly cost |
|---|---|---|---|---|---|---|---|
| pilot-light | 1m0s | 11s (max 12s) | 15m0s | 2m24s (max 2m26s) | 4/4 | 0 | $0 |
| warm-standby | 5s | 580ms (max 890ms) | 2m0s | 49s (max 52s) | 4/4 | 3 | $72 |
