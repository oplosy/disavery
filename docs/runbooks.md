# Runbook reference

Every drill scenario is one YAML file in [`runbooks/`](../runbooks), named after
its `id`. `disavery runbook lint` validates them (CI runs it on every pull
request); unknown keys are errors, so typos fail loudly.

```yaml
id: s6-restore-test            # must equal the file name
description: Restore a random backup and verify it.
tiers: []                      # DR tiers from docs/bia.yaml; empty = restore test
vars: { repo: "2" }            # string variables for templates and conditions
phases:                        # fixed names, in this order, each at most once:
  - name: recover              # inject, detect, decide, recover, verify
    steps:
      - { id: pick, run: "build/disavery restore-point --seed {{.seed}}", capture: point }
  - name: verify
    steps:
      - { id: pitr, check: pitr, with: { host: restore, target: "{{.point.target}}" } }
cleanup:                       # always runs, even after a failure or Ctrl-C
  - { id: tidy, run: "..." }
```

## Steps

A step has an `id` (lower-case letters, digits and dashes, unique in the
runbook) and exactly one kind:

| Kind | Fields | Does |
|---|---|---|
| `run` | command | Runs the command with bash in the repository root inside the toolbox |
| `ssh` | `host`, `cmd` | Runs `cmd` on a lab node as root |
| `wait_http` | `url`, `status` (200), `interval` (1s) | Polls until the URL answers `status` |
| `sleep` | duration | Waits |
| `manual` | prompt, `auto_after` | Asks the operator; unattended runs continue after `auto_after`. The decision is recorded |
| `check` | name, `with` | Runs a built-in verifier (below) |
| `wait_alert` | alert name | Reserved; arrives with Alertmanager in milestone 3 |

Common fields:

| Field | Meaning |
|---|---|
| `when` | Condition, e.g. `tier == 'pilot-light'`; `==`, `!=`, `&&`, `\|\|` over variables and quoted strings |
| `timeout` | Per attempt, e.g. `5m` |
| `retries` | Extra attempts (0–10) |
| `on_failure` | `abort` (default) or `continue` (default in `verify`, so every check reports) |
| `capture` | `run`/`ssh` only: stores stdout as a variable; a JSON object becomes a map (`{{.point.set}}`) |

## Templates and variables

`run`, `ssh.host`, `ssh.cmd`, `wait_http.url`, `manual` and `with` values are
Go templates; a missing key is an error. Available: the runbook's `vars`,
captured values, and the built-ins `scenario`, `tier`, `env`, `run_id`,
`report_dir`, `seed` and `db_host` (production database node).

## Checks

| Check | Parameters | Verifies |
|---|---|---|
| `amcheck` | `host` | `pg_amcheck` on heap and every index of the docsvc database |
| `business-rules` | `host`; optional `as_of`, `tolerance`, `compare_host` | Business rules hold; with `as_of`, no document newer than the target and the same number of older documents as `compare_host` |
| `db-object-consistency` | `host`, `store` (`vault` or `obj-<site>`); optional `as_of`, `grace` | Every document's attachment exists; orphan objects are reported |
| `pitr` | `host`, `target`; optional `tolerance` | The restore contains every canary write acknowledged before the target and none sent after it |
| `canary` | — | Actual RPO after the incident (scenarios with `inject`) |
| `prober` | — | Actual RTO after the incident (scenarios with `inject`) |

## Results

| Result | Exit | Meaning |
|---|---|---|
| `PASS` | 0 | Recovered, all checks pass, targets met |
| `MISSED_TARGET` | 2 | Recovered and verified, but RPO, RTO or restore time exceeded its target |
| `FAILED` | 3 | A step or check failed |
| `ERROR` | 4 | Preflight failed, the run was aborted (Ctrl-C, global timeout) or the tool failed |

Runbooks with an `inject` phase only run with `--yes` (or `CI=true`), and only
when the Terraform workspace and every node's `disavery.env` label match `--env`.
