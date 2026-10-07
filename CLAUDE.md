# CLAUDE.md

Guidance for Claude Code when working in this repository.

## Project

**disavery** is a disaster-recovery lab for PostgreSQL that *proves* recovery
instead of assuming it: backups to two repositories (one immutable), PITR,
drills that measure actual RPO/RTO from the outside, pilot light vs. warm
standby, failover and failback. It is a portfolio project for
solution-architect / platform roles, so readability of code, docs and reports
matters as much as correctness.

Source of truth:

- Design spec: `docs/superpowers/specs/2026-10-07-disavery-dr-design.md`
- Current plan (M3 of 5): `docs/superpowers/plans/2026-10-07-m3-site-b-s1-s7.md`
- Previous plans (shipped): `docs/superpowers/plans/2026-10-07-m1-environment-and-app.md`,
  `docs/superpowers/plans/2026-10-07-m2-cli-core-and-s6.md`

Read the relevant spec section and plan task before changing anything. If code
and spec disagree, raise it — don't silently diverge.

## Status

M1 shipped (PR #2) and M2 shipped (PR #4); see each plan's amendments and
decisions for deviations from the spec. The M3 plan is written and was verified
on a prototype (see its "How this plan was verified"); implementation goes on
branch `feat/m3-site-b`. Update this section as milestones ship.

| Milestone | Scope |
|---|---|
| M1 | Environment (global zone, site-a, vault) + `docsvc` + pgBackRest backups to both repos |
| M2 | `disavery` CLI core (runbook, executor, canary, prober, verify, report), `docs/bia.yaml`, S6 |
| M3 | Site-b (warm + pilot light), core alerting, S1 on both tiers, S7, tier comparison |
| M4 | S2–S5 |
| M5 | Remaining alerts + Grafana, nightly/weekly CI drills, badge, ADRs, README polish |

Plans are written one milestone at a time, after the previous one ships.

## Architecture (short)

- Every "machine" is a systemd + SSH Debian container, created by Terraform
  (Docker provider) and configured by Ansible over SSH.
- Terraform, Ansible, SOPS, age, psql etc. run inside a pinned **toolbox**
  container — never on the Windows host.
- Zones: **global** (CoreDNS, Caddy edge, webhook mock, monitoring; assumed to
  survive site loss), **site-a** (production: `app-a`, `db-a`, `obj-a`),
  **site-b** (DR), **vault** (MinIO with Object Lock, compliance mode).
- Go binaries: `docsvc` (the protected app) and `disavery` (drill CLI over YAML
  runbooks in `runbooks/`).

Planned layout: `cmd/`, `internal/`, `infra/terraform/`, `infra/ansible/`,
`images/`, `scripts/`, `runbooks/`, `docs/` (incl. `adr/`), `reports/`.

## Commands (planned, from M1 plan)

```bash
make up          # images → secrets → build → infra → configure (idempotent)
make smoke       # end-to-end smoke test, must end with SMOKE PASS
make down        # remove lab containers, keep secrets
make destroy     # remove everything incl. secrets/escrow volumes
make test-short  # unit tests
make test        # unit + integration (testcontainers, needs Docker)
make lint        # golangci-lint
```

## Conventions

- **English only** for code, comments, docs, commit messages. (Chat with the
  user may be in Turkish.)
- **LF line endings everywhere** (`.gitattributes`). Host is Windows; all
  scripts run in Linux containers, CRLF breaks them.
- Go module: `github.com/oplosy/disavery`, Go 1.26. PostgreSQL 16,
  pgBackRest stanza `main`.
- Network plan, hostnames, ports and other fixed values are listed under
  "Global Constraints" in the M1 plan — use those, don't invent new ones.
- Every Terraform-managed container/network carries label `disavery.env=drill`.
- `make up` must be idempotent: second run → Terraform `No changes.`, Ansible
  `changed=0` on every host.
- Tests: table-driven unit tests; integration tests via testcontainers-go
  (skipped with `-short`); report rendering via golden files.

## Secrets and safety

- Never commit plaintext secrets. Secrets live only in
  `/secrets/local.sops.yaml` (volume `disavery-secrets`), decrypted with
  `SOPS_AGE_KEY_FILE=/secrets/age/keys.txt`. Escrow copy of the age key:
  volume `disavery-escrow`, `/escrow/age-keys.txt`.
- Destructive drill steps only run against `drill`-labelled environments.
  Don't run `make destroy`, `terraform destroy` or drill injections unless the
  user asked for it.

## Git

- Never commit on `main`. Work on feature branches (`feat/...`, `docs/...`),
  conventional commits, PR into `main` with green CI.
- Commit messages end with
  `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
