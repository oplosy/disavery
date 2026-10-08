# ADR 0006: Vault identities and the delete-marker blast radius

- Status: accepted
- Date: 2026-10-08

## Context
Production writes to the vault as `prodwriter`: pgBackRest pushes WAL and
backups to repo2, and site stores replicate attachments. The vault's buckets
use Object Lock in compliance mode, and the writer's policy denies every
version-level destruction (`DeleteObjectVersion`, retention changes,
governance bypass, lifecycle, versioning, bucket deletion).

The writer still has `s3:DeleteObject`, because pgBackRest expires old backups
with it. In a versioned bucket a plain delete destroys nothing: it places a
delete marker over the current version. The S4 drill, an attacker holding the
writer's key, showed what that still allows: every one of about 1,800 objects
in both buckets was hidden behind a marker within seconds. The locked versions
were intact, yet `pgbackrest info` saw no backup, and a restore through any
normal listing would have found an empty vault.

Until then every restore also used the writer's key, so a restore needed an
identity that can write to the backups it depends on.

## Decision
- Keep `s3:DeleteObject` for the writer. Removing it would break pgBackRest's
  expiry, and the vault's own lifecycle cannot know which backup sets are
  still needed. The attack's worst case is a hidden, not a lost, backup.
- Only the vault administrator, a separate account whose credentials
  production does not use, removes delete markers: the writer may not delete
  any version, markers included, so an attacker cannot undo the repair either.
  `disavery vault undelete --since T` removes the markers placed since the
  attack and leaves older ones (expired backups) alone.
- Restores read through a third identity, `vaultreader`, with list and read
  permissions only: the isolated restore node is configured with it, and every
  restore command (S1 pilot light, S2–S6) overrides the writer through
  pgBackRest's environment variables. A node that becomes the primary keeps the
  writer in its configuration, because it must archive from its first moment.
- S4 attacks the vault with the writer's key on every run (`disavery vault
  attack`) and fails the drill if any destructive request is allowed; the
  report keeps the count of refused requests per action and bucket.

## Consequences
- Immutability is proven by a drill, not assumed: in S4 the vault refused all
  version deletions, retention changes, governance bypass, lifecycle,
  versioning and bucket deletion attempts, and the restore from it passed.
- Hidden backups add the administrator's undelete to the recovery path
  (seconds in the drill); the runbook makes it an explicit step.
- Credential rotation of the stolen writer key is out of scope for v1; the
  blast radius of that key is documented here instead.
