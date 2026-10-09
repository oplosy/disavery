# disavery — Disaster Recovery Architecture: Design Spec

- **Date:** 2026-10-07
- **Status:** Approved in design review, pending written-spec review
- **Scope:** v1 (local environment). Cloud profile is a follow-up.

## 1. Purpose

Most backup projects stop at "a backup file exists". disavery proves that a
service can actually be **brought back**, measures how long it took and how
much data was lost, and compares that against declared targets — repeatedly
and automatically.

It is a portfolio project for solution-architect and platform roles. A reader
should be able to clone the repo, bring the environment up, run a drill, and
read a report that shows target vs. actual RPO/RTO with evidence.

### Success criteria

1. A randomly selected backup is regularly restored into an isolated
   environment and verified (not just "restore exited 0").
2. Actual data loss (RPO) and actual time to recover (RTO) are **measured from
   the outside** and compared with targets per DR tier.
3. Credentials, DNS, certificates and third-party dependencies are explicit
   steps in executable runbooks; none can be silently forgotten.
4. Failback preserves data written while running in the DR site.
5. Pilot light and warm standby are run against the same scenario, producing a
   measured RTO/RPO/cost comparison table. Cost is a proxy: always-on
   resources per tier (containers, reserved CPU/RAM, storage), plus an
   estimated monthly cloud price documented in `docs/bia.md`.

### Non-goals (v1)

- Multi-region or real cloud deployment (follow-up: `infra/terraform/envs/cloud`).
- Synchronous replication, active-active, automatic failover managers
  (Patroni etc.). Failover is runbook-driven by design.
- Logical (`pg_dump`) backup as a primary mechanism (only used inside S2's
  surgical restore).
- Data-size vs. RTO scaling study.

## 2. Key decisions

| Topic | Decision | Rationale |
|---|---|---|
| Environment | Local first, cloud later; same Terraform/Ansible code | Free, reproducible; cloud profile only swaps the Terraform provider |
| Machines | Systemd + SSH Debian containers created by Terraform (Docker provider), configured by Ansible over SSH | Fast (< 1 min rebuild), CI-friendly, Ansible roles reusable on real VMs. Kernel/disk behaviour differs from VMs — documented in an ADR |
| Tooling host | Pinned "toolbox" container (Terraform, Ansible, SOPS, age, psql) | Ansible does not run natively on Windows; identical versions for every user and CI |
| Orchestrator | Go CLI `disavery`, thin orchestrator over YAML runbooks | Readable runbooks, real ops tools do the work, small testable Go core |
| Backup tool | pgBackRest | Multi-repo, encryption, `verify`, delta restore, async archiving, backup from standby |
| Object storage | MinIO with versioning; separate vault MinIO with Object Lock (compliance mode) | Immutable copy; deletion attempts with production credentials fail |
| Secrets | SOPS + age, escrow copy of the age key stored separately | Git-friendly; secret recovery is an explicit runbook step |
| DNS / traffic | CoreDNS (short TTL) + Caddy edge proxy | Lets us compare DNS-based and proxy-based repointing |
| TLS | Local root CA created with the `step` CLI; CA key stored SOPS-encrypted | Certificate/CA recovery appears in runbooks |
| Monitoring | Prometheus, Alertmanager, Grafana, Pushgateway; pgBackRest and Postgres exporters | Backup age, WAL archive lag, RPO breach, stale restore-test alerts |
| DR tiers | Pilot light and warm standby (async streaming replica, `pg_rewind` for failback) | Measured cost/benefit comparison |
| Language | All code, docs and README in English | International portfolio readability |

## 3. Architecture

Three zones, each a separate Docker network. Every "machine" is a systemd+SSH
container.

```
 ┌─────────────── global (survives site loss) ────────────────────────┐
 │  CoreDNS · Caddy edge · Prometheus/Alertmanager/Grafana/Pushgateway│
 │  webhook-mock (fake payment provider) · local CA                   │
 └────────────────────────────────────────────────────────────────────┘
 ┌──── site-a (production) ─┐          ┌──── site-b (DR) ─────────────┐
 │ db-a: PG16 + pgBackRest  │ ─WAL───▶ │ pilot light: absent; created │
 │ app-a: docsvc (Go)       │ stream   │   by Terraform during drill  │
 │ obj-a: MinIO (versioned) │ (warm)   │ warm: db-b replica + app-b   │
 └──────────┬───────────────┘          └──────────────────────────────┘
            │ pgBackRest repo2 + WAL archive, MinIO bucket replication
 ┌──────────▼──── vault (separate "account") ─────────────────────────┐
 │ MinIO + Object Lock (compliance) · write-only production user      │
 │ escrow: copy of the age key (separate volume)                      │
 └────────────────────────────────────────────────────────────────────┘
```

