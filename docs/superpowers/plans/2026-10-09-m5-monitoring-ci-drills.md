# M5 — Monitoring, Alerts, Grafana and Continuous Drills Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The lab watches its own recoverability and proves it every night: Prometheus raises `BackupTooOld`, `WalArchiveLagHigh`, `RpoBreachRisk` and `RestoreTestStale` next to plan 3's alerts; every drill result reaches a Pushgateway and three provisioned Grafana dashboards (backup health, replication, drill history); a GitHub Actions workflow brings the lab up on a runner with a ~500 MB seed, runs S6 plus one rotating scenario every night and the whole catalog every Sunday, and publishes the history, trend tables and a README badge to the `drill-history` branch (spec §8, §10, §12). The README, ADRs and spec are brought up to date for v1.

**Architecture:** No new moving parts inside the drill engine: `disavery drill run` pushes its report to a Pushgateway in two groups per scenario and tier (last run, last success) and never lets a failed push change the verdict. A pinned `pgbackrest_exporter` runs next to `postgres_exporter` on every database node; Prometheus finds both through the file-based targets Ansible already writes from the topology, so a failover moves the `role` label with the primary. The Pushgateway (persisted volume) and Grafana (anonymous, read-only, dashboards from files) are Terraform containers in the global zone. CI runs the lab exactly as a developer does (`make up`, `disavery seed`, `make smoke`), then `scripts/drill-programme.sh` and `scripts/publish-history.sh`, which appends `history.jsonl`, writes `badge.json` (a shields.io endpoint) and a trend README to an orphan branch — `main` gets no bot commits.

**Tech Stack:** Go 1.26 (no new dependencies), Prometheus 3.15 (`promtool test rules`), Alertmanager, Pushgateway 1.11, Grafana 13.2, pgbackrest_exporter 0.24 (pinned sha256), GitHub Actions (`ubuntu-24.04`), shellcheck, shields.io endpoint badges.

**Spec:** `docs/superpowers/specs/2026-10-07-disavery-dr-design.md` (§8 evidence, §10 monitoring, §12 CI, §13 step 5).

## Roadmap (this is plan 5 of 5)

