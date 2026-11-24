# Drill history

Written by the drills workflow; the badge in the README reads badge.json.

## Trend

| Scenario | Tier | Runs | Passed | Last result | Last run (UTC) | Measures |
|---|---|---|---|---|---|---|
| s1-site-loss | pilot-light | 11 | 11 | PASS | 2026-11-22 12:52 | rpo: median 11s, max 21s, target 1m0s, met 11/11<br>rto: median 2m19s, max 2m26s, target 15m0s, met 11/11 |
| s1-site-loss | warm-standby | 12 | 12 | PASS | 2026-11-22 12:59 | rpo: median 460ms, max 890ms, target 5s, met 12/12<br>rto: median 48s, max 52s, target 2m0s, met 12/12 |
| s2-drop-table | — | 11 | 11 | PASS | 2026-11-22 12:41 | rpo: median 520ms, max 850ms, target 5s, met 11/11<br>rto: median 1m14s, max 1m22s, target 15m0s, met 11/11 |
| s3-bad-migration | — | 13 | 13 | PASS | 2026-11-23 10:55 | rpo: median 32s, max 33s, target 1m0s, met 13/13<br>rto: median 1m20s, max 1m25s, target 15m0s, met 13/13 |
| s4-ransomware | — | 13 | 12 | PASS | 2026-11-24 12:38 | rpo: median 17s, max 26s, target 1m0s, met 12/12<br>rto: median 1m30s, max 1m37s, target 30m0s, met 12/12 |
| s5-secret-loss | — | 11 | 11 | PASS | 2026-11-22 12:47 | rpo: median 16s, max 23s, target 1m0s, met 11/11<br>rto: median 2m18s, max 2m29s, target 20m0s, met 11/11 |
| s6-restore-test | — | 49 | 49 | PASS | 2026-11-24 12:37 | recovery_time: median 46s, max 1m4s, target 15m0s, met 49/49 |
| s7-failback | pilot-light | 24 | 22 | PASS | 2026-11-22 12:55 | downtime: median 2.5s, max 5s, target 15m0s, met 22/22<br>rpo: median 0s, max 0s, target 1m0s, met 22/22 |
| s7-failback | warm-standby | 12 | 12 | PASS | 2026-11-22 13:01 | downtime: median 3.5s, max 6s, target 2m0s, met 12/12<br>rpo: median 0s, max 0s, target 5s, met 12/12 |

## DR tiers (site loss, S1)

| Tier | Target RPO | Actual RPO, median | Target RTO | Actual RTO, median | Runs passed | Always-on DR nodes | Est. monthly cost |
|---|---|---|---|---|---|---|---|
| pilot-light | 1m0s | 11s (max 21s) | 15m0s | 2m19s (max 2m26s) | 11/11 | 0 | $0 |
| warm-standby | 5s | 460ms (max 890ms) | 2m0s | 48s (max 52s) | 12/12 | 3 | $72 |
