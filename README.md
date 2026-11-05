# Drill history

Written by the drills workflow; the badge in the README reads badge.json.

## Trend

| Scenario | Tier | Runs | Passed | Last result | Last run (UTC) | Measures |
|---|---|---|---|---|---|---|
| s1-site-loss | pilot-light | 8 | 8 | PASS | 2026-11-02 09:40 | rpo: median 12s, max 21s, target 1m0s, met 8/8<br>rto: median 2m22s, max 2m26s, target 15m0s, met 8/8 |
| s1-site-loss | warm-standby | 8 | 8 | PASS | 2026-11-03 09:45 | rpo: median 460ms, max 890ms, target 5s, met 8/8<br>rto: median 48s, max 52s, target 2m0s, met 8/8 |
| s2-drop-table | — | 8 | 8 | PASS | 2026-11-04 09:25 | rpo: median 520ms, max 850ms, target 5s, met 8/8<br>rto: median 1m14s, max 1m22s, target 15m0s, met 8/8 |
| s3-bad-migration | — | 9 | 9 | PASS | 2026-11-05 09:38 | rpo: median 32s, max 33s, target 1m0s, met 9/9<br>rto: median 1m21s, max 1m25s, target 15m0s, met 9/9 |
| s4-ransomware | — | 8 | 7 | PASS | 2026-11-01 11:27 | rpo: median 20s, max 26s, target 1m0s, met 7/7<br>rto: median 1m30s, max 1m37s, target 30m0s, met 7/7 |
| s5-secret-loss | — | 7 | 7 | PASS | 2026-11-01 11:29 | rpo: median 16s, max 23s, target 1m0s, met 7/7<br>rto: median 2m18s, max 2m27s, target 20m0s, met 7/7 |
| s6-restore-test | — | 31 | 31 | PASS | 2026-11-05 09:37 | recovery_time: median 48s, max 1m4s, target 15m0s, met 31/31 |
| s7-failback | pilot-light | 17 | 15 | PASS | 2026-11-02 09:43 | downtime: median 2.5s, max 5s, target 15m0s, met 15/15<br>rpo: median 0s, max 0s, target 1m0s, met 15/15 |
| s7-failback | warm-standby | 8 | 8 | PASS | 2026-11-03 09:47 | downtime: median 4s, max 6s, target 2m0s, met 8/8<br>rpo: median 0s, max 0s, target 5s, met 8/8 |

## DR tiers (site loss, S1)

| Tier | Target RPO | Actual RPO, median | Target RTO | Actual RTO, median | Runs passed | Always-on DR nodes | Est. monthly cost |
|---|---|---|---|---|---|---|---|
| pilot-light | 1m0s | 12s (max 21s) | 15m0s | 2m22s (max 2m26s) | 8/8 | 0 | $0 |
| warm-standby | 5s | 460ms (max 890ms) | 2m0s | 48s (max 52s) | 8/8 | 3 | $72 |
