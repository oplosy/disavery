# Drill history

Written by the drills workflow; the badge in the README reads badge.json.

## Trend

| Scenario | Tier | Runs | Passed | Last result | Last run (UTC) | Measures |
|---|---|---|---|---|---|---|
| s1-site-loss | pilot-light | 8 | 8 | PASS | 2026-11-02 09:40 | rpo: median 12s, max 21s, target 1m0s, met 8/8<br>rto: median 2m22s, max 2m26s, target 15m0s, met 8/8 |
| s1-site-loss | warm-standby | 9 | 9 | PASS | 2026-11-09 10:00 | rpo: median 460ms, max 890ms, target 5s, met 9/9<br>rto: median 48s, max 52s, target 2m0s, met 9/9 |
| s2-drop-table | — | 9 | 9 | PASS | 2026-11-10 10:01 | rpo: median 450ms, max 850ms, target 5s, met 9/9<br>rto: median 1m14s, max 1m22s, target 15m0s, met 9/9 |
| s3-bad-migration | — | 10 | 10 | PASS | 2026-11-11 09:35 | rpo: median 32s, max 33s, target 1m0s, met 10/10<br>rto: median 1m21s, max 1m25s, target 15m0s, met 10/10 |
| s4-ransomware | — | 10 | 9 | PASS | 2026-11-12 10:00 | rpo: median 19s, max 26s, target 1m0s, met 9/9<br>rto: median 1m30s, max 1m37s, target 30m0s, met 9/9 |
| s5-secret-loss | — | 9 | 9 | PASS | 2026-11-13 09:42 | rpo: median 16s, max 23s, target 1m0s, met 9/9<br>rto: median 2m22s, max 2m29s, target 20m0s, met 9/9 |
| s6-restore-test | — | 38 | 38 | PASS | 2026-11-13 09:41 | recovery_time: median 48s, max 1m4s, target 15m0s, met 38/38 |
| s7-failback | pilot-light | 19 | 17 | PASS | 2026-11-13 09:46 | downtime: median 2.5s, max 5s, target 15m0s, met 17/17<br>rpo: median 0s, max 0s, target 1m0s, met 17/17 |
| s7-failback | warm-standby | 9 | 9 | PASS | 2026-11-09 10:02 | downtime: median 4s, max 6s, target 2m0s, met 9/9<br>rpo: median 0s, max 0s, target 5s, met 9/9 |

## DR tiers (site loss, S1)

| Tier | Target RPO | Actual RPO, median | Target RTO | Actual RTO, median | Runs passed | Always-on DR nodes | Est. monthly cost |
|---|---|---|---|---|---|---|---|
| pilot-light | 1m0s | 12s (max 21s) | 15m0s | 2m22s (max 2m26s) | 8/8 | 0 | $0 |
| warm-standby | 5s | 460ms (max 890ms) | 2m0s | 48s (max 52s) | 9/9 | 3 | $72 |