| Plan | Milestone | Status |
|---|---|---|
| 1 | Environment + docsvc + backups to both repos | **shipped** (PR #2) |
| 2 | `disavery` CLI core, `docs/bia.yaml`, S6 | **shipped** (PR #4) |
| 3 | Site-b (warm + pilot light), core alerts, S1 on both tiers, S7, tier comparison | **shipped** (PR #6) |
| 4 | S2–S5: dropped table, bad migration, ransomware, secret loss | **shipped** (PR #8; follow-up PR #9, ADR 0010) |
| **5** | **Remaining alerts, Pushgateway, Grafana, nightly/weekly CI drills, `drill-history` branch + badge, ADRs, README (this plan)** | **written** |

## How this plan was verified

Every file in this plan was written and run on the prototype branch `wip/m5-prototype` before the plan was written (2026-10-08/09): `go vet`, `go test -race ./...`, `golangci-lint` (0 issues), `promtool test rules` (4 test groups), `shellcheck scripts/*.sh`, `actionlint`, `ansible-lint`, `terraform fmt`, `make up` twice (second run: Terraform `No changes.`, Ansible `changed=0` on every host; app-a and the webhook mock restart once after a new commit, because the binaries carry it), `make smoke` (`SMOKE PASS`), then on the lab:

| Check | Result |
|---|---|
| Scrape targets | `postgres` and `pgbackrest` on db-a, and `pushgateway`, all `up` |
| `RestoreTestStale` | fires on a fresh lab (no S6 yet) without blocking preflight; resolves within a minute of a passing S6 |
| S6 with push | `PASS`; both Pushgateway groups present; dashboard row "s6-restore-test / none / PASS" |
| S6 pushing to a closed port | `PASS`, exit 0, one `warning: result not pushed` line |
| `disavery seed --size 500MB` | ballast grows to 500 MB, full backup to both repositories |
| Grafana | three dashboards provisioned, anonymous viewer, home = drill history; tables show only `result`, `scenario`, `tier` |
| `drills` workflow on GitHub (`wip/m5-prototype`, history to a temporary branch) | nightly: green in 13 min (lab up 6 min, seed, smoke, S6 + S1 pilot light + S7); weekly: green in 38 min, below. The temporary branch received `history.jsonl`, `badge.json` (`PASS · 2026-10-09 08:31 UTC`) and the trend README |

The weekly programme on a GitHub runner (`ubuntu-24.04`, 500 MB seed), every drill `PASS`:

| Drill | Measured |
|---|---|
| S6 restore test | exact PITR |
| S2 dropped table | RPO 0.5 s (target 5 s), no write lost; RTO 1 m 19 s |
| S3 bad migration | RPO 33 s (target 60 s); RTO 1 m 31 s |
| S4 ransomware | RPO 21 s (target 60 s); RTO 1 m 35 s (target 30 m) |
| S5 secret loss, then S7 | RPO 3.5 s (target 60 s); RTO 2 m 23 s (target 20 m); failback outage 5.5 s |
| S1 pilot light, then S7 | RPO 18 s (target 60 s); RTO 2 m 20 s (target 15 m); failback outage 5.5 s |
| S1 warm standby, then S7 | RPO 0.3 s (target 5 s), no write lost; RTO 46 s (target 2 m); failback outage 5.5 s |

Eight drills started right after another recovery were refused by preflight (canary silence still inside the last minute, `PostgresPrimaryDown` still resolving) and passed on the second to fourth attempt, as `drill-programme.sh` intends.

The prototype found these problems, all fixed in this plan:

1. **A fresh lab could never run its first restore test.** `RestoreTestStale` fires when no S6 has ever passed, and preflight's `no-firing-alerts` refused every drill while any alert fired. Alerts about the drill programme now carry `blocks_drills="false"` and preflight ignores them (ADR 0009).
2. **Grafana tables showed Prometheus internals.** An instant query rendered as a table carries `Time`, `__name__`, `job`, `instance` and `Value`; the drill tables now hide them.
3. **CI could not build the binaries.** On a runner the checkout belongs to uid 1001 while the toolbox runs as root, so git refused the repository ("dubious ownership") and `go build` failed to stamp its VCS information. The toolbox image now trusts `/work` (`safe.directory`).
4. **CI nodes found no packages.** The node image empties `/var/lib/apt/lists`, and Ansible judges the package cache's age by that directory's mtime: on a runner, where the image is built minutes before, the empty cache looked fresh, was never updated, and `apt` found no `jq`. Locally the image is older than `cache_valid_time` and the bug stayed hidden. The base role now updates the cache whenever no package lists exist.
5. **The programme did not wait for a recovery to settle.** S7 started right after S1 and its preflight failed (the canary's silence during the outage was still inside the last minute, `PostgresPrimaryDown` not yet resolved). The script retries exactly this case, but it looked for "preflight failed" in the CLI's output and the CLI only wrote it to the report; the run ended as `ERROR`. `drill run` now prints the note.
6. **Retries that never waited (S7 on CI).** S7 starts site a's machines and fences the old primary over SSH with five retries — but the executor retried at once, so all six attempts failed within 50 ms ("Connection refused") while db-a was still booting. Locally the containers came up fast enough to hide it. Drills now pause 3 s before each retry (`executor.Options.RetryDelay`).

## Decisions and spec clarifications

Decided with the user while planning:

1. **`BackupTooOld` reads pgbackrest_exporter**, a pinned binary (version + sha256 from the release's checksum file) run as a systemd service on every database node, like `postgres_exporter` in plan 3.
2. **The nightly CI drill seeds about 500 MB** (spec §12), so restores move a realistic amount of data.
3. **Prototype first**, on `wip/m5-prototype`, pushed to the remote so the drill workflow ran on GitHub; its history went to a temporary branch that was deleted afterwards.

Clarifications of the spec — raise if you disagree:

4. **Two Pushgateway groups per scenario and tier** (`disavery_drill`: last run, result, duration, measured values and targets; `disavery_drill_success`: when it last passed). A failed run replaces only the first, so it cannot hide when the scenario last passed. Pushing is best effort: a failure is a warning, never a different verdict. `tier` is `none` for tierless runbooks (the Pushgateway rejects empty grouping labels).
5. **`RestoreTestStale` does not block drills** (`blocks_drills="false"`): it is about the drill programme, not the lab's health. Every other alert still stops preflight.
6. **Alert thresholds reuse the BIA's preflight limits.** `BackupTooOld`: newest backup older than `max_backup_age` (2 h) for 5 minutes, per repository, on the primary only (a standby's empty repo1 is not a finding). `WalArchiveLagHigh`: last archived segment older than `max_archive_age` (45 s). `RpoBreachRisk`: older than 50 s — a site loss now would lose more than pilot light's 60 s RPO target. `ReplicationLagHigh` stays from plan 3.
7. **Prometheus scrapes what the alerts and dashboards read:** PostgreSQL, pgBackRest and the Pushgateway. MinIO, `docsvc` and node metrics (spec §10) answer no question v1 asks; they move to spec §14 (follow-ups).
8. **The badge shows a date, not an age.** Spec §12's "last restore drill: PASS · 6h ago" cannot be a static file; `badge.json` says `PASS · 2026-10-09 02:31 UTC` (when the newest S6 finished). A failed or skipped night leaves the previous date, which makes staleness visible.
9. **The nightly rotation** runs S6 and then one of `s1-pilot-light`, `s1-warm-standby`, `s2`, `s3`, `s4`, `s5`, chosen by day of the year. Site losses (S1, S5) are followed by their failback (S7); the warm-standby slot switches the tier and back. Weekly (Sunday) runs S6 and all six. A drill whose preflight fails changed nothing and is retried for up to five minutes (the previous recovery may still be settling); the job exits with the worst result.
10. **Seed ballast is a separate table** (`ballast`, random incompressible bytes from `pgcrypto`, so neither TOAST nor pgBackRest compression shrinks it), followed by a full backup to both repositories. `docsvc` never reads it, so verification is unchanged; S6's restores and S1's rebuilds carry it.
11. **Grafana is anonymous and read-only** (viewer role, no login form, provisioned dashboards cannot be edited), published on host port 3000; the home dashboard is the drill history.
12. **CI artifacts:** the run's `reports/` (30 days). The `drill-history` branch is the only durable record of CI drills; locally, Grafana and `reports/history.jsonl` are.
13. **Out of scope:** pruning the history branch (a few lines per night), notifications from Alertmanager to a real channel (the webhook mock stays the receiver), MinIO/node exporters (item 7).

## Global Constraints

Plans 1–4 still hold. Additions:

- Global zone: `pushgateway` 172.31.0.15:9091 (volume `disavery-pushgateway`), `grafana` 172.31.0.16:3000 (host port `grafana_host_port`, default 3000). Images `prom/pushgateway:v1.11.3`, `grafana/grafana:13.2.2`; `promtool` from `prom/prometheus:v3.15.0`.
- `pgbackrest_exporter` 0.24.0 on every database node, port 9854, `--collect.interval=60`, user `postgres`; Prometheus job `pgbackrest` from `/monitoring/pgbackrest.json` (same labels as `postgres.json`: `instance`, `site`, `role`).
- Metrics pushed by `disavery drill run` (grouping `job`, `scenario`, `tier`): `disavery_drill_last_run_timestamp_seconds`, `disavery_drill_result{result}`, `disavery_drill_duration_seconds`, `disavery_drill_measurement_seconds{measure,kind="actual"|"target"}`, `disavery_drill_last_success_timestamp_seconds`.
- Alert label `blocks_drills="false"` exempts an alert from preflight.
- New CLI: `drill run --pushgateway URL` (default `http://pushgateway:9091`, empty disables), `report badge [--history F] [--scenario S] [--label L]`, `seed [--size 500MB]`. New make target: `test-alerts`. New scripts: `scripts/drill-programme.sh nightly|weekly`, `scripts/publish-history.sh [branch]`.
- Branch `drill-history` (orphan): `history.jsonl`, `badge.json`, `README.md`; written only by the `drills` workflow.

## Review Focus

1. **A drill's verdict never depends on monitoring.** A push failure is a warning and the exit code is the drill's. Pinned in Task 1 (`TestPush`, error path) and Task 7 (a drill pushing to a closed port still passes).
2. **A failed run cannot hide the last success.** Only a pass writes `disavery_drill_success`. Pinned in Task 1 (`TestPush`).
3. **Preflight still refuses a sick lab.** Only alerts labelled `blocks_drills="false"` are ignored, and only `RestoreTestStale` carries the label. Pinned in Task 1 (`TestWaitAlert`) and Task 3 (`alerts_test.yml`).
4. **Alerts are tested, not eyeballed.** Every new rule has a firing and a non-firing case in `promtool test rules`, run in CI. Pinned in Task 3.
5. **Pinned downloads.** The exporter is checked against its sha256; images carry exact tags. Pinned in Task 3.
6. **`main` stays free of bot commits** and the workflow only writes the history branch (`contents: write` is the only permission). Pinned in Task 5.
7. **`make up` stays idempotent** with the new role, containers and uploads. Pinned in Task 7.
8. **No secret leaves the lab.** Grafana has no credentials to protect (anonymous viewer over Prometheus), the Pushgateway receives only numbers and scenario names, and the history branch only report summaries. Pinned in Task 7 (secret scan over the published files).

---

### Task 1: Drill results in the Pushgateway

**Files:**
- Create: `internal/report/push.go`, `internal/report/push_test.go`
- Modify: `internal/lab/env.go`, `internal/drill/runners.go`, `internal/drill/labenv.go`, `cmd/disavery/main.go`
- Test: `internal/drill/runners_test.go`

**Interfaces:**
- Consumes: plan 2's `report.Report` (`Scenario`, `Tier`, `Result`, `FinishedAt`, `Duration()`, `Measurements`).
- Produces: `(*report.Report).RunMetrics() string`, `(*report.Report).SuccessMetrics() string`, `report.Push(ctx, client, base, r) error`; `lab.Env.Pushgateway`; `drill run --pushgateway`; `activeAlerts(ctx, client, base, keep)` with `blocksDrills`.

- [ ] **Step 1: Create the branch**

Prerequisite: this plan is merged into `main`.

```bash
git switch main && git pull && git switch -c feat/m5-monitoring-ci-drills
```

- [ ] **Step 2: Write the failing tests**

`internal/report/push_test.go` (uses `at` from `report_test.go`):
```go
package report_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/oplosy/disavery/internal/report"
)

func TestRunMetrics(t *testing.T) {
	r := &report.Report{Scenario: "s1-site-loss", Tier: "warm-standby", Result: report.MissedTarget,
		StartedAt: at(0), FinishedAt: at(90.5),
		Measurements: []report.Measurement{{Name: "rpo", TargetSeconds: 5, ActualSeconds: 0.25}}}
	got := r.RunMetrics()
	for _, want := range []string{
		"disavery_drill_last_run_timestamp_seconds 1791374490.5\n",
		"disavery_drill_result{result=\"MISSED_TARGET\"} 1\n",
		"disavery_drill_result{result=\"PASS\"} 0\n",
		"disavery_drill_duration_seconds 90.5\n",
		"disavery_drill_measurement_seconds{measure=\"rpo\",kind=\"actual\"} 0.25\n",
		"disavery_drill_measurement_seconds{measure=\"rpo\",kind=\"target\"} 5\n",
		"# TYPE disavery_drill_result gauge\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in\n%s", want, got)
		}
	}
}

// TestPush: a pass replaces both groups; a failure only the last run, so the
// last success survives it.
func TestPush(t *testing.T) {
	var puts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Method != http.MethodPut || len(body) == 0 {
			t.Errorf("%s %s with %d bytes", r.Method, r.URL.Path, len(body))
		}
		puts = append(puts, r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	pass := &report.Report{Scenario: "s6-restore-test", Result: report.Pass, StartedAt: at(0), FinishedAt: at(60)}
	if err := report.Push(context.Background(), srv.Client(), srv.URL, pass); err != nil {
		t.Fatal(err)
	}
	failed := &report.Report{Scenario: "s1-site-loss", Tier: "pilot-light", Result: report.Failed, StartedAt: at(0), FinishedAt: at(60)}
	if err := report.Push(context.Background(), srv.Client(), srv.URL+"/", failed); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"/metrics/job/disavery_drill/scenario/s6-restore-test/tier/none",
		"/metrics/job/disavery_drill_success/scenario/s6-restore-test/tier/none",
		"/metrics/job/disavery_drill/scenario/s1-site-loss/tier/pilot-light",
	}
	if strings.Join(puts, "|") != strings.Join(want, "|") {
		t.Fatalf("puts %v", puts)
	}

	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "bad metric", http.StatusBadRequest)
	}))
	defer down.Close()
	if err := report.Push(context.Background(), down.Client(), down.URL, pass); err == nil || !strings.Contains(err.Error(), "status 400: bad metric") {
		t.Fatalf("want the gateway's error, got %v", err)
	}
}
```

In `internal/drill/runners_test.go`, `TestWaitAlert`'s server also returns an alert about the drill programme, and the test checks both filters. The handler's second response and the end of the test become:

```go
		_, _ = w.Write([]byte(`[{"labels":{"alertname":"ReplicationLagHigh"}},{"labels":{"alertname":"PostgresPrimaryDown"}},` +
			`{"labels":{"alertname":"RestoreTestStale","blocks_drills":"false"}}]`))
```

```go
	names, err := activeAlerts(context.Background(), srv.Client(), srv.URL, nil)
	if err != nil || strings.Join(names, ",") != "PostgresPrimaryDown,ReplicationLagHigh,RestoreTestStale" {
		t.Fatalf("names %v %v", names, err)
	}
	// Preflight ignores alerts about the drill programme itself.
	names, err = activeAlerts(context.Background(), srv.Client(), srv.URL, blocksDrills)
	if err != nil || strings.Join(names, ",") != "PostgresPrimaryDown,ReplicationLagHigh" {
		t.Fatalf("blocking names %v %v", names, err)
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

```bash
go test ./internal/report/ ./internal/drill/
```
Expected: compile errors (`report.Push`, `RunMetrics`, `blocksDrills` undefined; `activeAlerts` has too many arguments).

- [ ] **Step 4: Implement**

`internal/report/push.go`:
```go
package report

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Pushgateway groups. A run replaces its scenario's "last run" group; only a
// passing run replaces the "last success" group, so a failed run does not hide
// when the scenario last passed (alert RestoreTestStale reads it).
const (
	pushJobRun     = "disavery_drill"
	pushJobSuccess = "disavery_drill_success"
)

// results are every verdict, so dashboards can show the current one as 1.
var results = []Result{Pass, MissedTarget, Failed, Error}

// RunMetrics renders the report as the "last run" group in the Prometheus
// text format.
func (r *Report) RunMetrics() string {
	var b strings.Builder
	gauge := func(name, help string) {
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s gauge\n", name, help, name)
	}
	gauge("disavery_drill_last_run_timestamp_seconds", "When the drill's last run finished.")
	fmt.Fprintf(&b, "disavery_drill_last_run_timestamp_seconds %s\n", unix(r))
	gauge("disavery_drill_result", "The last run's result: 1 for its verdict, 0 for the others.")
	for _, res := range results {
		v := 0
		if r.Result == res {
			v = 1
		}
		fmt.Fprintf(&b, "disavery_drill_result{result=%q} %d\n", res, v)
	}
	gauge("disavery_drill_duration_seconds", "How long the last run took.")
	fmt.Fprintf(&b, "disavery_drill_duration_seconds %s\n", float(r.Duration().Seconds()))
	if len(r.Measurements) > 0 {
		gauge("disavery_drill_measurement_seconds", "Measured values of the last run and their targets (RPO, RTO, ...).")
		for _, m := range r.Measurements {
			fmt.Fprintf(&b, "disavery_drill_measurement_seconds{measure=%q,kind=\"actual\"} %s\n", m.Name, float(m.ActualSeconds))
			fmt.Fprintf(&b, "disavery_drill_measurement_seconds{measure=%q,kind=\"target\"} %s\n", m.Name, float(m.TargetSeconds))
		}
	}
	return b.String()
}

// SuccessMetrics renders the "last success" group.
func (r *Report) SuccessMetrics() string {
	return "# HELP disavery_drill_last_success_timestamp_seconds When the drill last passed.\n" +
		"# TYPE disavery_drill_last_success_timestamp_seconds gauge\n" +
		"disavery_drill_last_success_timestamp_seconds " + unix(r) + "\n"
}

// Push sends the report to a Pushgateway at base (e.g. http://pushgateway:9091):
// the "last run" group always, the "last success" group only for a pass.
func Push(ctx context.Context, client *http.Client, base string, r *Report) error {
	tier := r.Tier
	if tier == "" {
		tier = "none" // the Pushgateway rejects empty grouping labels in the path
	}
	put := func(job, body string) error {
		u := fmt.Sprintf("%s/metrics/job/%s/scenario/%s/tier/%s", strings.TrimSuffix(base, "/"),
			url.PathEscape(job), url.PathEscape(r.Scenario), url.PathEscape(tier))
		req, err := http.NewRequestWithContext(ctx, http.MethodPut, u, bytes.NewBufferString(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "text/plain; version=0.0.4")
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			return fmt.Errorf("pushgateway %s: status %d: %s", job, resp.StatusCode, bytes.TrimSpace(msg))
		}
		return nil
	}
	if err := put(pushJobRun, r.RunMetrics()); err != nil {
		return err
	}
	if r.Result == Pass {
		return put(pushJobSuccess, r.SuccessMetrics())
	}
	return nil
}

func unix(r *Report) string { return float(float64(r.FinishedAt.UnixMilli()) / 1000) }

func float(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
```

`internal/drill/runners.go` — `activeAlerts` takes a filter; `waitAlertRunner` passes `nil` (a drill waits for any alert it names):

```go
// blocksDrills reports whether an active alert means the lab is unfit for a
// drill. Alerts about the drill programme itself carry blocks_drills="false":
// RestoreTestStale fires in a lab that has never run a restore test, and it
// must not stop that restore test from starting.
func blocksDrills(labels map[string]string) bool { return labels["blocks_drills"] != "false" }

// activeAlerts returns the names of active (not silenced or inhibited) alerts
// whose labels keep accepts; a nil keep accepts all.
func activeAlerts(ctx context.Context, c *http.Client, base string, keep func(map[string]string) bool) ([]string, error) {
```
and inside its loop:
```go
	for _, a := range alerts {
		if keep == nil || keep(a.Labels) {
			names = append(names, a.Labels["alertname"])
		}
	}
```
In `waitAlertRunner.Run`: `active, err := activeAlerts(ctx, r.Client, r.URL, nil)`.

`internal/drill/labenv.go`, `noFiringAlerts`:
```go
	active, err := activeAlerts(ctx, &http.Client{Timeout: 5 * time.Second}, l.Env.Alertmanager, blocksDrills)
	...
	return "Alertmanager reports no alert that blocks drills", nil
```

`internal/lab/env.go`: add the field after `Alertmanager` and its default after `Alertmanager: "http://alertmanager:9093",`:
```go
	Pushgateway   string // base URL of the Pushgateway that keeps drill results
```
```go
		Pushgateway:   "http://pushgateway:9091",
```

`cmd/disavery/main.go`, `case "drill run"`: a new flag next to `--seed`, and the push after the report path is printed (imports gain `net/http`):
```go
		push := fs.String("pushgateway", lab.Default(".").Pushgateway, "Pushgateway for the result (empty: do not push)")
```
```go
		fmt.Fprintf(stdout, "\nreport %s\n", filepath.Join(dir, "report.md"))
		// Dashboards and alert RestoreTestStale read the result; failing to
		// push it does not change the drill's verdict.
		if *push != "" {
			pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			if err := report.Push(pctx, &http.Client{}, *push, r); err != nil {
				fmt.Fprintf(stderr, "warning: result not pushed to %s: %v\n", *push, err)
			}
			cancel()
		}
		return r.Result.ExitCode()
```
`context.WithoutCancel` matters: after Ctrl-C the drill's context is cancelled, but an `ERROR` result with an "aborted" note is still worth pushing.

- [ ] **Step 5: Run the tests to verify they pass**

```bash
go test -race ./internal/report/ ./internal/drill/ ./cmd/disavery/ && go vet ./...
```
Expected: `ok` for all three.

- [ ] **Step 6: Commit**

```bash
git add internal/report/push.go internal/report/push_test.go internal/lab/env.go internal/drill/runners.go internal/drill/runners_test.go internal/drill/labenv.go cmd/disavery/main.go
git commit -m "feat: push drill results to the Pushgateway"
```

---

### Task 2: Badge and seed commands

**Files:**
- Create: `internal/report/badge.go`, `internal/report/badge_test.go`
- Modify: `internal/drill/labenv.go`, `cmd/disavery/main.go`
- Test: `cmd/disavery/main_test.go`

**Interfaces:**
- Consumes: plan 2's `report.HistoryEntry`, `report.ReadHistory`; plan 4's `LabEnv.psql`, `LabEnv.DBHost`; the `pgbackrest-backup@.service` units from plan 1.
- Produces: `report.Badge`, `report.NewBadge(entries, scenario, label) Badge`; `disavery report badge`; `(*LabEnv).Seed(ctx, size, out) (int64, error)`; `disavery seed --size`; `parseSize`.

- [ ] **Step 1: Write the failing tests**

`internal/report/badge_test.go`:
```go
package report_test

import (
	"testing"

	"github.com/oplosy/disavery/internal/report"
)

func TestNewBadge(t *testing.T) {
	h := []report.HistoryEntry{
		{Scenario: "s6-restore-test", Result: report.Pass, StartedAt: at(0), DurationSeconds: 60},
		{Scenario: "s6-restore-test", Result: report.Failed, StartedAt: at(3600), DurationSeconds: 90},
		{Scenario: "s1-site-loss", Result: report.Pass, StartedAt: at(7200), DurationSeconds: 60},
	}
	got := report.NewBadge(h, "s6-restore-test", "last restore drill")
	want := report.Badge{SchemaVersion: 1, Label: "last restore drill", Message: "FAILED · 2026-10-07 13:01 UTC", Color: "red"}
	if got != want {
		t.Fatalf("got %+v", got)
	}
	if b := report.NewBadge(nil, "s6-restore-test", "x"); b.Message != "no run yet" || b.Color != "lightgrey" {
		t.Fatalf("empty history: %+v", b)
	}
}
```

Append to `cmd/disavery/main_test.go`:
```go
func TestParseSize(t *testing.T) {
	for in, want := range map[string]int64{"500MB": 500 << 20, "1gb": 1 << 30, "64 KB": 64 << 10, "10B": 10} {
		if got, err := parseSize(in); err != nil || got != want {
			t.Fatalf("%q: %d %v", in, got, err)
		}
	}
	for _, in := range []string{"", "MB", "-5MB", "5TB", "five MB"} {
		if _, err := parseSize(in); err == nil {
			t.Fatalf("%q accepted", in)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
go test ./internal/report/ ./cmd/disavery/
```
Expected: compile errors (`report.NewBadge`, `report.Badge`, `parseSize` undefined).

- [ ] **Step 3: Implement**

`internal/report/badge.go`:
```go
package report

// Badge is a shields.io endpoint badge (https://shields.io/badges/endpoint-badge).
type Badge struct {
	SchemaVersion int    `json:"schemaVersion"`
	Label         string `json:"label"`
	Message       string `json:"message"`
	Color         string `json:"color"`
}

var badgeColors = map[Result]string{Pass: "brightgreen", MissedTarget: "yellow", Failed: "red", Error: "lightgrey"}

// NewBadge describes the newest run of a scenario in the history, e.g.
// "PASS · 2026-10-08 02:31 UTC". The time is when that run finished: a static
// badge cannot say "6 h ago", and the date tells a reader the same.
func NewBadge(entries []HistoryEntry, scenario, label string) Badge {
	b := Badge{SchemaVersion: 1, Label: label, Message: "no run yet", Color: "lightgrey"}
	var last *HistoryEntry
	for i := range entries {
		if e := &entries[i]; e.Scenario == scenario && (last == nil || e.StartedAt.After(last.StartedAt)) {
			last = e
		}
	}
	if last == nil {
		return b
	}
	finished := last.StartedAt.Add(seconds(last.DurationSeconds)).UTC()
	b.Message = string(last.Result) + " · " + finished.Format("2006-01-02 15:04") + " UTC"
	b.Color = badgeColors[last.Result]
	return b
}
```

`internal/drill/labenv.go`, after `PickRestorePoint`:
```go
// seedBatch is how much ballast one statement inserts: 8,192 rows of 8 KiB.
const seedBatch = 8192 * 8192

// Seed grows the table ballast in the production database to at least size
// bytes of random, incompressible data, then takes a full backup to both
// repositories, so restores in nightly drills move a realistic amount of data
// (spec §12). The application never reads ballast. It returns the table size.
func (l *LabEnv) Seed(ctx context.Context, size int64, out io.Writer) (int64, error) {
	host, err := l.DBHost()
	if err != nil {
		return 0, err
	}
	sql := l.psql("docsvc")
	if _, err := sql.Query(ctx, host, `CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE TABLE IF NOT EXISTS ballast (id bigint PRIMARY KEY, payload bytea NOT NULL)`); err != nil {
		return 0, err
	}
	current := func() (int64, error) {
		rows, err := sql.Query(ctx, host, "SELECT pg_total_relation_size('ballast')")
		if err != nil {
			return 0, err
		}
		if len(rows) != 1 || len(rows[0]) != 1 {
			return 0, fmt.Errorf("unexpected result %v", rows)
		}
		return strconv.ParseInt(rows[0][0], 10, 64)
	}
	for {
		have, err := current()
		if err != nil {
			return 0, err
		}
		if have >= size {
			fmt.Fprintf(out, "ballast holds %d MB; taking a full backup to both repositories\n", have>>20)
			if _, err := l.Env.SSH.Run(ctx, host, "systemctl start pgbackrest-backup@full.service", nil); err != nil {
				return have, err
			}
			return have, nil
		}
		fmt.Fprintf(out, "ballast holds %d of %d MB\n", have>>20, size>>20)
		// pgcrypto's random bytes defeat both TOAST and pgBackRest compression.
		if _, err := sql.Query(ctx, host, `INSERT INTO ballast
SELECT coalesce((SELECT max(id) FROM ballast), 0) + g, gen_random_bytes(1024) || gen_random_bytes(1024) || gen_random_bytes(1024) || gen_random_bytes(1024) ||
  gen_random_bytes(1024) || gen_random_bytes(1024) || gen_random_bytes(1024) || gen_random_bytes(1024)
FROM generate_series(1, `+strconv.Itoa(seedBatch/8192)+`) g`); err != nil {
			return have, err
		}
	}
}
```

`cmd/disavery/main.go`:

Usage lines, after `report tiers` and after `vault undelete`:
```
  report badge [--scenario S]              shields.io endpoint JSON for the newest run (README badge)
```
```
  seed [--size 500MB]                      grow the production database with ballast, then back it up (CI drills)
```

`report badge` is a two-word command: add `args[1] == "badge"` to the `report` condition in the dispatcher:
```go
	if len(args) > 1 && !strings.HasPrefix(args[1], "-") && (cmd == "drill" || cmd == "runbook" || cmd == "env" || cmd == "canary" || cmd == "attachments" || cmd == "vault" || (cmd == "report" && (args[1] == "trend" || args[1] == "tiers" || args[1] == "badge"))) {
```

New cases, `report badge` before `report tiers` and `seed` before `canary run`:
```go
	case "report badge":
		history := fs.String("history", "", "history file (default <reports>/history.jsonl)")
		scenario := fs.String("scenario", "s6-restore-test", "scenario the badge reports")
		label := fs.String("label", "last restore drill", "badge label")
		if err := fs.Parse(args[1:]); err != nil {
			return exitError
		}
		path := *history
		if path == "" {
			path = filepath.Join(p.join(p.reports), "history.jsonl")
		}
		entries, err := report.ReadHistory(path)
		if err != nil {
			return fail(err)
		}
		if err := json.NewEncoder(stdout).Encode(report.NewBadge(entries, *scenario, *label)); err != nil {
			return fail(err)
		}
		return 0
```
```go
	case "seed":
		size := fs.String("size", "500MB", "ballast to reach in the production database (e.g. 500MB, 1GB)")
		if err := fs.Parse(args[1:]); err != nil {
			return exitError
		}
		n, err := parseSize(*size)
		if err != nil {
			return fail(err)
		}
		b, err := bia.Load(p.join(p.bia))
		if err != nil {
			return fail(err)
		}
		if _, err := p.lab(b, "drill", nil, stdout).Seed(ctx, n, stdout); err != nil {
			return fail(err)
		}
		return 0
```

And before `parseWithArg` (imports gain `strconv`):
```go
// parseSize parses a size such as 500MB or 1GB (binary units: 1 MB = 2^20 bytes).
func parseSize(s string) (int64, error) {
	units := []struct {
		suffix string
		shift  uint
	}{{"GB", 30}, {"MB", 20}, {"KB", 10}, {"B", 0}}
	for _, u := range units {
		if num, ok := strings.CutSuffix(strings.ToUpper(strings.TrimSpace(s)), u.suffix); ok {
			n, err := strconv.ParseInt(strings.TrimSpace(num), 10, 64)
			if err != nil || n <= 0 {
				return 0, fmt.Errorf("invalid size %q", s)
			}
			return n << u.shift, nil
		}
	}
	return 0, fmt.Errorf("invalid size %q (use B, KB, MB or GB)", s)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
go test -race ./internal/report/ ./internal/drill/ ./cmd/disavery/ && go vet ./... && make lint
```
Expected: `ok`; 0 lint issues.

- [ ] **Step 5: Commit**

```bash
git add internal/report/badge.go internal/report/badge_test.go internal/drill/labenv.go cmd/disavery/main.go cmd/disavery/main_test.go
git commit -m "feat: add the drill badge and the seed command"
```

---

### Task 3: Backup metrics, Pushgateway and alerts

**Files:**
- Create: `infra/ansible/roles/pgbackrest_exporter/{defaults,handlers,tasks}/main.yml`, `infra/ansible/roles/pgbackrest_exporter/templates/pgbackrest_exporter.service.j2`, `infra/ansible/roles/monitoring_targets/templates/pgbackrest.json.j2`, `infra/terraform/envs/local/monitoring/alerts_test.yml`
- Modify: `infra/ansible/playbooks/site.yml`, `infra/ansible/playbooks/rebuild-site.yml`, `infra/ansible/roles/monitoring_targets/tasks/main.yml`, `infra/terraform/envs/local/main.tf`, `infra/terraform/envs/local/monitoring.tf`, `infra/terraform/envs/local/monitoring/prometheus.yml`, `infra/terraform/envs/local/monitoring/alerts.yml`, `Makefile`, `.github/workflows/ci.yml`

**Interfaces:**
- Consumes: plan 3's `postgres_exporter` role, `monitoring_targets` role (file_sd with `role` from the topology), `alerts.yml`; Task 1's push format.
- Produces: `pgbackrest_backup_repo_since_last_completion_seconds` per node and repository; containers `pushgateway` (172.31.0.15) and the IP for `grafana` (172.31.0.16, container in Task 4); alerts `BackupTooOld`, `WalArchiveLagHigh`, `RpoBreachRisk`, `RestoreTestStale`; `make test-alerts`.

- [ ] **Step 1: Write the failing alert tests**

`infra/terraform/envs/local/monitoring/alerts_test.yml`:
```yaml
# Unit tests for alerts.yml: `promtool test rules alerts_test.yml` (CI runs it).
rule_files: [alerts.yml]
evaluation_interval: 15s

tests:
  # WAL archiving: 45 s is a warning, 50 s risks the pilot-light RPO target.
  - interval: 15s
    input_series:
      - series: 'pg_stat_archiver_last_archive_age{job="postgres", role="primary", instance="db-a"}'
        values: '20 30 46 46 46 55'
      - series: 'pg_stat_archiver_last_archive_age{job="postgres", role="standby", instance="db-b"}'
        values: '300x5'
    alert_rule_test:
      - eval_time: 30s
        alertname: WalArchiveLagHigh
        exp_alerts: []
      - eval_time: 60s
        alertname: WalArchiveLagHigh
        exp_alerts:
          - exp_labels: {severity: warning, job: postgres, role: primary, instance: db-a}
            exp_annotations: {summary: "db-a last archived WAL 46s ago (archive_timeout is 30 s)"}
      - eval_time: 60s
        alertname: RpoBreachRisk
        exp_alerts: []
      - eval_time: 75s
        alertname: RpoBreachRisk
        exp_alerts:
          - exp_labels: {severity: critical, job: postgres, role: primary, instance: db-a}
            exp_annotations: {summary: "db-a has unarchived WAL 55s old; the pilot-light RPO target is 60 s"}

  # Backups: older than 2 h for 5 minutes; a standby's empty repo1 is ignored.
  - interval: 1m
    input_series:
      - series: 'pgbackrest_backup_repo_since_last_completion_seconds{job="pgbackrest", role="primary", backup_type="incr", repo_key="2", instance="db-a"}'
        values: '7000+60x10'
      - series: 'pgbackrest_backup_repo_since_last_completion_seconds{job="pgbackrest", role="standby", backup_type="incr", repo_key="1", instance="db-b"}'
        values: '99999x10'
    alert_rule_test:
      - eval_time: 5m
        alertname: BackupTooOld
        exp_alerts: []
      - eval_time: 9m
        alertname: BackupTooOld
        exp_alerts:
          - exp_labels: {severity: warning, job: pgbackrest, role: primary, backup_type: incr, repo_key: "2", instance: db-a}
            exp_annotations: {summary: "repo2 has no new backup for 2h 5m 40s (policy: hourly)"}

  # Restore tests: stale after 48 h, and a lab that never ran one; neither blocks drills.
  - interval: 1m
    input_series:
      - series: 'disavery_drill_last_success_timestamp_seconds{job="disavery_drill_success", scenario="s6-restore-test", tier="none"}'
        values: '0x3000'
    alert_rule_test:
      - eval_time: 47h
        alertname: RestoreTestStale
        exp_alerts: []
      - eval_time: 50h
        alertname: RestoreTestStale
        exp_alerts:
          - exp_labels: {severity: warning, blocks_drills: "false", scenario: s6-restore-test}
            exp_annotations: {summary: "No random restore test (S6) has passed in the last 48 hours"}
  - interval: 1m
    input_series: []
    alert_rule_test:
      - eval_time: 2m
        alertname: RestoreTestStale
        exp_alerts:
          - exp_labels: {severity: warning, blocks_drills: "false", scenario: s6-restore-test}
            exp_annotations: {summary: "No random restore test (S6) has passed in the last 48 hours"}
```

`Makefile`, after the `canary` target:
```make
.PHONY: test-alerts
test-alerts: ## Unit-test the Prometheus alert rules
	docker run --rm -v "$(CURDIR)/infra/terraform/envs/local/monitoring:/m" -w /m --entrypoint promtool prom/prometheus:v3.15.0 test rules alerts_test.yml
```

- [ ] **Step 2: Run them to verify they fail**

```bash
make test-alerts
```
Expected: `FAILED` for `WalArchiveLagHigh`, `RpoBreachRisk`, `BackupTooOld` and `RestoreTestStale` (no such rules yet).

- [ ] **Step 3: Write the rules and the exporter**

`infra/terraform/envs/local/monitoring/alerts.yml`, whole:
```yaml
groups:
  - name: disavery
    rules:
      - alert: PostgresPrimaryDown
        expr: up{job="postgres", role="primary"} == 0 or pg_up{job="postgres", role="primary"} == 0
        for: 10s
        labels:
          severity: critical
        annotations:
          summary: "The primary database {{ $labels.instance }} does not answer"

      - alert: ReplicationLagHigh
        expr: pg_replication_lag_seconds{job="postgres", role="standby"} > 5
        for: 30s
        labels:
          severity: warning
        annotations:
          summary: "Standby {{ $labels.instance }} replays {{ $value | humanizeDuration }} behind the primary"

      # Backups run hourly (incremental) to both repositories; the metric for
      # incr falls back to the newest full backup. Same limit as preflight.
      - alert: BackupTooOld
        expr: pgbackrest_backup_repo_since_last_completion_seconds{job="pgbackrest", role="primary", backup_type="incr"} > 2 * 3600
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "repo{{ $labels.repo_key }} has no new backup for {{ $value | humanizeDuration }} (policy: hourly)"

      # archive_timeout is 30 s and the canary writes every second, so a
      # segment leaves the primary at least every 30 s. Same limit as preflight.
      - alert: WalArchiveLagHigh
        expr: pg_stat_archiver_last_archive_age{job="postgres", role="primary"} > 45
        for: 15s
        labels:
          severity: warning
        annotations:
          summary: "{{ $labels.instance }} last archived WAL {{ $value | humanizeDuration }} ago (archive_timeout is 30 s)"

      # A site loss now would lose more than pilot light's 60 s RPO target.
      - alert: RpoBreachRisk
        expr: pg_stat_archiver_last_archive_age{job="postgres", role="primary"} > 50
        labels:
          severity: critical
        annotations:
          summary: "{{ $labels.instance }} has unarchived WAL {{ $value | humanizeDuration }} old; the pilot-light RPO target is 60 s"

      # About the drill programme, not the lab: it fires in a lab that never
      # ran a restore test, so it must not stop drills (blocks_drills). Both
      # branches keep the scenario label, so the alert stays one series.
      - alert: RestoreTestStale
        expr: >-
          time() - max by (scenario) (disavery_drill_last_success_timestamp_seconds{scenario="s6-restore-test"}) > 48 * 3600
          or absent(disavery_drill_last_success_timestamp_seconds{scenario="s6-restore-test"})
        for: 1m
        labels:
          severity: warning
          blocks_drills: "false"
        annotations:
          summary: "No random restore test (S6) has passed in the last 48 hours"
```

`infra/ansible/roles/pgbackrest_exporter/defaults/main.yml`:
```yaml
pgbackrest_exporter_version: 0.24.0
# From the release's pgbackrest_exporter_checksums.txt; pinned so reruns need no network.
pgbackrest_exporter_sha256: a6e6a5adf85fb7c46dc3a3212efcba2045b9c543a1d162ddc868b821bd1d0f10
pgbackrest_exporter_release: "https://github.com/woblerr/pgbackrest_exporter/releases/download/v{{ pgbackrest_exporter_version }}"
pgbackrest_exporter_archive: "pgbackrest_exporter-{{ pgbackrest_exporter_version }}-linux-x86_64"
# pgbackrest info against both repositories this often; BackupTooOld allows
# for it.
pgbackrest_exporter_interval: 60
```

`infra/ansible/roles/pgbackrest_exporter/handlers/main.yml`:
```yaml
- name: Restart pgbackrest_exporter
  ansible.builtin.systemd_service:
    name: pgbackrest_exporter
    state: restarted
    daemon_reload: true
```

`infra/ansible/roles/pgbackrest_exporter/tasks/main.yml`:
```yaml
- name: Download pgbackrest_exporter
  ansible.builtin.get_url:
    url: "{{ pgbackrest_exporter_release }}/{{ pgbackrest_exporter_archive }}.tar.gz"
    checksum: "sha256:{{ pgbackrest_exporter_sha256 }}"
    dest: "/opt/{{ pgbackrest_exporter_archive }}.tar.gz"
    mode: "0644"
    timeout: 60

- name: Unpack pgbackrest_exporter
  ansible.builtin.unarchive:
    src: "/opt/{{ pgbackrest_exporter_archive }}.tar.gz"
    dest: /opt
    remote_src: true
    creates: "/opt/{{ pgbackrest_exporter_archive }}/pgbackrest_exporter"

- name: Install pgbackrest_exporter
  ansible.builtin.copy:
    src: "/opt/{{ pgbackrest_exporter_archive }}/pgbackrest_exporter"
    dest: /usr/local/bin/pgbackrest_exporter
    remote_src: true
    mode: "0755"
  notify: Restart pgbackrest_exporter

- name: Install the pgbackrest_exporter unit
  ansible.builtin.template:
    src: pgbackrest_exporter.service.j2
    dest: /etc/systemd/system/pgbackrest_exporter.service
    mode: "0644"
  notify: Restart pgbackrest_exporter

- name: Start pgbackrest_exporter
  ansible.builtin.systemd_service:
    name: pgbackrest_exporter
    state: started
    enabled: true
    daemon_reload: true
```

`infra/ansible/roles/pgbackrest_exporter/templates/pgbackrest_exporter.service.j2`:
```ini
[Unit]
Description=Prometheus pgBackRest exporter
After=network-online.target

[Service]
# pgbackrest.conf and repo1 belong to postgres; the exporter only runs
# `pgbackrest info`, which reads both repositories.
User=postgres
ExecStart=/usr/local/bin/pgbackrest_exporter --web.listen-address=:9854 --collect.interval={{ pgbackrest_exporter_interval }} --backrest.stanza-include={{ pgbackrest_stanza }}
Restart=always
RestartSec=2

[Install]
WantedBy=multi-user.target
```

`infra/ansible/roles/monitoring_targets/templates/pgbackrest.json.j2`:
```jinja
[
{% for host in groups['db'] | default([]) | sort %}
  {"targets": ["{{ host }}:9854"], "labels": {"instance": "{{ host }}", "site": "{{ hostvars[host].site }}", "role": "{{ 'primary' if hostvars[host].site == active_site else ('standby' if hostvars[host].site == standby_site else 'none') }}"}}{{ "," if not loop.last }}
{% endfor %}
]
```

`infra/ansible/roles/monitoring_targets/tasks/main.yml`, whole:
```yaml
# Prometheus picks the file up by itself (file_sd); no restart, so firing
# alerts resolve normally after a repoint.
- name: Publish the database and backup scrape targets
  ansible.builtin.template:
    src: "{{ item }}.json.j2"
    dest: "/monitoring/{{ item }}.json"
    mode: "0644"
  loop: [postgres, pgbackrest]
```

`infra/ansible/playbooks/site.yml`: add `- pgbackrest_exporter` after `- postgres_exporter` in the plays "Production database" and "Standby database". `infra/ansible/playbooks/rebuild-site.yml`: the same in its database play (a rebuilt site exports its backups at once).

`infra/terraform/envs/local/main.tf`, in `local.ip` after `alertmanager`:
```hcl
      pushgateway  = cidrhost(var.wan_subnet, 15)
      grafana      = cidrhost(var.wan_subnet, 16)
```

`infra/terraform/envs/local/monitoring/prometheus.yml`, append to `scrape_configs`:
```yaml
  # pgbackrest_exporter on the same nodes; the role label comes from the topology too.
  - job_name: pgbackrest
    file_sd_configs:
      - files: ["/etc/prometheus/targets/pgbackrest.json"]

  # Drill results pushed by `disavery drill run`. honor_labels keeps each push
  # group's job, scenario and tier labels.
  - job_name: pushgateway
    honor_labels: true
    static_configs:
      - targets: ["pushgateway:9091"]
```

`infra/terraform/envs/local/monitoring.tf`, append:
```hcl
# Drill results (spec §8): `disavery drill run` pushes each result here, and
# Prometheus scrapes it. Persisted, so a restart keeps when the restore test
# last passed (alert RestoreTestStale).
resource "docker_image" "pushgateway" {
  name         = "prom/pushgateway:v1.11.3"
  keep_locally = true
}

resource "docker_volume" "pushgateway" {
  name = "disavery-pushgateway"

  labels {
    label = "disavery.env"
    value = "drill"
  }
}

resource "docker_container" "pushgateway" {
  name    = "pushgateway"
  image   = docker_image.pushgateway.image_id
  restart = "unless-stopped"
  # See modules/node: Docker reports "bridge" for networks_advanced-only containers.
  network_mode = "bridge"
  command = [
    "--persistence.file=/data/metrics",
    "--persistence.interval=30s",
  ]

  networks_advanced {
    name         = data.docker_network.wan.name
    ipv4_address = local.ip.pushgateway
  }

  volumes {
    volume_name    = docker_volume.pushgateway.name
    container_path = "/data"
  }

  labels {
    label = "disavery.env"
    value = "drill"
  }
}
```

`.github/workflows/ci.yml`, job `infra`, after `terraform validate`:
```yaml
      - name: Alert rules
        run: docker run --rm -v "$PWD/infra/terraform/envs/local/monitoring:/m" -w /m --entrypoint promtool prom/prometheus:v3.15.0 test rules alerts_test.yml
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
make test-alerts
docker compose exec -T toolbox bash -c 'terraform fmt -check -recursive infra/terraform && cd infra/ansible && ansible-lint playbooks roles'
```
Expected: `SUCCESS`; fmt silent; `Passed: 0 failure(s)`.

- [ ] **Step 5: Commit**

```bash
git add infra Makefile .github/workflows/ci.yml
git commit -m "feat: alert on stale backups, WAL archive lag and stale restore tests"
```

---

### Task 4: Grafana dashboards

**Files:**
- Create: `infra/terraform/envs/local/monitoring/grafana/datasource.yml`, `infra/terraform/envs/local/monitoring/grafana/dashboards.yml`, `infra/terraform/envs/local/monitoring/grafana/dashboards/{backups,replication,drills}.json`
- Modify: `infra/terraform/envs/local/monitoring.tf`, `infra/terraform/envs/local/variables.tf`

**Interfaces:**
- Consumes: Task 3's metrics and the Pushgateway; plan 3's `postgres_exporter` metrics (`pg_replication_lag_seconds`, `pg_stat_archiver_*`, `pg_up`).
- Produces: container `grafana` on http://localhost:3000 (anonymous viewer); dashboards "Backup health", "Replication", "Drill history" (home).

- [ ] **Step 1: Write the provisioning files**

`infra/terraform/envs/local/monitoring/grafana/datasource.yml`:
```yaml
apiVersion: 1
datasources:
  - name: Prometheus
    uid: prometheus
    type: prometheus
    access: proxy
    url: http://prometheus:9090
    isDefault: true
    editable: false
```

`infra/terraform/envs/local/monitoring/grafana/dashboards.yml`:
```yaml
apiVersion: 1
providers:
  - name: disavery
    type: file
    disableDeletion: true
    allowUiUpdates: false
    options:
      path: /etc/grafana/dashboards
```

`infra/terraform/envs/local/monitoring/grafana/dashboards/backups.json`:
```json
{
  "uid": "disavery-backups",
  "title": "Backup health",
  "description": "Backups and WAL archiving of the primary (spec \u00a710).",
  "tags": [
    "disavery"
  ],
  "timezone": "utc",
  "schemaVersion": 39,
  "version": 1,
  "editable": false,
  "refresh": "30s",
  "time": {
    "from": "now-24h",
    "to": "now"
  },
  "panels": [
    {
      "id": 1,
      "type": "stat",
      "title": "Newest backup per repository",
      "datasource": {
        "type": "prometheus",
        "uid": "prometheus"
      },
      "gridPos": {
        "x": 0,
        "y": 0,
        "w": 8,
        "h": 6
      },
      "targets": [
        {
          "refId": "A",
          "datasource": {
            "type": "prometheus",
            "uid": "prometheus"
          },
          "expr": "pgbackrest_backup_repo_since_last_completion_seconds{role=\"primary\",backup_type=\"incr\"}",
          "legendFormat": "repo{{repo_key}}"
        }
      ],
      "fieldConfig": {
        "defaults": {
          "unit": "s",
          "thresholds": {
            "mode": "absolute",
            "steps": [
              {
                "color": "green",
                "value": null
              },
              {
                "color": "yellow",
                "value": 3600
              },
              {
                "color": "red",
                "value": 7200
              }
            ]
          },
          "color": {
            "mode": "thresholds"
          }
        },
        "overrides": []
      },
      "options": {
        "reduceOptions": {
          "calcs": [
            "lastNotNull"
          ],
          "fields": "",
          "values": false
        },
        "colorMode": "background",
        "graphMode": "none",
        "textMode": "value_and_name"
      },
      "description": "Age of the newest backup of any type in each repository. Alert BackupTooOld fires above 2 h."
    },
    {
      "id": 2,
      "type": "timeseries",
      "title": "WAL archive age (primary)",
      "datasource": {
        "type": "prometheus",
        "uid": "prometheus"
      },
      "gridPos": {
        "x": 8,
        "y": 0,
        "w": 16,
        "h": 6
      },
      "targets": [
        {
          "refId": "A",
          "datasource": {
            "type": "prometheus",
            "uid": "prometheus"
          },
          "expr": "pg_stat_archiver_last_archive_age{role=\"primary\"}",
          "legendFormat": "{{instance}}"
        }
      ],
      "fieldConfig": {
        "defaults": {
          "unit": "s",
          "thresholds": {
            "mode": "absolute",
            "steps": [
              {
                "color": "green",
                "value": null
              },
              {
                "color": "yellow",
                "value": 45
              },
              {
                "color": "red",
                "value": 50
              }
            ]
          },
          "color": {
            "mode": "palette-classic"
          },
          "custom": {
            "thresholdsStyle": {
              "mode": "line"
            }
          }
        },
        "overrides": []
      },
      "options": {},
      "description": "archive_timeout is 30 s. WalArchiveLagHigh fires above 45 s, RpoBreachRisk above 50 s (pilot-light RPO target: 60 s)."
    },
    {
      "id": 3,
      "type": "timeseries",
      "title": "Failed archive attempts",
      "datasource": {
        "type": "prometheus",
        "uid": "prometheus"
      },
      "gridPos": {
        "x": 0,
        "y": 6,
        "w": 12,
        "h": 6
      },
      "targets": [
        {
          "refId": "A",
          "datasource": {
            "type": "prometheus",
            "uid": "prometheus"
          },
          "expr": "increase(pg_stat_archiver_failed_count{role=\"primary\"}[5m])",
          "legendFormat": "{{instance}}"
        }
      ],
      "fieldConfig": {
        "defaults": {
          "unit": "short"
        },
        "overrides": []
      },
      "options": {}
    },
    {
      "id": 4,
      "type": "stat",
      "title": "pgbackrest_exporter",
      "datasource": {
        "type": "prometheus",
        "uid": "prometheus"
      },
      "gridPos": {
        "x": 12,
        "y": 6,
        "w": 12,
        "h": 6
      },
      "targets": [
        {
          "refId": "A",
          "datasource": {
            "type": "prometheus",
            "uid": "prometheus"
          },
          "expr": "pgbackrest_exporter_status",
          "legendFormat": "{{instance}}"
        }
      ],
      "fieldConfig": {
        "defaults": {
          "thresholds": {
            "mode": "absolute",
            "steps": [
              {
                "color": "red",
                "value": null
              },
              {
                "color": "green",
                "value": 1
              }
            ]
          },
          "color": {
            "mode": "thresholds"
          }
        },
        "overrides": []
      },
      "options": {
        "reduceOptions": {
          "calcs": [
            "lastNotNull"
          ],
          "fields": "",
          "values": false
        },
        "colorMode": "background",
        "graphMode": "none",
        "textMode": "value_and_name"
      },
      "description": "1: the exporter read both repositories; 0: pgbackrest info failed."
    }
  ]
}
```

`infra/terraform/envs/local/monitoring/grafana/dashboards/replication.json`:
```json
{
  "uid": "disavery-replication",
  "title": "Replication",
  "description": "Streaming replication of the warm standby.",
  "tags": [
    "disavery"
  ],
  "timezone": "utc",
  "schemaVersion": 39,
  "version": 1,
  "editable": false,
  "refresh": "30s",
  "time": {
    "from": "now-24h",
    "to": "now"
  },
  "panels": [
    {
      "id": 1,
      "type": "timeseries",
      "title": "Standby replay lag",
      "datasource": {
        "type": "prometheus",
        "uid": "prometheus"
      },
      "gridPos": {
        "x": 0,
        "y": 0,
        "w": 16,
        "h": 8
      },
      "targets": [
        {
          "refId": "A",
          "datasource": {
            "type": "prometheus",
            "uid": "prometheus"
          },
          "expr": "pg_replication_lag_seconds{role=\"standby\"}",
          "legendFormat": "{{instance}}"
        }
      ],
      "fieldConfig": {
        "defaults": {
          "unit": "s",
          "thresholds": {
            "mode": "absolute",
            "steps": [
              {
                "color": "green",
                "value": null
              },
              {
                "color": "red",
                "value": 5
              }
            ]
          },
          "color": {
            "mode": "palette-classic"
          },
          "custom": {
            "thresholdsStyle": {
              "mode": "line"
            }
          }
        },
        "overrides": []
      },
      "options": {},
      "description": "Warm standby only. ReplicationLagHigh fires above 5 s (the warm-standby RPO target)."
    },
    {
      "id": 2,
      "type": "stat",
      "title": "Databases up",
      "datasource": {
        "type": "prometheus",
        "uid": "prometheus"
      },
      "gridPos": {
        "x": 16,
        "y": 0,
        "w": 8,
        "h": 8
      },
      "targets": [
        {
          "refId": "A",
          "datasource": {
            "type": "prometheus",
            "uid": "prometheus"
          },
          "expr": "pg_up",
          "legendFormat": "{{instance}} ({{role}})"
        }
      ],
      "fieldConfig": {
        "defaults": {
          "thresholds": {
            "mode": "absolute",
            "steps": [
              {
                "color": "red",
                "value": null
              },
              {
                "color": "green",
                "value": 1
              }
            ]
          },
          "color": {
            "mode": "thresholds"
          }
        },
        "overrides": []
      },
      "options": {
        "reduceOptions": {
          "calcs": [
            "lastNotNull"
          ],
          "fields": "",
          "values": false
        },
        "colorMode": "background",
        "graphMode": "none",
        "textMode": "value_and_name"
      }
    }
  ]
}
```

`infra/terraform/envs/local/monitoring/grafana/dashboards/drills.json`:
```json
{
  "uid": "disavery-drills",
  "title": "Drill history",
  "description": "Results of the recovery drills (spec \u00a78).",
  "tags": [
    "disavery"
  ],
  "timezone": "utc",
  "schemaVersion": 39,
  "version": 1,
  "editable": false,
  "refresh": "30s",
  "time": {
    "from": "now-24h",
    "to": "now"
  },
  "panels": [
    {
      "id": 1,
      "type": "table",
      "title": "Last run per scenario",
      "datasource": {
        "type": "prometheus",
        "uid": "prometheus"
      },
      "gridPos": {
        "x": 0,
        "y": 0,
        "w": 14,
        "h": 8
      },
      "targets": [
        {
          "refId": "A",
          "datasource": {
            "type": "prometheus",
            "uid": "prometheus"
          },
          "expr": "disavery_drill_result == 1",
          "legendFormat": "",
          "instant": true,
          "format": "table"
        }
      ],
      "fieldConfig": {
        "defaults": {},
        "overrides": []
      },
      "options": {
        "showHeader": true
      },
      "description": "The newest result of every scenario and tier, as pushed by `disavery drill run`.",
      "transformations": [
        {
          "id": "organize",
          "options": {
            "excludeByName": {
              "Time": true,
              "__name__": true,
              "job": true,
              "instance": true,
              "Value": true,
              "alertstate": true
            }
          }
        }
      ]
    },
    {
      "id": 2,
      "type": "stat",
      "title": "Since the last passing restore test",
      "datasource": {
        "type": "prometheus",
        "uid": "prometheus"
      },
      "gridPos": {
        "x": 14,
        "y": 0,
        "w": 10,
        "h": 8
      },
      "targets": [
        {
          "refId": "A",
          "datasource": {
            "type": "prometheus",
            "uid": "prometheus"
          },
          "expr": "time() - max(disavery_drill_last_success_timestamp_seconds{scenario=\"s6-restore-test\"})",
          "legendFormat": "S6"
        }
      ],
      "fieldConfig": {
        "defaults": {
          "unit": "s",
          "thresholds": {
            "mode": "absolute",
            "steps": [
              {
                "color": "green",
                "value": null
              },
              {
                "color": "yellow",
                "value": 86400
              },
              {
                "color": "red",
                "value": 172800
              }
            ]
          },
          "color": {
            "mode": "thresholds"
          }
        },
        "overrides": []
      },
      "options": {
        "reduceOptions": {
          "calcs": [
            "lastNotNull"
          ],
          "fields": "",
          "values": false
        },
        "colorMode": "background",
        "graphMode": "none",
        "textMode": "value_and_name"
      },
      "description": "RestoreTestStale fires after 48 h."
    },
    {
      "id": 3,
      "type": "timeseries",
      "title": "Measured RPO, RTO and downtime",
      "datasource": {
        "type": "prometheus",
        "uid": "prometheus"
      },
      "gridPos": {
        "x": 0,
        "y": 8,
        "w": 24,
        "h": 9
      },
      "targets": [
        {
          "refId": "A",
          "datasource": {
            "type": "prometheus",
            "uid": "prometheus"
          },
          "expr": "disavery_drill_measurement_seconds{kind=\"actual\"}",
          "legendFormat": "{{scenario}} {{tier}} {{measure}}"
        }
      ],
      "fieldConfig": {
        "defaults": {
          "unit": "s"
        },
        "overrides": []
      },
      "options": {
        "legend": {
          "displayMode": "table",
          "placement": "right"
        }
      },
      "description": "Each point is the newest run's value; targets are in docs/bia.yaml."
    },
    {
      "id": 4,
      "type": "table",
      "title": "Firing alerts",
      "datasource": {
        "type": "prometheus",
        "uid": "prometheus"
      },
      "gridPos": {
        "x": 0,
        "y": 17,
        "w": 24,
        "h": 6
      },
      "targets": [
        {
          "refId": "A",
          "datasource": {
            "type": "prometheus",
            "uid": "prometheus"
          },
          "expr": "ALERTS{alertstate=\"firing\"}",
          "legendFormat": "",
          "instant": true,
          "format": "table"
        }
      ],
      "fieldConfig": {
        "defaults": {},
        "overrides": []
      },
      "options": {
        "showHeader": true
      },
      "transformations": [
        {
          "id": "organize",
          "options": {
            "excludeByName": {
              "Time": true,
              "__name__": true,
              "job": true,
              "instance": true,
              "Value": true,
              "alertstate": true
            }
          }
        }
      ]
    }
  ]
}
```

- [ ] **Step 2: The container**

`infra/terraform/envs/local/variables.tf`, before `prometheus_host_port`:
```hcl
variable "grafana_host_port" {
  description = "Host port published for Grafana (anonymous, read-only)."
  type        = number
  default     = 3000
}
```

`infra/terraform/envs/local/monitoring.tf`, append:
```hcl
# Dashboards for backup health, replication and drill history (spec §10),
# provisioned from files; anonymous users can view, nobody can edit.
resource "docker_image" "grafana" {
  name         = "grafana/grafana:13.2.2"
  keep_locally = true
}

resource "docker_container" "grafana" {
  name    = "grafana"
  image   = docker_image.grafana.image_id
  restart = "unless-stopped"
  # See modules/node: Docker reports "bridge" for networks_advanced-only containers.
  network_mode = "bridge"
  env = [
    "GF_AUTH_ANONYMOUS_ENABLED=true",
    "GF_AUTH_ANONYMOUS_ORG_ROLE=Viewer",
    "GF_AUTH_DISABLE_LOGIN_FORM=true",
    "GF_USERS_ALLOW_SIGN_UP=false",
    "GF_ANALYTICS_REPORTING_ENABLED=false",
    "GF_ANALYTICS_CHECK_FOR_UPDATES=false",
    "GF_DASHBOARDS_DEFAULT_HOME_DASHBOARD_PATH=/etc/grafana/dashboards/drills.json",
  ]

  ports {
    internal = 3000
    external = var.grafana_host_port
  }

  networks_advanced {
    name         = data.docker_network.wan.name
    ipv4_address = local.ip.grafana
  }

  upload {
    file    = "/etc/grafana/provisioning/datasources/prometheus.yml"
    content = file("${path.module}/monitoring/grafana/datasource.yml")
  }
  upload {
    file    = "/etc/grafana/provisioning/dashboards/disavery.yml"
    content = file("${path.module}/monitoring/grafana/dashboards.yml")
  }
  dynamic "upload" {
    for_each = fileset("${path.module}/monitoring/grafana/dashboards", "*.json")
    content {
      file    = "/etc/grafana/dashboards/${upload.value}"
      content = file("${path.module}/monitoring/grafana/dashboards/${upload.value}")
    }
  }

  labels {
    label = "disavery.env"
    value = "drill"
  }
}
```

Every dashboard is uploaded into the container, so editing a JSON file replaces Grafana on the next `make up` (it keeps no state worth keeping: anonymous, provisioned).

- [ ] **Step 3: Validate and commit**

```bash
for f in infra/terraform/envs/local/monitoring/grafana/dashboards/*.json; do jq empty "$f" || echo "invalid $f"; done
docker compose exec -T toolbox bash -c 'terraform fmt -check -recursive infra/terraform && terraform -chdir=infra/terraform/envs/local validate'
git add infra/terraform
git commit -m "feat: provision Grafana dashboards for backups, replication and drills"
```
Expected: no `invalid`; `Success! The configuration is valid.`

---

### Task 5: Nightly and weekly CI drills

**Files:**
- Create: `scripts/drill-programme.sh`, `scripts/publish-history.sh`, `.github/workflows/drills.yml`
- Modify: `internal/executor/executor.go`, `internal/drill/drill.go`, `docs/runbooks.md`, `images/toolbox/Dockerfile`, `infra/ansible/roles/base/tasks/main.yml`, `.gitignore`, `.github/workflows/ci.yml`
- Test: `internal/executor/executor_test.go`, `internal/drill/drill_test.go`

**Interfaces:**
- Consumes: `make up`, `make smoke`, Task 2's `seed` and `report badge`, plan 2's `report trend`, plan 3's `report tiers` and `env reset`, every runbook.
- Produces: `executor.Options.RetryDelay` (drills use 3 s); `drill run` prints `preflight failed (<checks>); nothing was changed` when it refuses to start; workflow `drills` (cron nightly 02:17 UTC, weekly Sunday 04:17 UTC, manual with `programme` and `history_branch`); branch `drill-history`.

- [ ] **Step 1: Write the failing tests**

Unattended drills back to back need two things a person at the keyboard never noticed: retries that wait, and a refused drill that says why on its output (the programme retries exactly that case).

`internal/executor/executor_test.go`, after `TestRetries`:
```go
// TestRetryDelay: attempts are spaced out, so a retry can outlast a node that
// is still booting (S7 fences site a right after starting it); cancelling the
// drill ends the wait at once.
func TestRetryDelay(t *testing.T) {
	rb := &runbook.Runbook{Phases: []runbook.Phase{{Name: "recover", Steps: []runbook.Step{
		{ID: "a", Run: "flaky:3", Retries: 2},
	}}}}
	start := time.Now()
	res := executor.Execute(context.Background(), rb, executor.Options{
		Runners:    map[string]executor.Runner{runbook.KindRun: &fakeRunner{}},
		RetryDelay: 50 * time.Millisecond,
	})
	if res.Outcome != executor.Completed || res.Steps[0].Attempts != 3 {
		t.Fatalf("result %+v", res)
	}
	if elapsed := time.Since(start); elapsed < 100*time.Millisecond {
		t.Fatalf("three attempts took %s, want two pauses of 50ms", elapsed)
	}

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	start = time.Now()
	rb.Phases[0].Steps[0] = runbook.Step{ID: "b", Run: "fail", Retries: 3}
	res = executor.Execute(ctx, rb, executor.Options{
		Runners:    map[string]executor.Runner{runbook.KindRun: &fakeRunner{}},
		RetryDelay: time.Hour,
	})
	if res.Outcome != executor.Cancelled || res.Steps[0].Attempts != 1 || time.Since(start) > 5*time.Second {
		t.Fatalf("cancelled wait: %+v after %s", res, time.Since(start))
	}
}
```

`internal/drill/drill_test.go`, `TestOutcomes` captures the output and checks every note appears in it. From the start of the `t.Run` body to the end of the function:
```go
			o, _ := setup(t, tc.max)
			var out strings.Builder
			o.Out = &out
			l := &fakeLab{preflight: pass(), checks: map[string]verify.Status{"amcheck": verify.Pass}}
			if tc.mutate != nil {
				tc.mutate(&o, l)
			}
			r, _, err := drill.Run(context.Background(), o, l)
			if err != nil {
				t.Fatal(err)
			}
			if r.Result != tc.result || len(r.Notes) == 0 || !strings.Contains(r.Notes[0], tc.note) || (len(l.ran) > 0) != tc.executed {
				t.Fatalf("result %s notes %v ran %v", r.Result, r.Notes, l.ran)
			}
			// The note is printed too: scripts/drill-programme.sh retries a
			// drill whose output says its preflight failed.
			if !strings.Contains(out.String(), tc.note) {
				t.Fatalf("note %q not in the output:\n%s", tc.note, out.String())
			}
		})
	}
}
```

```bash
go test ./internal/executor/ ./internal/drill/
```
Expected: compile error (`unknown field RetryDelay`); after Step 2's executor change alone, FAIL for `TestOutcomes/preflight` (`note "preflight failed (backups-fresh)" not in the output`).

- [ ] **Step 2: Implement**

`internal/executor/executor.go`, a new field at the end of `Options`:
```go
	// RetryDelay is the pause before each retry of a failed step; retrying at
	// once cannot outlast what usually fails a step, e.g. a node still booting.
	RetryDelay time.Duration
```
and the start of the attempt loop in `step`:
```go
	for attempt := 1; attempt <= s.Retries+1; attempt++ {
		if attempt > 1 && e.opt.RetryDelay > 0 {
			fmt.Fprintf(log, "# waiting %s\n", e.opt.RetryDelay)
			select {
			case <-ctx.Done():
			case <-time.After(e.opt.RetryDelay):
			}
			if ctx.Err() != nil {
				break
			}
		}
		rec.Attempts = attempt
```

`internal/drill/drill.go`, after the imports:
```go
// retryDelay spaces out the attempts of a step with retries: S7's fencing
// steps retry until the restarted site-a nodes accept SSH, which takes a few
// seconds on a CI runner.
const retryDelay = 3 * time.Second
```
`RetryDelay: retryDelay` in the `executor.Options` of the main run (not preflight's), and where preflight failures end the run:
```go
	if len(failedPreflight) > 0 {
		r.Result = report.Error
		r.Notes = []string{"preflight failed (" + strings.Join(failedPreflight, ", ") + "); nothing was changed"}
		// scripts/drill-programme.sh retries on this line.
		fmt.Fprintf(o.Out, "\n%s\n", r.Notes[0])
		return finish()
	}
```

`docs/runbooks.md`: the description of `retries` becomes "Extra attempts (0–10), 3 s apart".

```bash
go test -race ./internal/executor/ ./internal/drill/ && go run ./cmd/disavery runbook lint
```
Expected: `ok` for both; every runbook `ok`.

- [ ] **Step 3: Write the scripts**

`scripts/drill-programme.sh` (executable):
```bash
#!/usr/bin/env bash
# Runs a drill programme against the lab, as the nightly and weekly CI jobs do
# (spec §12). Runs on the host, next to `make`.
#
#   nightly  the random restore test (S6) plus one scenario that rotates by
#            day of the year: S1 on pilot light, S1 on warm standby, S2, S3,
#            S4, S5. Site losses are followed by their failback (S7).
#   weekly   the whole catalog.
#
# A drill whose preflight fails changed nothing (ERROR); it is retried for up
# to five minutes, because the previous drill's recovery may still be settling
# (a firing PostgresPrimaryDown, a minute of steady canary writes).
# Exits with the worst result: 0 PASS, 2 MISSED_TARGET, 3 FAILED, 4 ERROR.
# Usage: scripts/drill-programme.sh nightly|weekly
set -uo pipefail

worst=0
tb() { docker compose exec -T toolbox "$@"; }

drill() {
  local out code
  for _ in $(seq 1 15); do
    out=$(tb build/disavery drill run "$@" 2>&1)
    code=$?
    printf '%s\n' "$out"
    if (( code == 4 )) && grep -q 'preflight failed' <<<"$out"; then
      sleep 20
      continue
    fi
    break
  done
  (( code > worst )) && worst=$code
  return 0
}

tier() {
  echo "== baseline: $1"
  if ! tb build/disavery env reset --tier "$1"; then
    worst=4
  fi
}

scenario() {
  case $1 in
    s1-pilot-light)  drill s1-site-loss --tier pilot-light --yes; drill s7-failback --tier pilot-light ;;
    s1-warm-standby) tier warm-standby
                     drill s1-site-loss --tier warm-standby --yes; drill s7-failback --tier warm-standby
                     tier pilot-light ;;
    s5)              drill s5-secret-loss --yes; drill s7-failback --tier pilot-light ;;
    s2)              drill s2-drop-table --yes ;;
    s3)              drill s3-bad-migration --yes ;;
    s4)              drill s4-ransomware --yes ;;
    *)               echo "unknown scenario $1" >&2; exit 4 ;;
  esac
}

rotation=(s1-pilot-light s1-warm-standby s2 s3 s4 s5)

case ${1:-} in
  nightly)
    drill s6-restore-test
    pick=${rotation[$(( 10#$(date -u +%j) % ${#rotation[@]} ))]}
    echo "== rotating scenario: $pick"
    scenario "$pick"
    ;;
  weekly)
    drill s6-restore-test
    for s in s2 s3 s4 s5 s1-pilot-light s1-warm-standby; do
      scenario "$s"
    done
    ;;
  *)
    echo "usage: $0 nightly|weekly" >&2
    exit 4
    ;;
esac
exit "$worst"
```

`scripts/publish-history.sh` (executable):
```bash
#!/usr/bin/env bash
# Appends this lab's drill history to the history branch and refreshes the
# badge and the trend tables there (spec §12): main stays free of bot commits.
# Runs on the CI host after scripts/drill-programme.sh.
# Usage: scripts/publish-history.sh [branch]   (default drill-history)
set -euo pipefail

branch=${1:-drill-history}
dir=.drill-history # inside the repository, so the toolbox (/work) sees it

[[ -s reports/history.jsonl ]] || { echo "no drill history to publish"; exit 0; }
git worktree remove --force "$dir" 2>/dev/null || true
if git fetch --quiet origin "$branch" 2>/dev/null; then
  git worktree add --quiet -B "$branch" "$dir" "origin/$branch"
else
  git worktree add --quiet --detach "$dir"
  git -C "$dir" checkout --quiet --orphan "$branch"
  git -C "$dir" rm -rfq .
fi

cat reports/history.jsonl >> "$dir/history.jsonl"
tb() { docker compose exec -T toolbox build/disavery "$@"; }
tb report badge --history "$dir/history.jsonl" > "$dir/badge.json"
{
  echo "# Drill history"
  echo
  echo "Written by the drills workflow; the badge in the README reads badge.json."
  echo
  echo "## Trend"
  echo
  tb report trend --history "$dir/history.jsonl"
  echo
  echo "## DR tiers (site loss, S1)"
  echo
  tb report tiers --history "$dir/history.jsonl"
} > "$dir/README.md"

git -C "$dir" add history.jsonl badge.json README.md
git -C "$dir" -c user.name="github-actions[bot]" -c user.email="41898282+github-actions[bot]@users.noreply.github.com" \
  commit --quiet -m "chore: drill history of run ${GITHUB_RUN_ID:-local}"
git -C "$dir" push --quiet origin "HEAD:$branch"
git worktree remove --force "$dir"
echo "published to $branch"
```

```bash
git update-index --chmod=+x scripts/drill-programme.sh scripts/publish-history.sh
```

`.gitignore`, append:
```
/.drill-history/
```

- [ ] **Step 4: Make the lab buildable on a fresh runner**

Two things only a fresh machine shows (both found by the prototype's first CI runs). The toolbox must trust the checkout, which belongs to the runner's user, not root. `images/toolbox/Dockerfile`, append:
```dockerfile
# The checkout is bind-mounted at /work and owned by the host user (uid 1001
# on CI runners); without this, git refuses it and `go build` cannot stamp
# the binaries with their commit.
RUN git config --system --add safe.directory /work
```

And nodes from a node image built minutes ago must still update their package cache. `infra/ansible/roles/base/tasks/main.yml`, the first task becomes:
```yaml
# The node image ships without package lists, but Ansible judges the cache's
# age by the lists directory's mtime: on a node image built minutes ago (CI)
# the empty cache looks fresh and is never updated.
- name: Find the package lists
  ansible.builtin.find:
    paths: /var/lib/apt/lists
    patterns: "*_Packages*"
  register: base_apt_lists

- name: Install base packages
  ansible.builtin.apt:
    name: [ca-certificates, curl, gnupg, jq]
    update_cache: true
    cache_valid_time: "{{ 3600 if base_apt_lists.matched > 0 else 0 }}"
```

- [ ] **Step 5: The workflows**

`.github/workflows/drills.yml`:
```yaml
name: drills

# Brings the lab up on a runner and proves recovery (spec §12): every night
# the random restore test plus one rotating scenario, every Sunday the whole
# catalog. Results go to the drill-history branch (badge, trend), reports to
# the run's artifacts.
on:
  schedule:
    - cron: "17 2 * * *" # nightly
    - cron: "17 4 * * 0" # weekly, Sunday
  workflow_dispatch:
    inputs:
      programme:
        description: Drill programme
        type: choice
        options: [nightly, weekly]
        default: nightly
      history_branch:
        description: Branch that keeps the drill history
        type: string
        default: drill-history

permissions:
  contents: write # the history branch

concurrency:
  group: drills
  cancel-in-progress: false

jobs:
  drills:
    runs-on: ubuntu-24.04
    timeout-minutes: 240
    env:
      PROGRAMME: ${{ inputs.programme || (github.event.schedule == '17 4 * * 0' && 'weekly') || 'nightly' }}
      HISTORY_BRANCH: ${{ inputs.history_branch || 'drill-history' }}
    steps:
      - uses: actions/checkout@v4

      - name: Bring the lab up
        run: make up

      - name: Seed the database with about 500 MB
        run: docker compose exec -T toolbox build/disavery seed --size 500MB

      - name: Smoke test
        run: make smoke

      - name: Drills (${{ env.PROGRAMME }})
        id: drills
        run: scripts/drill-programme.sh "$PROGRAMME"

      - name: Keep the reports
        if: always()
        uses: actions/upload-artifact@v4
        with:
          name: drill-reports-${{ github.run_id }}
          path: reports/
          retention-days: 30

      - name: Publish the history and badge
        if: always() && steps.drills.outcome != 'skipped'
        run: scripts/publish-history.sh "$HISTORY_BRANCH"
```

`.github/workflows/ci.yml`, job `infra`, after "Alert rules":
```yaml
      - name: Drill scripts
        run: shellcheck scripts/*.sh
```

- [ ] **Step 6: Check and commit**

```bash
shellcheck scripts/*.sh
docker run --rm -v "$PWD:/repo" -w /repo rhysd/actionlint:1.7.7 .github/workflows/drills.yml .github/workflows/ci.yml
make toolbox
git add internal/executor internal/drill docs/runbooks.md scripts .github images/toolbox/Dockerfile infra/ansible/roles/base .gitignore
git commit -m "ci: run nightly and weekly drills and publish their history"
```
Expected: shellcheck and actionlint silent.

---

### Task 6: ADR, BIA, spec, README

**Files:**
- Create: `docs/adr/0009-drill-evidence-pushgateway-and-history-branch.md`
- Modify: `docs/bia.md`, `docs/superpowers/specs/2026-10-07-disavery-dr-design.md`, `README.md`, `CLAUDE.md`

- [ ] **Step 1: ADR 0009**

`docs/adr/0009-drill-evidence-pushgateway-and-history-branch.md`:
```markdown
# ADR 0009: Drill evidence in the Pushgateway and on a history branch

- Status: accepted
- Date: 2026-10-08

## Context
A drill writes `reports/<run>/` and appends to `reports/history.jsonl`, both
local to the lab. Spec §8 and §12 also want the results where people and
alerts see them: an alert when the last successful restore test is older than
48 hours, Grafana trends, and a README badge kept up to date by nightly CI
drills, without bot commits on `main`.

CI labs are ephemeral: every nightly run brings a fresh lab up on a runner, so
nothing that lives in the lab survives the run.

## Decision
- `disavery drill run` pushes every result to the lab's Pushgateway in two
  groups per scenario and tier: the last run (result, duration, measured values
  and targets) and the last success. A failed run replaces only the first, so
  it cannot hide when the scenario last passed. Pushing is best effort: a
  failure is a warning and never changes the drill's verdict. The Pushgateway
  persists to a volume, so a restart keeps the history.
- `RestoreTestStale` fires when the last successful S6 is older than 48 hours
  or missing. It is about the drill programme, not the lab, so it carries
  `blocks_drills="false"` and preflight ignores it; otherwise a fresh lab could
  never run its first restore test.
- CI publishes the run's `history.jsonl` lines to the `drill-history` branch,
  with `badge.json` (a shields.io endpoint badge) and a README of trend tables.
  The branch is the only durable record of CI drills; `main` stays free of
  generated commits.
- The badge shows the result and the finishing time of the newest S6 ("PASS ·
  2026-10-08 02:31 UTC") rather than spec §12's "6 h ago": a static file cannot
  age, and a date tells a reader the same.

## Consequences
- Locally, Grafana shows the lab's drills; in CI, the history branch and the
  run artifacts (reports, 30 days) are the evidence.
- A failed or skipped nightly run leaves the badge on the previous result and
  its date, which makes staleness visible to a reader.
- The history branch grows by a few lines per night; at this rate it needs no
  pruning in v1.
```

- [ ] **Step 2: BIA**

`docs/bia.md`, after the section "Preflight thresholds":
```markdown
## Alerts

The alerts that watch recoverability between drills reuse the preflight
limits, so the lab warns about exactly what would stop a drill.

| Alert | Fires when | Why |
|---|---|---|
| `BackupTooOld` | a repository's newest backup on the primary is older than 2 h, for 5 min | `max_backup_age`: incremental backups run hourly to both repositories |
| `WalArchiveLagHigh` | the primary's last archived segment is older than 45 s | `max_archive_age`: `archive_timeout` is 30 s |
| `RpoBreachRisk` | … older than 50 s | a site loss now would lose more than pilot light's 60 s RPO target |
| `ReplicationLagHigh` | the warm standby replays more than 5 s behind | the warm-standby RPO target is 5 s |
| `RestoreTestStale` | no S6 has passed for 48 h, or none ever | spec §8; it does not stop drills, or a new lab could never run its first restore test |
```

- [ ] **Step 3: Spec amendments**

In `docs/superpowers/specs/2026-10-07-disavery-dr-design.md`:
- §8, last bullet: "Results are pushed to Prometheus Pushgateway — the last run and the last success per scenario and tier, so a failed run cannot hide when the scenario last passed; a failed push never changes the verdict ([ADR 0009](../../adr/0009-drill-evidence-pushgateway-and-history-branch.md)). Grafana shows trends and the **"last successful restore test older than 48 h"** alert fires from it."
- §10, first sentence: "Prometheus scrapes PostgreSQL (`postgres_exporter`), pgBackRest (`pgbackrest_exporter`) and the Pushgateway." Add after the alert list: "Thresholds reuse the BIA's preflight limits (`docs/bia.md`, Alerts). Alerts about the drill programme rather than the lab (`RestoreTestStale`) carry `blocks_drills="false"` and do not stop preflight."
- §12, Outputs: "…README shows a "last restore drill: PASS · 2026-10-09 02:31 UTC" badge (the time the newest S6 finished; a static badge cannot age)."
- §14, add: "MinIO, `docsvc` and node metrics in Prometheus (v1 scrapes what its alerts and dashboards read)."

- [ ] **Step 4: README**

`README.md`, whole:
markdown
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


- [ ] **Step 5: CLAUDE.md**

Status: "M1–M5 shipped (PRs #2, #4, #6, #8, and this plan's implementation PR); v1 is complete. Next: the follow-ups in spec §14." Move this plan from "Current plan" to "Previous plans". The table stays.

- [ ] **Step 6: Commit**

```bash
git add docs README.md CLAUDE.md
git commit -m "docs: describe monitoring, CI drills and the drill history"
```

---

### Task 7: The lab, end to end

- [ ] **Step 1: Bring the lab up, twice**

```bash
make up
make up 2>&1 | grep -E 'No changes|: ok='
make smoke
```
Expected: second run `No changes.` and `changed=0` on every host; `SMOKE PASS`.

- [ ] **Step 2: Scrape targets and the stale restore test**

```bash
docker compose exec -T toolbox sh -c 'curl -s prometheus:9090/api/v1/targets | jq -r ".data.activeTargets[] | \"\(.labels.job) \(.labels.instance) \(.health)\""'
docker compose exec -T toolbox sh -c 'curl -s alertmanager:9093/api/v2/alerts | jq -r ".[].labels | \"\(.alertname) blocks_drills=\(.blocks_drills)\""'
```
Expected: `postgres db-a up`, `pgbackrest db-a up`, `pushgateway … up`. On a lab that never pushed an S6 (e.g. after `make destroy`), `RestoreTestStale blocks_drills=false` and nothing else.

- [ ] **Step 3: A drill pushes, and a push failure changes nothing**

```bash
make drill SCENARIO=s6-restore-test
docker compose exec -T toolbox sh -c 'curl -s pushgateway:9091/metrics | grep -E "^disavery_drill_(result|last_success).*s6-restore-test"'
make drill SCENARIO=s6-restore-test ARGS="--pushgateway http://pushgateway:9"; echo "exit $?"
```
Expected: `result PASS`; `disavery_drill_result{…result="PASS"…} 1` and a `disavery_drill_last_success_timestamp_seconds` line; against the closed port, `warning: result not pushed to http://pushgateway:9: … connection refused` and `exit 0`. Within a minute `RestoreTestStale` resolves. (Stopping the Pushgateway container does not test this: S6 provisions its restore node with `terraform apply`, which starts every stopped lab container again.)

- [ ] **Step 4: Grafana**

Open http://localhost:3000: the home dashboard "Drill history" shows the S6 run (`result`, `scenario`, `tier` columns only) and the time since it passed; "Backup health" shows both repositories' backup age and the WAL archive age under 45 s; "Replication" shows the standby's lag after `env reset --tier warm-standby` (switch back with `--tier pilot-light`).

- [ ] **Step 5: Seed and a CI-style programme locally**

```bash
docker compose exec -T toolbox build/disavery seed --size 500MB
scripts/drill-programme.sh nightly; echo "exit $?"
```
Expected: `ballast holds 500 MB`, a full backup; S6 and the day's scenario `PASS`, exit 0.

- [ ] **Step 6: The workflow on GitHub**

`workflow_dispatch` only works once the workflow is on `main`, so prove it on the branch with a temporary commit. In `.github/workflows/drills.yml`, add under `on:`
```yaml
  push: # TEMPORARY: proves the workflow before merge; reverted below
    branches: [feat/m5-monitoring-ci-drills]
```
and make the history branch `${{ inputs.history_branch || (github.event_name == 'push' && 'drill-history-test') || 'drill-history' }}`. Commit, push, and wait for the run:

```bash
git commit -am "ci: TEMPORARY run drills on push" && git push -u origin feat/m5-monitoring-ci-drills
gh run watch "$(gh run list --workflow drills --branch feat/m5-monitoring-ci-drills --limit 1 --json databaseId --jq '.[0].databaseId')" --exit-status
git fetch origin drill-history-test && git show origin/drill-history-test:badge.json
```
Expected: every step green; artifact `drill-reports-<id>`; `badge.json` says `PASS · <today> … UTC`; the branch README holds the trend tables. Then undo both:

```bash
git revert --no-edit HEAD && git push
git push origin --delete drill-history-test
```

- [ ] **Step 7: Final verification and PR**

```bash
go vet ./... && go test -race ./... && make lint && make test-alerts && shellcheck scripts/*.sh
docker compose exec -T toolbox bash -c 'terraform fmt -check -recursive infra/terraform && cd infra/ansible && ansible-lint playbooks roles'
git push -u origin feat/m5-monitoring-ci-drills
```
Expected: all green. Open a PR to `main`; CI must be green before merging. After the merge, trigger `drills` once by hand (programme `nightly`) so the badge exists before the first night.
