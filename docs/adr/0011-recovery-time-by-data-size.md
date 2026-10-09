# ADR 0011: Recovery time by data size

- Status: accepted
- Date: 2026-10-09

## Context
v1 measured every recovery on a database of a few hundred megabytes. Whether
the targets still hold for a larger database was an open question (spec §14),
and pilot light and warm standby were expected to differ most there: one
restores the data at recovery time, the other keeps a copy streaming.

Two constraints shaped the study. The lab runs on one machine with 87 GB of
free disk, and every gigabyte of data exists about five times at once
(production, both repositories, site b or the warm standby). The vault keeps
every backup and WAL segment for 14 days in compliance mode, so nothing the
study writes there can be cleaned up until then.

## Decision
- **Every drill records the database size** (`data_bytes`, measured after
  preflight). A size study is then a grouping of ordinary drill history
  (`disavery report scaling`), not a separate mode of the drill engine.
- **Sizes 1, 5 and 10 GB**, three runs each, on a lab built for the study and
  destroyed after it. 50 GB does not fit the disk.
- **Ballast compresses like application data**, about fourfold with zstd:
  each 8 KiB row is 2 KiB of random bytes and 6 KiB of zeros, without TOAST
  compression. Random bytes, the v1 seed, are the worst case for backups and
  would have locked about 80 GB in the vault.
- **S1 and S7 on both tiers; not S6.** The random restore test restores a
  randomly chosen backup set, whose size has nothing to do with today's
  database (at "10 GB" one run restored a 75 MB set). Pilot light's rebuild is
  the restore of the current data.
- **Waits are bounded by progress, not by size.** Failing at size, the drills
  showed two fixed limits that were really about data volume: `stanza-create`
  gave up at once on an archive lock held by a WAL backlog, and a rejoining
  standby was given one minute to catch up. The first now retries on lock
  errors for up to ten minutes; the second waits while replay advances and
  fails after 60 s without progress.

## Consequences
- The answer is in [docs/scaling.md](../scaling.md): pilot light's RTO grows by
  6–10 s per GB on top of a fixed 2 m 50 s; warm standby's stays at one minute;
  data loss and the users' outage during failback do not change with size.
- Any history with sizes can be summarised the same way, including nightly CI
  runs (500 MB) and a future cloud profile.
- Repeating the study takes about four hours and a fresh lab; results depend on
  the machine, so they are evidence for this lab, not a sizing rule.
