# disavery

Disaster recovery architecture for PostgreSQL that proves recovery instead of
assuming it: automated backups to two repositories (one immutable), measured
RPO/RTO drills, pilot light vs. warm standby, failover and failback.

> Status: milestone 1 of 5 — the lab environment, the protected application and
> backups. Drills arrive in milestone 2. Design: [spec](docs/superpowers/specs/2026-10-07-disavery-dr-design.md).

## What runs

| Zone | Nodes | Purpose |
|---|---|---|
| global | `dns` (CoreDNS), `edge` (Caddy), `webhook` | External DNS, edge/CDN and a fake payment provider |
| site-a | `app-a` (docsvc), `db-a` (PostgreSQL 16 + pgBackRest), `obj-a` (MinIO) | Production |
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

## Development

```bash
make test-short  # unit tests
make test        # unit + integration tests (needs Docker)
make lint
```
