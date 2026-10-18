# Drill history

Written by the drills workflow; the badge in the README reads badge.json.

## Trend

| Scenario | Tier | Runs | Passed | Last result | Last run (UTC) | Measures |
|---|---|---|---|---|---|---|
| s1-site-loss | pilot-light | 3 | 3 | PASS | 2026-10-15 07:36 | rpo: median 11s, max 12s, target 1m0s, met 3/3<br>rto: median 2m24s, max 2m26s, target 15m0s, met 3/3 |
| s1-site-loss | warm-standby | 3 | 3 | PASS | 2026-10-16 07:21 | rpo: median 580ms, max 890ms, target 5s, met 3/3<br>rto: median 49s, max 52s, target 2m0s, met 3/3 |
| s2-drop-table | — | 2 | 2 | PASS | 2026-10-17 07:06 | rpo: median 850ms, max 850ms, target 5s, met 2/2<br>rto: median 1m10s, max 1m10s, target 15m0s, met 2/2 |
| s3-bad-migration | — | 3 | 3 | PASS | 2026-10-18 07:22 | rpo: median 32s, max 32s, target 1m0s, met 3/3<br>rto: median 1m18s, max 1m19s, target 15m0s, met 3/3 |
| s4-ransomware | — | 2 | 2 | PASS | 2026-10-13 09:37 | rpo: median 26s, max 26s, target 1m0s, met 2/2<br>rto: median 1m37s, max 1m37s, target 30m0s, met 2/2 |
| s5-secret-loss | — | 2 | 2 | PASS | 2026-10-14 07:47 | rpo: median 21s, max 21s, target 1m0s, met 2/2<br>rto: median 2m24s, max 2m24s, target 20m0s, met 2/2 |
| s6-restore-test | — | 10 | 10 | PASS | 2026-10-18 07:21 | recovery_time: median 49s, max 1m4s, target 15m0s, met 10/10 |
| s7-failback | pilot-light | 7 | 5 | PASS | 2026-10-15 07:39 | downtime: median 2.5s, max 3s, target 15m0s, met 5/5<br>rpo: median 0s, max 0s, target 1m0s, met 5/5 |
| s7-failback | warm-standby | 3 | 3 | PASS | 2026-10-16 07:23 | downtime: median 3s, max 6s, target 2m0s, met 3/3<br>rpo: median 0s, max 0s, target 5s, met 3/3 |

## DR tiers (site loss, S1)

| Tier | Target RPO | Actual RPO, median | Target RTO | Actual RTO, median | Runs passed | Always-on DR nodes | Est. monthly cost |
|---|---|---|---|---|---|---|---|
| pilot-light | 1m0s | 11s (max 12s) | 15m0s | 2m24s (max 2m26s) | 3/3 | 0 | $0 |
| warm-standby | 5s | 580ms (max 890ms) | 2m0s | 49s (max 52s) | 3/3 | 3 | $72 |
