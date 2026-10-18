# Drill history

Written by the drills workflow; the badge in the README reads badge.json.

## Trend

| Scenario | Tier | Runs | Passed | Last result | Last run (UTC) | Measures |
|---|---|---|---|---|---|---|
| s1-site-loss | pilot-light | 3 | 3 | ERROR (refused) | 2026-10-18 09:18 | rpo: median 11s, max 12s, target 1m0s, met 3/3<br>rto: median 2m24s, max 2m26s, target 15m0s, met 3/3 |
| s1-site-loss | warm-standby | 3 | 3 | ERROR (refused) | 2026-10-18 09:40 | rpo: median 580ms, max 890ms, target 5s, met 3/3<br>rto: median 49s, max 52s, target 2m0s, met 3/3 |
| s2-drop-table | — | 3 | 3 | PASS | 2026-10-18 08:58 | rpo: median 600ms, max 850ms, target 5s, met 3/3<br>rto: median 1m10s, max 1m16s, target 15m0s, met 3/3 |
| s3-bad-migration | — | 4 | 4 | PASS | 2026-10-18 08:59 | rpo: median 32s, max 32s, target 1m0s, met 4/4<br>rto: median 1m19s, max 1m25s, target 15m0s, met 4/4 |
| s4-ransomware | — | 3 | 2 | FAILED | 2026-10-18 09:02 | rpo: median 26s, max 26s, target 1m0s, met 2/2<br>rto: median 1m37s, max 1m37s, target 30m0s, met 2/2 |
| s5-secret-loss | — | 2 | 2 | ERROR (refused) | 2026-10-18 09:08 | rpo: median 21s, max 21s, target 1m0s, met 2/2<br>rto: median 2m24s, max 2m24s, target 20m0s, met 2/2 |
| s6-restore-test | — | 11 | 11 | PASS | 2026-10-18 08:57 | recovery_time: median 48s, max 1m4s, target 15m0s, met 11/11 |
| s7-failback | pilot-light | 7 | 5 | ERROR (refused) | 2026-10-18 09:23 | downtime: median 2.5s, max 3s, target 15m0s, met 5/5<br>rpo: median 0s, max 0s, target 1m0s, met 5/5 |
| s7-failback | warm-standby | 3 | 3 | ERROR (refused) | 2026-10-18 09:45 | downtime: median 3s, max 6s, target 2m0s, met 3/3<br>rpo: median 0s, max 0s, target 5s, met 3/3 |

## DR tiers (site loss, S1)

| Tier | Target RPO | Actual RPO, median | Target RTO | Actual RTO, median | Runs passed | Always-on DR nodes | Est. monthly cost |
|---|---|---|---|---|---|---|---|
| pilot-light | 1m0s | 11s (max 12s) | 15m0s | 2m24s (max 2m26s) | 3/3 | 0 | $0 |
| warm-standby | 5s | 580ms (max 890ms) | 2m0s | 49s (max 52s) | 3/3 | 3 | $72 |
