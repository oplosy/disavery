# ADR 0008: Point-in-time restores come from the vault

- Status: accepted
- Date: 2026-10-08

## Context
Every database node has two pgBackRest repositories: repo1 on its own disk
and repo2 in the vault. Both receive the node's backups and WAL, and repo1 is
the faster one to restore from.

The bad-migration drill (S3) rewound db-a in place from repo1 and passed, until
it ran after a site loss and a failback. Then PostgreSQL refused to start:
"recovery ended before configured recovery target was reached". The newest
backup in db-a's repo1 was from before the site loss; to reach the target,
recovery had to follow the timelines of the site-b primary that served in
between, and site b had archived that WAL to its own repo1 and to the vault,
never to db-a's disk. The failed drill left production down until it was
rewound from the vault.

## Decision
- Every restore that must reach a point in time or the end of the archive
  reads repo2, the vault, where every primary of every site archives: the
  isolated restore (S2, S6), the in-place rewind (S3) and the rebuilds (S1, S4,
  S5). S3 runs `rebuild-site.yml` with a `restore_target`, which restores
  through the read-only vault user ([ADR 0006](0006-vault-identities-and-delete-markers.md)).
- repo1 stays as a node-local copy of the node's own history; no runbook
  depends on it.

## Consequences
- A rewind works regardless of which site served before it; the S3 re-run
  after the failover cycle passed with a 45-second rewind.
- Restores pay the vault's latency instead of a local disk: in the lab the
  difference is a few seconds; at real data sizes it belongs in the RTO
  budget (spec §14's data-size study).
- The playbook formerly named `pilot-light.yml` is now `rebuild-site.yml`
  (extra vars `target_site`, `restore_target`), since it serves S1, S3, S4 and S5.
