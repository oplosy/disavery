# Drill history

Written by the drills workflow; the badge in the README reads badge.json.

## Trend

| Scenario | Tier | Runs | Passed | Last result | Last run (UTC) | Measures |
|---|---|---|---|---|---|---|
| s1-site-loss | pilot-light | 10 | 10 | PASS | 2026-11-20 10:10 | rpo: median 11s, max 21s, target 1m0s, met 10/10<br>rto: median 2m22s, max 2m26s, target 15m0s, met 10/10 |
| s1-site-loss | warm-standby | 11 | 11 | PASS | 2026-11-21 10:00 | rpo: median 460ms, max 890ms, target 5s, met 11/11<br>rto: median 47s, max 52s, target 2m0s, met 11/11 |
| s2-drop-table | — | 9 | 9 | PASS | 2026-11-10 10:01 | rpo: median 450ms, max 850ms, target 5s, met 9/9<br>rto: median 1m14s, max 1m22s, target 15m0s, met 9/9 |
| s3-bad-migration | — | 11 | 11 | PASS | 2026-11-17 10:53 | rpo: median 32s, max 33s, target 1m0s, met 11/11<br>rto: median 1m21s, max 1m25s, target 15m0s, met 11/11 |
| s4-ransomware | — | 11 | 10 | PASS | 2026-11-18 10:18 | rpo: median 19s, max 26s, target 1m0s, met 10/10<br>rto: median 1m30s, max 1m37s, target 30m0s, met 10/10 |
| s5-secret-loss | — | 10 | 10 | PASS | 2026-11-19 10:30 | rpo: median 16s, max 23s, target 1m0s, met 10/10<br>rto: median 2m22s, max 2m29s, target 20m0s, met 10/10 |
| s6-restore-test | — | 45 | 45 | PASS | 2026-11-21 09:57 | recovery_time: median 47s, max 1m4s, target 15m0s, met 45/45 |
| s7-failback | pilot-light | 22 | 20 | PASS | 2026-11-20 10:13 | downtime: median 2.5s, max 5s, target 15m0s, met 20/20<br>rpo: median 0s, max 0s, target 1m0s, met 20/20 |
| s7-failback | warm-standby | 11 | 11 | PASS | 2026-11-21 10:01 | downtime: median 3.5s, max 6s, target 2m0s, met 11/11<br>rpo: median 0s, max 0s, target 5s, met 11/11 |

## DR tiers (site loss, S1)

| Tier | Target RPO | Actual RPO, median | Target RTO | Actual RTO, median | Runs passed | Always-on DR nodes | Est. monthly cost |
|---|---|---|---|---|---|---|---|
| pilot-light | 1m0s | 11s (max 21s) | 15m0s | 2m22s (max 2m26s) | 10/10 | 0 | $0 |
| warm-standby | 5s | 460ms (max 890ms) | 2m0s | 47s (max 52s) | 11/11 | 3 | $72 |
