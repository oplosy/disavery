# disavery

[![last restore drill](https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/oplosy/disavery/drill-history/badge.json)](https://github.com/oplosy/disavery/tree/drill-history)
[![ci](https://github.com/oplosy/disavery/actions/workflows/ci.yml/badge.svg)](https://github.com/oplosy/disavery/actions/workflows/ci.yml)
[![drills](https://github.com/oplosy/disavery/actions/workflows/drills.yml/badge.svg)](https://github.com/oplosy/disavery/actions/workflows/drills.yml)

Disaster recovery architecture for PostgreSQL that proves recovery instead of
assuming it: automated backups to two repositories (one immutable), measured
RPO/RTO drills, pilot light vs. warm standby, failover and failback — run
every night in CI.

> Status: v1 complete. Every scenario of the
> [design spec](docs/superpowers/specs/2026-10-07-disavery-dr-design.md) runs as a
> drill on both DR tiers where it applies, with alerting, dashboards and nightly
> drills in GitHub Actions. Next: the follow-ups in spec §14 (cloud profile, RTO
> vs. data size).

## What runs

| Zone | Nodes | Purpose |
|---|---|---|
| global | `dns` (CoreDNS), `edge` (Caddy), `webhook`, `prometheus`, `alertmanager`, `pushgateway`, `grafana` | External DNS, edge/CDN, a fake payment provider, alerting and drill evidence |
| site-a | `app-a` (docsvc), `db-a` (PostgreSQL 16 + pgBackRest), `obj-a` (MinIO) | Production |
| site-b | `app-b`, `db-b`, `obj-b` | DR site: absent (pilot light) or a streaming replica with the app stopped (warm standby) |
| vault | `vault` (MinIO, Object Lock) | Immutable backup copy in a "separate account" |

Every node is a systemd container created by Terraform and configured by Ansible
([ADR 0001](docs/adr/0001-systemd-containers-as-nodes.md)); the global zone is
assumed to survive a site loss ([ADR 0002](docs/adr/0002-global-zone-survives-site-loss.md)).

## Quick start

Requirements: Docker (Docker Desktop with WSL2 on Windows), GNU make, Go 1.26 for local tests.

```bash
make up      # build images, generate secrets, create and configure the lab
make smoke   # end-to-end check: TLS, upload, webhook, WAL archive, backups, immutability
make down    # remove the lab containers (keeps secrets)
make destroy # remove everything, including secrets and the escrow volume
```

`make up` is idempotent; running it again changes nothing. It takes about
3 minutes with images cached; the first run also builds MinIO from source,
since MinIO no longer publishes community builds
([ADR 0003](docs/adr/0003-minio-built-from-source.md)).

Secrets are generated once into the `disavery-secrets` volume, encrypted with
SOPS/age; the age key is escrowed to the separate `disavery-escrow` volume.

## Drills

| Scenario | What happens | Tiers |
|---|---|---|
| S1 site loss | site a crashes; site b takes over (rebuilt from the vault, or a promoted replica) | pilot light, warm standby |
| S2 dropped table | `DROP TABLE documents`; the table is copied back from an isolated point-in-time restore | — |
| S3 bad migration | a wrong `UPDATE` and deleted attachments; database and object store rewound together | — |
| S4 ransomware | the vault is attacked with the production writer's key, production encrypted; site a rebuilt from the vault | — |
| S5 secret loss | site a and the age key are lost; the key comes back from escrow before the failover | — |
| S6 restore test | a random backup restored to a random point in time and verified | — |
| S7 failback | site a comes back without losing a write made in the DR site | pilot light, warm standby |

```bash
make drill SCENARIO=s6-restore-test
make drill SCENARIO=s1-site-loss TIER=pilot-light ARGS=--yes
make drill SCENARIO=s7-failback TIER=pilot-light
make drill SCENARIO=s2-drop-table ARGS=--yes      # likewise s3-bad-migration, s4-ransomware, s5-secret-loss
docker compose exec toolbox build/disavery env reset --tier warm-standby   # switch the lab to the other tier
```

A drill checks the lab is healthy, runs its [runbook](docs/runbooks.md) while a
prober watches production, and writes `reports/<run>/report.md` (target vs.
actual, phase timings, verification, timeline) plus `report.json` and per-step
logs. A canary service journals one acknowledged write per second, so data loss
and point-in-time accuracy are measured, not assumed. Targets and thresholds are
in the [BIA](docs/bia.md). Examples: [a passing restore test](reports/samples/s6-restore-test/report.md),
[site loss on warm standby](reports/samples/s1-site-loss-warm-standby/report.md),
[failback from pilot light](reports/samples/s7-failback-pilot-light/report.md),
[ransomware against the vault](reports/samples/s4-ransomware/report.md) and
[the tier comparison](reports/samples/tier-comparison.md).

```bash
docker compose exec toolbox build/disavery report trend   # history of all drills
docker compose exec toolbox build/disavery report tiers   # pilot light vs. warm standby: measured RPO/RTO and cost
```

## Monitoring

Grafana (http://localhost:3000, read-only without login) shows backup health,
replication and the drill history; Prometheus is on http://localhost:9090.
Every drill pushes its result to a Pushgateway
([ADR 0009](docs/adr/0009-drill-evidence-pushgateway-and-history-branch.md)).

| Alert | Fires when |
|---|---|
| `PostgresPrimaryDown` | the primary does not answer (site-loss drills wait for it to measure detection) |
| `BackupTooOld` | a repository has no new backup for 2 h |
| `WalArchiveLagHigh` / `RpoBreachRisk` | the newest archived WAL is older than 45 s / 50 s (pilot light's RPO target is 60 s) |
| `ReplicationLagHigh` | the warm standby replays more than 5 s behind |
| `RestoreTestStale` | no restore test has passed for 48 h |

The thresholds are the drills' own preflight limits ([BIA](docs/bia.md#alerts)),
so the lab warns about exactly what would stop a drill.

## Continuous drills

The [`drills`](.github/workflows/drills.yml) workflow brings the lab up on a
GitHub runner with a ~500 MB database, then runs the random restore test plus
one rotating scenario every night and the whole catalog every Sunday. Reports
are kept as run artifacts; the history, the badge above and trend tables go to
the [`drill-history`](https://github.com/oplosy/disavery/tree/drill-history)
branch, so `main` stays free of bot commits.

## What the drills found

Each of these was found by a drill failing, and fixed:

- an RPO that silently grew after crash restarts ([ADR 0005](docs/adr/0005-checkpoint-after-start.md));
- backups an attacker can hide with the production key, although not delete ([ADR 0006](docs/adr/0006-vault-identities-and-delete-markers.md));
- a wiped object store that claimed to hold every attachment, because MinIO proxies reads to its replication target ([ADR 0007](docs/adr/0007-list-replicated-stores.md));
- a point-in-time rewind that only worked until the first failover ([ADR 0008](docs/adr/0008-point-in-time-restores-from-the-vault.md));
- a vault that gained one more version of every attachment each time a site store was refilled from it ([ADR 0010](docs/adr/0010-copy-attachments-back-as-replicas.md)).

All decisions: [docs/adr](docs/adr/).

## Development

```bash
make test-short   # unit tests
make test         # unit + integration tests (needs Docker)
make test-alerts  # Prometheus alert rules (promtool)
make lint
```
