# disavery

Disaster recovery architecture for PostgreSQL that proves recovery instead of
assuming it: automated backups to two repositories (one immutable), measured
RPO/RTO drills, pilot light vs. warm standby, failover and failback.

> Status: milestone 3 of 5 — the lab, backups to two repositories, the drill CLI,
> the random restore test (S6), site loss (S1) and failback (S7) on both DR tiers
> with alerting. Design: [spec](docs/superpowers/specs/2026-10-07-disavery-dr-design.md).

## What runs

| Zone | Nodes | Purpose |
|---|---|---|
| global | `dns` (CoreDNS), `edge` (Caddy), `webhook`, `prometheus`, `alertmanager` | External DNS, edge/CDN, a fake payment provider and alerting |
| site-a | `app-a` (docsvc), `db-a` (PostgreSQL 16 + pgBackRest), `obj-a` (MinIO) | Production |
| site-b | `app-b`, `db-b`, `obj-b` | DR site: absent (pilot light) or a streaming replica with the app stopped (warm standby) |
| vault | `vault` (MinIO, Object Lock) | Immutable backup copy in a "separate account" |

Every node is a systemd container created by Terraform and configured by Ansible
([ADR 0001](docs/adr/0001-systemd-containers-as-nodes.md)).

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

```bash
make drill SCENARIO=s6-restore-test                                # random backup + random PITR target, restored and verified
make drill SCENARIO=s1-site-loss TIER=pilot-light ARGS=--yes       # kill site a, rebuild site b from the vault
make drill SCENARIO=s7-failback TIER=pilot-light                   # bring site a back without losing a DR write
make drill SCENARIO=s2-drop-table ARGS=--yes                       # drop a table, copy it back from an isolated PITR
make drill SCENARIO=s3-bad-migration ARGS=--yes                    # bad UPDATE + deleted attachments, rewind DB and objects
make drill SCENARIO=s4-ransomware ARGS=--yes                       # attack the vault with the writer's key, encrypt production
make drill SCENARIO=s5-secret-loss ARGS=--yes                      # lose site a and the age key, recover from escrow
docker compose exec toolbox build/disavery env reset --tier warm-standby   # switch the lab to the other tier
```

S2–S4 repair site a in place and S5 fails over like a pilot-light site loss
(fail back with `s7-failback --tier pilot-light`); all four run on the
pilot-light baseline and are judged against per-scenario targets in the BIA.

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

Prometheus (http://localhost:9090) raises `PostgresPrimaryDown`, which a site-loss
drill waits for to measure detection time. Drills found and fixed real problems:
an RPO that silently grew after crash restarts ([ADR 0005](docs/adr/0005-checkpoint-after-start.md)),
backups an attacker can hide with the production key although they cannot be
deleted ([ADR 0006](docs/adr/0006-vault-identities-and-delete-markers.md)), and a
wiped object store that claimed to hold every attachment because MinIO proxies
reads to its replication target ([ADR 0007](docs/adr/0007-list-replicated-stores.md)),
and a point-in-time rewind that only worked until the first failover
([ADR 0008](docs/adr/0008-point-in-time-restores-from-the-vault.md)).

## Development

```bash
make test-short  # unit tests
make test        # unit + integration tests (needs Docker)
make lint
```