**Assumption (ADR):** the global zone stands in for external DNS/CDN providers
and is assumed to survive the loss of a site.

### Components

- **`docsvc`** (Go): document service. Records in PostgreSQL, attachments in
  MinIO. Sends webhooks to and receives callbacks from `webhook-mock`. Exposes
  `/healthz`, `/readyz` and a `canary` endpoint/table used by drills.
- **`disavery` CLI** (Go): executes runbooks, runs canary writer and prober,
  performs verification, writes reports. Subcommands: `drill run`,
  `drill list`, `env reset`, `verify`, `report`, `report trend`,
  `runbook lint`.
- **Toolbox container:** pinned infra tool versions; `run` steps execute here.
- **Terraform** (`infra/terraform`): `modules/node` (systemd/SSH container),
  `envs/local` (zones, networks, nodes per site and tier).
- **Ansible** (`infra/ansible/roles`): `base`, `postgres`, `pgbackrest`,
  `minio`, `docsvc`, `coredns`, `caddy`, `monitoring`; playbooks for site
  build and traffic repointing.

### Repository layout

```
cmd/disavery/          cmd/docsvc/
internal/              runbook · executor · canary · prober · verify · report
infra/terraform/       modules/node · envs/local   (envs/cloud later)
infra/ansible/         roles/ · playbooks/
runbooks/              one YAML per scenario
docs/                  bia.md · bia.yaml · architecture.md · adr/
reports/               drill outputs (sample reports committed)
```

## 4. Business impact, targets and retention

`docsvc` is classified **Tier-1**. Targets live in `docs/bia.yaml` (read by the
CLI) and are explained in `docs/bia.md`.

| DR tier | Target RPO | Target RTO | Mechanism |
|---|---|---|---|
| Pilot light | ≤ 60 s | ≤ 15 min | pgBackRest async WAL archive (`archive_timeout=30s`); site-b built from scratch |
| Warm standby | ≤ 5 s | ≤ 2 min | Async streaming replica; site-b always running |

Scenarios S2–S5 do not depend on the tier (they are repaired in the production
site or follow the pilot-light path) and run on the pilot-light baseline; their
targets are declared per scenario in `docs/bia.yaml` (`scenarios`).

Retention: daily full, hourly incremental, 7-day PITR window, 14-day Object
Lock retention in the vault. All values are configuration and may be shortened
in the drill environment.

RTO is reported in four phases: **detect** (alert fires) → **decide** (disaster
declared; fixed configurable delay in drills) → **recover** → **verify**.

## 5. Scenario catalog (v1)

| # | Scenario | Injection | Recovery path | Proves |
|---|---|---|---|---|
| S1 | Site loss (both tiers) | Kill all site-a machines | Pilot: Terraform + Ansible + restore. Warm: promote replica. Then DNS/proxy repoint | Tier comparison |
| S2 | `DROP TABLE` | Drop a table while traffic continues | Surgical: PITR into isolated env → copy only that table back to production | Recovery without losing unrelated writes |
| S3 | Bad migration | `UPDATE` without `WHERE` + some attachments deleted | Full PITR rewind + MinIO version-based restore + consistency check | DB ↔ object-store consistency |
| S4 | Ransomware / backup attack | Attempt to delete vault backups with production credentials; corrupt production data | Deletion is blocked by Object Lock (recorded as evidence); restore from clean backup | Immutability, privilege separation |
| S5 | Secret loss | Age key and secrets lost together with the site | Retrieve key from escrow → decrypt with SOPS → recover | Key escrow works |
| S6 | Random restore test (nightly) | None | Random backup set + random PITR point → isolated env → verify | Backups are restorable |
| S7 | Failback (follows S1) | None | Warm: `pg_rewind` site-a as replica → controlled switchover. Pilot: backup from site-b → restore to site-a → switchover | Data written in DR is preserved |

