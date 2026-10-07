# Business impact analysis: docsvc

`docsvc` stores customer documents and their payment status. It is classified
**Tier-1**: an outage stops document intake and payment confirmation, and a lost
document is a lost customer record. The numbers below live in
[`bia.yaml`](bia.yaml), which the `disavery` CLI reads; this page explains them.

## Recovery targets per DR tier

| DR tier | Target RPO | Target RTO | Mechanism |
|---|---|---|---|
| Pilot light | ≤ 60 s | ≤ 15 min | pgBackRest async WAL archive (`archive_timeout = 30s`); site-b is built from scratch |
| Warm standby | ≤ 5 s | ≤ 2 min | Async streaming replica; site-b always running |

- **RPO** is bounded by how often WAL leaves the primary. With archiving only,
  a segment is shipped at least every 30 s (`archive_timeout`), so up to about
  30 s of commits plus shipping time can be lost; 60 s leaves headroom. A
  streaming replica receives WAL continuously, so seconds are realistic.
- **RTO** for pilot light is dominated by provisioning and restoring site-b;
  for warm standby by detection, the decision and promotion.

Actual RPO and RTO are measured from the outside by every drill (spec §6):
the canary writer journals each write the application acknowledged, and the
prober records availability every 500 ms through the public endpoint.

### RTO phases

Reports break RTO into **detect** (alert fires) → **decide** (disaster declared;
a fixed, configurable delay in drills) → **recover** → **verify**. The decide
phase is real in production and deliberately not optimised away in drills.

## Random restore test (S6)

A backup that has never been restored is a hope, not a backup. Every S6 run
picks a random backup set from the immutable vault and a random point in time
the WAL archive can reach, restores it to an isolated node and proves it:

| Threshold | Value | Why |
|---|---|---|
| `restore_test.max_duration` | 15 min | Restore plus verification must fit the pilot-light RTO; a slower restore means pilot light cannot meet its target |
| `restore_test.pitr_tolerance` | 1 s | Writes in flight within this window of the target may land on either side and are not judged |

The restore is **exact** when every canary write acknowledged before the
target is present and none sent after it is. It is **consistent** when
`pg_amcheck` finds no corruption, business rules hold, document counts match
production at the target, and every document's attachment exists in the vault.

## Preflight thresholds

Drills refuse to start unless the environment is healthy, so a failed drill
always means a failed recovery, not a broken lab.

| Threshold | Value | Why |
|---|---|---|
| `preflight.max_backup_age` | 2 h | Incremental backups run hourly to both repositories |
| `preflight.max_archive_age` | 45 s | `archive_timeout` is 30 s and the canary writes every second, so a segment ships at least every 30 s; anything older means the primary is not archiving on schedule ([ADR 0005](adr/0005-checkpoint-after-start.md)) |
| `preflight.max_canary_silence` | 10 s | The canary writes every second; silence means the measurement would be blind |

## Retention

| What | Policy |
|---|---|
| Full backups | Daily, 7 kept in repo1 (local), 14 in repo2 (vault) |
| Incremental backups | Hourly, both repositories |
| PITR window | 7 days (repo1) |
| Vault Object Lock | Compliance mode, 14 days; noncurrent versions expire one day after the lock lapses |

All values are configuration and may be shortened in the drill environment.

## Cost

The price of a tier is what runs all the time for disaster recovery alone.
Both tiers share the vault and the backups, so only site b differs:

| Tier | Always-on DR nodes | Estimated monthly cost |
|---|---|---|
| Pilot light | 0 (site b is built during the disaster) | $0 |
| Warm standby | 3 (app, database, object store) | about $72 |

The estimate assumes a small general-purpose VM (2 vCPU, 4 GB) at about
$24 per month, a common list price at large clouds; storage and traffic are
left out because both tiers pay them. The numbers live in `bia.yaml`
(`tiers.*.cost`) and `disavery report tiers` puts them next to the measured
RPO and RTO of every site-loss drill (S1).
