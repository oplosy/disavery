# ADR 0005: Checkpoint right after PostgreSQL starts

- Status: accepted
- Date: 2026-10-07

## Context
Pilot light's RPO rests on `archive_timeout = 30s`: at least every 30 seconds
the primary closes its WAL segment and pgBackRest ships it to the vault, so a
site loss costs at most about half a minute of writes (target: 60 s).

The first measured pilot-light site loss (S1) lost 72 seconds of acknowledged
writes. The primary had been restarted after a crash a few minutes earlier,
and from that restart until the incident PostgreSQL had not switched a single
segment on its own. Reproduced on PostgreSQL 16.15: after crash recovery the
checkpointer goes back to sleep for a full `checkpoint_timeout` (5 min)
before it evaluates `archive_timeout` again, so for up to five minutes after
every crash restart WAL is only archived when a segment fills up. A single
`CHECKPOINT` wakes the checkpointer and the 30-second cadence returns at once.

A second, independent mistake made it look fine: the drill counted only writes
acknowledged since the drill started, half a second before the incident, so
it reported an RPO of 0.5 s.

## Decision
- Every PostgreSQL unit gets a systemd drop-in that, after each start, waits
  until the server accepts connections and runs `CHECKPOINT`. It is never fatal
  for the service.
- Preflight requires the last archived segment to be at most 45 s old
  (`archive_timeout` plus margin), down from 90 s, so a primary that is not
  archiving on schedule stops a drill before anything is broken.
- The canary measurement looks back 15 minutes before the incident for the last
  surviving write and only counts losses after it; a regression test pins the
  72-second case.

## Consequences
- After a crash restart the RPO bound of pilot light holds again; the re-run
  measured 24 s, matching the 24 s archive age at the incident.
- One extra checkpoint per start: negligible.
- This is the kind of failure only a measured drill finds: backups, archiving
  and `pgbackrest check` all looked healthy throughout.

## Update 2026-10-08: restored clusters checkpoint after the promotion
A ransomware drill (S4) that ran a minute after a bad-migration rewind (S3)
lost 2 m 15 s of writes against a 60 s target. The rewound primary had
archived nothing for more than a minute after its promotion: the unit's
checkpoint after start had run while the restore was still recovering, and the
promotion's end-of-recovery checkpoint put the checkpointer to sleep again.
Promoted streaming standbys (S1 warm standby, S7) were not affected; their
archive kept its 30-second cadence. Every restore that becomes a primary
(role `restore`: S1 and S5 pilot light, S3, S4) now runs `CHECKPOINT` after the
promotion, and the S3-then-S4 sequence is part of the plan 4 verification.
