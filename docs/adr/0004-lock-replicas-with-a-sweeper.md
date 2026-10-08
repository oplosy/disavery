# ADR 0004: Lock replicated vault objects with a sweeper

- Status: accepted
- Date: 2026-10-07

## Context
Attachments reach the vault through MinIO bucket replication from the site
store. The vault bucket has Object Lock with a default compliance retention, but
MinIO does not apply a bucket's default retention to replicated objects: a
replica carries the source object's lock settings, and the site bucket has none.
Replicas therefore arrived unlocked and the vault root could delete them, which
breaks the immutability the design relies on (scenario S4). pgBackRest writes
directly to the vault, so its objects get the default retention and are not
affected.

## Decision
A systemd timer on the vault (`vault-lock-sweeper.timer`, every minute) applies
compliance retention of `vault_retention_days` to every version in the
replicated buckets that has no retention yet. Versions that are already locked
are skipped, so their retention is never extended. The smoke test waits for the
lock and then proves that neither the production writer nor the vault root can
delete the version.

Alternatives considered: Object Lock on the site bucket (replicas would inherit
it, but production attachments would become undeletable on the site too, and in
governance mode at best) and replacing replication with application dual-writes
(a larger design change).

## Consequences
- A replicated attachment is unprotected for up to about a minute plus the
  replication lag. Production credentials still cannot delete versions during
  that window (IAM policy); only the vault root can.
- The sweeper lists the retention of every version each run; fine for lab
  sizes, but a production setup would use event notifications instead.

## Update 2026-10-08: incremental sweeps
After a day of drills the vault held about 95,000 attachment versions, and
reading the retention of every one took over a minute per run. A new replica
then stayed unlocked for more than 90 seconds and the smoke test failed. A run
now lists the versions once (12 s for 95,000) and checks only those modified
since the previous successful run, with a two-minute overlap for replication
lag; it locks them in parallel. A full sweep still runs every hour for replicas
that keep an older modification time (existing-object replication), which can
therefore stay unlocked for up to an hour. Incremental runs take about 5 s of
CPU; locking the 2,700 replicas a ransomware drill (S4) copies back took one
run of about 1.5 minutes.

The growth itself comes from copying attachments back into a site store that
replicates to the vault (S4, S5, S7): every copy arrives in the vault as one
more version of an object it already holds.
