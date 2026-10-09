# Recovery time by data size

How does recovering the service scale with the amount of data? Site loss (S1)
and failback (S7) on both DR tiers, measured at 1, 5 and 10 GB, three runs
each, on a lab built from scratch for the study (2026-10-09). The decision
behind the method is [ADR 0011](adr/0011-recovery-time-by-data-size.md).

## Results

Medians, with the number of runs in parentheses. `< 0.5 GB` is a fresh lab
(75 MB), measured once as the baseline. Reproduce the table from the
committed history:

```bash
docker compose exec toolbox build/disavery report scaling --history reports/samples/scaling/history.jsonl
```

| Scenario | Tier | Measure | < 0.5 GB | 1 GB | 5 GB | 10 GB |
|---|---|---|---|---|---|---|
| s1-site-loss | pilot-light | drill duration | 3m6s (1) | 3m18s (3) | 4m5s (3) | 4m53s (3) |
| s1-site-loss | pilot-light | rpo | 5.23s (1) | 19s (3) | 16s (3) | 18s (3) |
| s1-site-loss | pilot-light | rto | 2m51s (1) | 3m0s (3) | 3m25s (3) | 4m15s (3) |
| s1-site-loss | warm-standby | drill duration | 1m22s (1) | 1m24s (3) | 1m55s (3) | 2m38s (3) |
| s1-site-loss | warm-standby | rpo | 410ms (1) | 270ms (3) | 920ms (3) | 450ms (3) |
| s1-site-loss | warm-standby | rto | 1m1s (1) | 1m0s (3) | 1m0s (3) | 1m1s (3) |
| s7-failback | pilot-light | downtime | 6.01s (1) | 7s (3) | 8.5s (3) | 6.5s (3) |
| s7-failback | pilot-light | drill duration | 2m46s (1) | 3m25s (3) | 4m59s (3) | 4m57s (3) |
| s7-failback | pilot-light | rpo | 0s (1) | 0s (3) | 0s (3) | 0s (3) |
| s7-failback | warm-standby | downtime | 5s (1) | 8s (3) | 9.5s (2) | 9.01s (3) |
| s7-failback | warm-standby | drill duration | 3m9s (1) | 3m52s (3) | 5m58s (2) | 6m52s (3) |
| s7-failback | warm-standby | rpo | 0s (1) | 0s (3) | 0s (2) | 0s (3) |

Every one of these runs passed. The 5 GB warm-standby failback has two runs:
its third failed, which is how finding 1 below was found.

## What it means

- **Pilot light pays for data at recovery time; warm standby does not.**
  Pilot light's RTO is a fixed 2 m 50 s (detection 20 s, the decision 30 s,
  provisioning and configuring site b) plus 6–10 s per GB to restore from the
  vault: 4 m 15 s at 10 GB. Warm standby's RTO stays at 1 minute from 75 MB to
  10 GB, because the data is already there. The tier comparison
  ([sample](../reports/samples/tier-comparison.md)) widens with size.
- **Where pilot light's target breaks, roughly.** At 6–10 s per GB beyond
  10 GB, the 15-minute target is crossed somewhere between 75 and 120 GB
  (about 90 GB on a straight line through 1–10 GB). That is an
  extrapolation from 10 GB on one machine, not a measurement; the cloud profile
  (spec §14) is where to measure it.
- **Data size does not change data loss.** RPO is set by the archive cadence
  (`archive_timeout` 30 s) and the replication stream, not by how much data
  there is: from 1 to 10 GB pilot light loses 16–19 s, warm standby under a
  second.
- **Failback gets longer, not the outage.** The users' outage during S7 stays
  at 5–10 s. What grows is everything around it: the old primary catching up
  (finding 2), rebuilding the standby, and verification — `pg_amcheck` checks
  every page, 41–80 s at 10 GB.

## What the study found

1. **Warm-standby failback broke on an archive lock** (5 GB, third run).
   Failback re-runs the site playbook, whose pgBackRest role calls
   `stanza-create` on the DR primary. That needs the archive lock, held by the
   asynchronous `archive-push` while it ships a WAL backlog; `stanza-create`
   failed at once with error 50 instead of waiting. More data, longer backlog,
   likelier collision. The role now waits for the lock.
2. **A rejoining standby was given one minute.** The old primary first replays
   from the vault everything site b wrote meanwhile, then streams; the role
   gave up after 60 s while replay was progressing. It now waits as long as
   replay advances and fails after 60 s without progress.
3. **The study seeded the wrong primary after a failure.** With the lab left in
   site b, the next size went into db-b. Each size now starts from a verified
   pilot-light baseline, or the study stops.
4. **The random restore test does not measure size.** S6 restores a randomly
   chosen backup set to a random moment: at "10 GB" one run restored the
   morning's 75 MB backup. Its time depends on the set and the WAL replayed,
   not on today's database, so it is left out of this study
   (`report scaling --exclude`); pilot light's rebuild is the restore that
   scales with the current size.

## Method

- Ballast rows (`disavery seed`) are 2 KiB of random bytes and 6 KiB of zeros,
  stored without TOAST compression: the database holds the full size, while
  backups and WAL compress about fourfold with zstd, as typical application
  data would. The application never reads the ballast.
- `scripts/drill-programme.sh scaling` resets the lab to pilot light, seeds up
  to each size (a full backup to both repositories follows), then runs S1 and
  S7 on pilot light and on warm standby, three times.
- Every drill records the database size with its result (`data_bytes`);
  `disavery report scaling` groups recovered runs by size, rounded to the
  nearest GB.
- One run is left out of the published history: the failback that completed
  finding 1's broken run by hand, which started half-way and is not comparable.
- 50 GB, the spec's largest size, did not fit: every size exists about five
  times at once (production, both repositories, site b or the warm standby),
  and the vault keeps everything for 14 days. The study peaked at about 55 GB
  of the host's disk.
