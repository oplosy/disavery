# ADR 0010: Copy attachments back as replicas

- Status: accepted
- Date: 2026-10-08

## Context
`disavery attachments copy` refills a site store from the vault (S1 pilot
light, S4, S5) or from the other site (S7 failback). The target store
replicates to the vault (rule priority 1), so every object the copy wrote
reached the vault again as one more version of an attachment the vault already
held. After a day of drills one attachment had 26 versions and the vault about
95,000; each extra version is kept for 14 days under compliance retention and
had to be locked by the sweeper ([ADR 0004](0004-lock-replicas-with-a-sweeper.md)).

## Decision
The copy writes a version the vault already holds the way MinIO's own
replication writes it: a PUT with the replication request headers, status
`REPLICA` and the source's version ID. The target keeps the vault's version ID
and does not replicate a replica, so the vault gets nothing new. Copying from
the vault, every version qualifies; copying from another site, the copy asks
the vault for that exact version through the read-only user (which already
has `s3:GetObjectVersion`) and writes versions the vault lacks as ordinary new
versions, which replicate as before. The replica carries no source mtime, so
it is dated at the copy and becomes current even over a newer delete marker.

Rejected:
- Disabling the vault rule during the copy: production writes in that window
  would not reach the vault (MinIO catches up only through its slow scanner),
  and a failed copy would leave replication off.
- A tag filter on the rule, with the application tagging its writes: changes
  the production write path, and untagged existing objects would no longer
  replicate.
- Removing or not locking duplicate versions in the vault: weakens Object
  Lock, which the design must not do.

## Consequences
- A refill adds no versions to the vault; neither IAM policies nor Object Lock
  change. In the S4 re-run the copy wrote 7,444 attachments, all as replicas,
  and the vault grew by 223 versions: exactly the 223 new attachments the
  application wrote during the drill. The site root, which the copy already uses, may send replication
  requests.
- The copy depends on MinIO-specific request headers (minio-go's
  `PutObjectOptions.Internal`); another S3 implementation would ignore them or
  reject the request. An integration test against the pinned MinIO build
  proves the behaviour, and multipart uploads are off for replicas because
  only the single PUT was verified.
- A replica is not replicated to a warm standby either. No runbook needs it:
  every copy fills the store that becomes active with no standby configured,
  and a standby added later is seeded from the active store by `site.yml`.