Every scenario's verify phase includes: canary RPO, prober RTO, `pg_amcheck`,
row-count/business-rule checks, DB ↔ MinIO orphan/missing-object check,
`docsvc` smoke test, webhook reaches the new endpoint.

## 6. Measurement

All timestamps come from a single monotonic clock on the host running the
CLI, so cross-machine clock skew cannot affect results.

### Canary writer → actual RPO

- Every 1 s, `POST /canary {seq}` via the public DNS name through Caddy, i.e.
  it measures writes the application acknowledged, not direct DB writes.
- Each acknowledged `seq` and its timestamp are appended to a local journal.
- After recovery, surviving `seq` values are queried.
- **Actual RPO** = incident time − timestamp of the last surviving
  acknowledged write. Also reported: number of lost acknowledged writes, and an
  ordering anomaly warning if an older write is lost while a newer one
  survived. A point-in-time rewind (S3) also discards writes acknowledged after
  the incident; actual RPO then runs to the newest lost write.

### Prober → actual RTO

- Every 500 ms: `GET /readyz` plus a document read/write round trip.
- Resolves DNS through a TTL-honouring caching resolver so client-side DNS
  caching delay is part of the measurement.
- **Actual RTO** = first moment after the incident followed by 5 consecutive
  successes − incident time.

## 7. Runbooks

One YAML file per scenario in `runbooks/`. Example:

```yaml
id: s1-site-loss
tiers: [pilot-light, warm-standby]       # targets come from bia.yaml per tier
vars: { decision_delay: 30s }
phases:
  - name: inject
    steps:
      - { id: kill-site-a, run: "docker kill app-a db-a obj-a" }   # machines crash, disks survive (pg_rewind failback)
  - name: detect
    steps:
      - { id: alert, wait_alert: PostgresPrimaryDown, timeout: 2m }
  - name: decide
    steps:
      - { id: declare, manual: "Declare disaster?", auto_after: "{{.decision_delay}}" }
  - name: recover
    steps:
      - { id: provision-b, when: "tier == 'pilot-light'", run: "...", timeout: 5m, retries: 1 }
      - { id: promote-b,   when: "tier == 'warm-standby'", ssh: { host: db-b, cmd: "pg_ctl promote ..." } }
      - { id: repoint,     run: "ansible-playbook playbooks/repoint-traffic.yml -e site=b" }
  - name: verify
    steps:
      - { id: rpo,   check: canary }
      - { id: rto,   check: prober }
      - { id: integ, check: amcheck }
      - { id: objs,  check: db-object-consistency }
cleanup:            # always runs
  - { run: "..." }
```

**Step types:** `run` (command in toolbox), `ssh` (command on a node),
`wait_alert` (Alertmanager), `wait_http`, `sleep`, `manual` (human
confirmation; in CI auto-continues after `auto_after` and is recorded as a
decision point), `check` (built-in Go verifier).

**Common fields:** `id`, `when`, `timeout`, `retries`,
`on_failure: abort | continue`. Templating uses Go `text/template`. Phase names
are fixed (`inject`, `detect`, `decide`, `recover`, `verify`) so RTO can be
broken down; scenarios without injection (S6, S7) omit `inject`/`detect`, and
scenarios whose incident trips no alert (S2, S3) omit `detect`.

## 8. Evidence and reports

Output directory: `reports/<timestamp>-<scenario>-<tier>/`

- `steps/<id>.log` — stdout/stderr plus start/end timestamps per step.
- `report.json` — schema-versioned machine-readable result.
- `report.md` — target vs. actual table, RTO phase breakdown, step timeline
  (Mermaid gantt), verification results.
- `reports/history.jsonl` — one line per drill. `disavery report trend` builds
  trend tables and the tier comparison table from it.
- Results are pushed to Prometheus Pushgateway — the last run and the last
  success per scenario and tier, so a failed run cannot hide when the scenario
  last passed; a failed push never changes the verdict
  ([ADR 0009](../../adr/0009-drill-evidence-pushgateway-and-history-branch.md)).
  Grafana shows trends and the **"last successful restore test older than
  48 h"** alert fires from it.

### Result classes and exit codes

| Result | Exit | Meaning |
|---|---|---|
| `PASS` | 0 | Recovered, all checks pass, targets met |
| `MISSED_TARGET` | 2 | Recovered and verified, but RPO or RTO target exceeded |
| `FAILED` | 3 | Recovery or verification failed |
| `ERROR` | 4 | Tool/infrastructure error, preflight failure or abort; drill invalid |

## 9. Error handling and safety

- **Preflight** (before any injection): site-a healthy, latest backup younger
  than threshold, WAL archive lag low, canary writing, no firing alerts, vault
  reachable, escrow present. Any failure → `ERROR`, nothing is broken.
- **Step failure:** retries exhausted → apply `on_failure`; `abort` runs
  `cleanup` and writes a `FAILED` report with partial evidence.
- **Ctrl-C / global timeout:** `cleanup` still runs; report written as
  `ERROR` with an "aborted" note.
- **Safety lock:** injection only runs against environments labelled `drill`;
  `--env` must match the Terraform workspace; destructive steps require
  `--yes` or a CI context.
- **`disavery env reset`:** idempotently returns the environment to baseline
  (site-a primary, site-b in the state required by the tier).

## 10. Monitoring and alerting

Prometheus scrapes PostgreSQL (`postgres_exporter`), pgBackRest
(`pgbackrest_exporter`) and the Pushgateway.
Alerts (Alertmanager):

- `PostgresPrimaryDown`
- `BackupTooOld` (last successful full/incremental older than policy)
- `WalArchiveLagHigh` / `RpoBreachRisk` (archive lag approaching target RPO)
- `ReplicationLagHigh` (warm standby)
- `RestoreTestStale` (last successful S6 older than 48 h, via Pushgateway)

Thresholds reuse the BIA's preflight limits (`docs/bia.md`, Alerts). Alerts
about the drill programme rather than the lab (`RestoreTestStale`) carry
`blocks_drills="false"` and do not stop preflight.

Grafana dashboards: backup health, replication, drill history.

## 11. Testing strategy

| Layer | What | How |
|---|---|---|
| Unit | Runbook parsing and schema validation | `go test`, table-driven |
| Unit | Executor: ordering, `when`, retries, timeouts, cleanup always runs | Fake step runners |
| Unit | RPO/RTO math and anomaly detection | Synthetic canary/prober journals |
| Unit | Report rendering | Golden files |
| Integration | `docsvc`, DB ↔ object consistency checker | testcontainers-go (Postgres, MinIO) |
| Static | Code and config quality | `golangci-lint`, `terraform fmt/validate`, `ansible-lint`, `disavery runbook lint` |
| End-to-end | The drills themselves | S1–S7 against the real environment |

## 12. CI (GitHub Actions)

- **Pull requests:** lint, unit, integration, runbook lint, Terraform
  validate. Target ~5 min, no environment bring-up.
- **Nightly cron:** bring up the local environment on an Ubuntu runner with a
  small seed dataset (~500 MB); run S6 plus one rotating scenario from S1–S5
  (S1 rotates through both tiers).
- **Weekly:** full catalog.
- **Outputs:** reports uploaded as artifacts; `history.jsonl` and badge JSON
  committed by a bot to a dedicated `drill-history` branch (keeps `main`
  clean); README shows a "last restore drill: PASS · 2026-10-09 02:31 UTC"
  badge (the time the newest S6 finished; a static badge cannot age).

## 13. Build order

1. Environment and app: site-a, vault, global zone via Terraform/Ansible;
   `docsvc`; pgBackRest backups to both repos.
2. CLI core (runbook, executor, canary, prober, verify, report) and S6.
3. S1 on both tiers and S7. Produces the tier comparison table.
4. S2–S5.
5. Monitoring and alerts, CI cron, badge, README, ADRs.

## 14. Follow-ups (post-v1)

- Cloud profile (`envs/cloud`, Hetzner or AWS) reusing Ansible roles.
- Fencing / split-brain prevention and DNS TTL experiments in depth.
- RTO vs. data size study (1 / 10 / 50 GB).
- Logical backups for cross-version and partial restores.
- MinIO, `docsvc` and node metrics in Prometheus (v1 scrapes what its alerts
  and dashboards read).
