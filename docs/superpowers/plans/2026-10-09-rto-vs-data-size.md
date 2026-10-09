# RTO vs. Data Size Study Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Answer, with measurements, how recovery time grows with the amount of data: site loss (S1) and failback (S7) on both DR tiers, at 1, 5 and 10 GB, three runs each, reproducible with one command (`scripts/drill-programme.sh scaling`) and summarised by `disavery report scaling` (spec §14, first follow-up after v1).

**Architecture:** Every drill records the production database's size with its result (`data_bytes` in `report.json` and `history.jsonl`), so any history can be grouped by size; no separate study format. `disavery seed` writes ballast that compresses like application data, so backups and WAL are realistic in size. A new programme in `drill-programme.sh` grows the database step by step and runs the drills at each size; `report scaling` turns the history into a median-per-size table. The study's raw history is committed as a sample, so the table can be regenerated and checked.

**Tech Stack:** Go 1.26 (no new dependencies), PostgreSQL 16 (`pg_database_size`, TOAST storage `EXTERNAL`), pgBackRest 2.59 (zstd, asynchronous archiving), Ansible, Bash.

**Spec:** `docs/superpowers/specs/2026-10-07-disavery-dr-design.md` (§14 follow-ups; §6 measurement; §8 evidence).

## Roadmap

v1 (plans 1–5) shipped with PR #11; PR #12 stopped counting refused drills. This is the first of the post-v1 follow-ups in spec §14; the others (cloud profile, fencing and DNS TTL, logical backups, more exporters) get their own plans.

## How this plan was verified

Every file in this plan was written and run on the prototype branch `wip/scaling-prototype` (2026-10-09): `go vet`, `go test -race ./...`, `golangci-lint` (0 issues), `ansible-lint`, `shellcheck`. The study itself ran there, on a lab rebuilt from scratch for it (`make destroy`, `make up`, `SMOKE PASS`), 16 CPUs and 16 GB RAM under Docker Desktop/WSL2:

| Size | S1 pilot light RTO | S1 warm standby RTO | S7 outage (PL / WS) | S1 RPO (PL / WS) |
|---|---|---|---|---|
| 75 MB (fresh lab, 1 run) | 2 m 51 s | 1 m 1 s | 6 s / 5 s | 5 s / 0.4 s |
| 1 GB | 3 m 0 s | 1 m 0 s | 7 s / 8 s | 19 s / 0.3 s |
| 5 GB | 3 m 25 s | 1 m 0 s | 8.5 s / 9.5 s | 16 s / 0.9 s |
| 10 GB | 4 m 15 s | 1 m 1 s | 6.5 s / 9 s | 18 s / 0.5 s |

Medians of three runs (all `PASS`; the 5 GB warm-standby failback has two, see finding 1). The full table, with drill durations, is in `docs/scaling.md` (Task 5). The study took about four hours and peaked at about 55 GB of the host's disk; the lab was destroyed and rebuilt afterwards.

The study found four problems, all fixed in this plan:

1. **Warm-standby failback broke on an archive lock (5 GB, third run).** S7 re-runs `site.yml`, whose pgBackRest role calls `stanza-create` on the DR primary. That needs the archive lock, which the asynchronous `archive-push` holds while it ships a WAL backlog; `stanza-create` failed at once with error 50 ("unable to acquire lock … is another pgBackRest process running?") instead of waiting. The larger the database, the longer the backlog and the likelier the collision. The role now waits for the lock (Task 4).
2. **A rejoining standby was given one minute.** After a failback the old primary replays from the vault everything site b wrote meanwhile, then streams. The replica role waited 60 s for streaming whatever the backlog; with gigabytes to replay it gave up while replay was progressing steadily. It now waits as long as replay advances and fails only after 60 s without progress (Task 4).
3. **The programme seeded the wrong primary.** After the failed failback the lab stayed in site b, `env reset` refused, and the next size was seeded into db-b, whose seed backup then failed. Each size now starts with a successful reset to the pilot-light baseline, or the study stops (Task 4).
4. **The random restore test does not measure size.** The prototype ran S6 at every size too. S6 restores a randomly chosen backup set to a random moment; at "10 GB" one run restored the morning's 75 MB set, and S6's median jumped from 1 m 18 s to 4 m 47 s for reasons unrelated to today's size (the set chosen, the WAL replayed). `report scaling` leaves S6 out by default (`--exclude`), and the programme no longer runs it (Tasks 3 and 4). Its runs stay in the sample history.

One run is left out of the published history: the failback that completed finding 1's broken run by hand, which started half-way and is not comparable. The two failed runs stay (they are the evidence for findings 1 and 2); `report scaling` does not count them.

## Decisions and spec clarifications

Decided with the user while planning:

1. **Sizes 1, 5 and 10 GB.** Spec §14's 50 GB does not fit: Docker's disk lives on the host's C: drive (87 GB free), and every size exists about five times at once (db-a, repo1, the vault, the restore node or site b, the warm standby). A pilot at 75 MB (a fresh lab) gives the size-independent baseline.
2. **Scenarios:** S1 and S7 on pilot light (rebuild from the vault, size-dependent by design); S1 and S7 on warm standby as the control (promotion should not depend on size). The user also chose S6; the prototype showed it does not measure size (finding 4), so it is left out.
3. **Three runs per size and scenario; medians.** Local only: CI runners lack the disk.
4. **A fresh lab, destroyed again afterwards.** Everything written to the vault is locked for 14 days (compliance mode), so the study starts from `make destroy` + `make up` and ends with them.

Clarifications — raise if you disagree:

5. **Ballast compresses about fourfold.** The v1 seed wrote random bytes, which neither TOAST nor zstd can shrink: backups and WAL as large as the database, the worst case. Real application data compresses; the vault would also have held about 80 GB of locked data for this study. Each 8 KiB ballast row is now 2 KiB of random bytes and 6 KiB of zeros, stored with `STORAGE EXTERNAL` (no TOAST compression): the database holds the full size, pgBackRest's zstd shrinks backups and WAL about fourfold. This also changes the nightly CI seed (500 MB), which becomes lighter.
6. **Size is recorded, not configured.** Every drill measures `pg_database_size('docsvc')` after preflight and records it (`data_bytes`); `report scaling` groups by size rounded to the nearest GB. Any history works, including the nightly one; no study mode in the drill engine.
7. **What `report scaling` counts:** recovered runs only (`PASS`, `MISSED_TARGET`) with a recorded size; refused, failed and errored runs do not have comparable timings. For each scenario and tier it shows the median of every measure and of the drill's duration, with the number of runs. Scenarios in `--exclude` (default `s6-restore-test`) are left out.
8. **Out of scope:** 50 GB (decision 1); restore throughput tuning (`process-max`, parallel restore), which would answer a different question; repeated `restore_command` lines in `postgresql.auto.conf` after many failbacks (harmless, the last wins), noted for a later cleanup.

## Global Constraints

All plans before still hold. Additions:

- `report.json` and `history.jsonl` gain `data_bytes` (omitted when unknown); `report.md` shows "database N GB" in the run line.
- `disavery report scaling [--history F] [--exclude S,...]` (default excludes `s6-restore-test`); `scripts/drill-programme.sh scaling` with `SCALING_SIZES` (default `1GB 5GB 10GB`) and `SCALING_RUNS` (default 3).
- The study's sample: `reports/samples/scaling/history.jsonl` and `docs/scaling.md` (its table is `report scaling --history reports/samples/scaling/history.jsonl`).

## Review Focus

1. **A drill never fails because its size could not be measured**: an error prints "data size unknown" and the drill goes on. Pinned in Task 1.
2. **The table counts only comparable runs** (recovered, sized, not refused, not S6). Pinned in Task 3 (`TestScaling`, `TestReportScalingExcludesRestoreTests`).
3. **Waiting is bounded by progress, not by size**: the archive-lock wait retries only on error 50, at most 10 minutes; the standby wait fails after 60 s without replay progress. Pinned in Task 4 and by the 10 GB failbacks.
4. **The published table is reproducible** from the committed history. Pinned in Task 5.
5. **The lab is left clean**: destroyed and rebuilt after the study, so 14-day-locked vault data does not linger. Pinned in Task 6.

---

### Task 1: Every result records the data size

**Files:**
- Modify: `internal/drill/drill.go`, `internal/drill/labenv.go`, `internal/report/report.go`, `internal/report/history.go`, `internal/report/markdown.go`, `internal/report/report.md.tmpl`
- Test: `internal/drill/drill_test.go`

**Interfaces:**
- Produces: `Lab.DataSize(ctx) (int64, error)`; `Report.DataBytes`, `HistoryEntry.DataBytes` (`data_bytes`); `report.FormatBytes(n) string`; `(*LabEnv).queryInt`.

- [ ] **Step 1: Create the branch**

```bash
git switch main && git pull && git switch -c feat/rto-vs-data-size
```

- [ ] **Step 2: Write the failing test**

`internal/drill/drill_test.go`: `fakeLab` gains a `dataSize int64` field and
```go
func (f *fakeLab) DataSize(context.Context) (int64, error)         { return f.dataSize, nil }
```
`TestRestoreTestPasses` builds its lab with `dataSize: 5 << 30` and checks `r.DataBytes != 5<<30` in its first condition and `h[0].DataBytes != 5<<30` in the history check.

```bash
go test ./internal/drill/
```
Expected: compile errors (`DataBytes` unknown).

- [ ] **Step 3: Implement**

`internal/drill/drill.go`, in `Lab` after `DBHost`:
```go
	// DataSize is the production database's size in bytes, recorded with the
	// result so recovery times can be compared across data sizes.
	DataSize(ctx context.Context) (int64, error)
```
and right after the preflight block that returns a refused drill:
```go
	if n, err := l.DataSize(ctx); err != nil {
		fmt.Fprintf(o.Out, "  data size unknown: %v\n", err)
	} else {
		r.DataBytes = n
		fmt.Fprintf(o.Out, "  data size        %s\n", report.FormatBytes(n))
	}
```

`internal/report/report.go`, after `Refused`:
```go
	DataBytes     int64                  `json:"data_bytes,omitempty"` // production database size when the drill started
```
`internal/report/history.go`: the same field in `HistoryEntry` (comment `// see Report.DataBytes`), set in `HistoryEntry()` with `DataBytes: r.DataBytes`.

`internal/report/markdown.go`: `"bytes": FormatBytes,` in `funcs`, and
```go
// FormatBytes renders a size in binary units: "512 MB", "10.0 GB".
func FormatBytes(n int64) string {
	if n >= 1<<30 {
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	}
	return fmt.Sprintf("%d MB", (n+1<<19)>>20)
}
```
`internal/report/report.md.tmpl`, the run line:
```
Run `{{.RunID}}`{{with .IncidentAt}} · incident at {{stamp .}} UTC{{end}}{{with .DataBytes}} · database {{bytes .}}{{end}}
```

`internal/drill/labenv.go`, after `Seed` (Task 2 rewrites `Seed` to use `queryInt` too):
```go
// DataSize implements Lab: the production database's size in bytes.
func (l *LabEnv) DataSize(ctx context.Context) (int64, error) {
	host, err := l.DBHost()
	if err != nil {
		return 0, err
	}
	return l.queryInt(ctx, host, "SELECT pg_database_size('docsvc')")
}

// queryInt runs a query that returns one integer in the docsvc database.
func (l *LabEnv) queryInt(ctx context.Context, host, query string) (int64, error) {
	rows, err := l.psql("docsvc").Query(ctx, host, query)
	if err != nil {
		return 0, err
	}
	if len(rows) != 1 || len(rows[0]) != 1 {
		return 0, fmt.Errorf("unexpected result %v", rows)
	}
	return strconv.ParseInt(rows[0][0], 10, 64)
}
```

- [ ] **Step 4: Run the tests**

```bash
go test -race ./internal/drill/ ./internal/report/ ./cmd/disavery/ && go vet ./...
```
Expected: `ok` (the report golden files are unchanged: the size only shows when known).

- [ ] **Step 5: Commit**

```bash
git add internal
git commit -m "feat: record the database size with every drill result"
```

---

### Task 2: Ballast that compresses like application data

**Files:**
- Modify: `internal/drill/labenv.go`

- [ ] **Step 1: Rewrite `Seed`**

`internal/drill/labenv.go`, the comment, `seedBatch` and `Seed` become:
```go
// seedBatch is how much ballast one statement inserts: 8,192 rows of 8 KiB.
const seedBatch = 8192 * 8192

// Seed grows the table ballast in the production database to at least size
// bytes, then takes a full backup to both repositories, so restores in drills
// move a realistic amount of data (spec §12, §14). Each 8 KiB row is 2 KiB of
// random bytes and 6 KiB of zeros, stored without TOAST compression: the
// database holds the full size, while pgBackRest's zstd shrinks backups and
// WAL about fourfold, as it would for typical application data. The
// application never reads ballast. It returns the table size.
func (l *LabEnv) Seed(ctx context.Context, size int64, out io.Writer) (int64, error) {
	host, err := l.DBHost()
	if err != nil {
		return 0, err
	}
	sql := l.psql("docsvc")
	if _, err := sql.Query(ctx, host, `CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE TABLE IF NOT EXISTS ballast (id bigint PRIMARY KEY, payload bytea NOT NULL);
ALTER TABLE ballast ALTER COLUMN payload SET STORAGE EXTERNAL`); err != nil {
		return 0, err
	}
	for {
		have, err := l.queryInt(ctx, host, "SELECT pg_total_relation_size('ballast')")
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
		if _, err := sql.Query(ctx, host, `INSERT INTO ballast
SELECT coalesce((SELECT max(id) FROM ballast), 0) + g,
  gen_random_bytes(1024) || gen_random_bytes(1024) || decode(repeat('00', 6144), 'hex')
FROM generate_series(1, `+strconv.Itoa(seedBatch/8192)+`) g`); err != nil {
			return have, err
		}
	}
}

// Seed grows the table ballast in the production database to at least size
// bytes, then takes a full backup to both repositories, so restores in drills
// move a realistic amount of data (spec §12, §14). Each 8 KiB row is 2 KiB of
// random bytes and 6 KiB of zeros, stored without TOAST compression: the
// database holds the full size, while pgBackRest's zstd shrinks backups and
// WAL about fourfold, as it would for typical application data. The
// application never reads ballast. It returns the table size.
func (l *LabEnv) Seed(ctx context.Context, size int64, out io.Writer) (int64, error) {
	host, err := l.DBHost()
	if err != nil {
		return 0, err
	}
	sql := l.psql("docsvc")
	if _, err := sql.Query(ctx, host, `CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE TABLE IF NOT EXISTS ballast (id bigint PRIMARY KEY, payload bytea NOT NULL);
ALTER TABLE ballast ALTER COLUMN payload SET STORAGE EXTERNAL`); err != nil {
		return 0, err
	}
	for {
		have, err := l.queryInt(ctx, host, "SELECT pg_total_relation_size('ballast')")
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
		if _, err := sql.Query(ctx, host, `INSERT INTO ballast
SELECT coalesce((SELECT max(id) FROM ballast), 0) + g,
  gen_random_bytes(1024) || gen_random_bytes(1024) || decode(repeat('00', 6144), 'hex')
FROM generate_series(1, `+strconv.Itoa(seedBatch/8192)+`) g`); err != nil {
			return have, err
		}
	}
}
```

- [ ] **Step 2: Check and commit**

```bash
go vet ./... && go test ./internal/drill/ && make lint
git add internal/drill/labenv.go
git commit -m "feat: seed ballast that compresses like application data"
```
Expected: `ok`, 0 lint issues. (The lab check is in Task 6: `ballast holds … MB`, and a full backup about a quarter of the database.)

---

### Task 3: `disavery report scaling`

**Files:**
- Create: `internal/report/scaling.go`, `internal/report/scaling_test.go`
- Modify: `cmd/disavery/main.go`

**Interfaces:**
- Produces: `report.ScalingRow`, `report.Scaling(entries) ([]int, []ScalingRow)`, `report.RenderScaling(sizes, rows) string`; CLI `report scaling [--history F] [--exclude S,...]`.

- [ ] **Step 1: Write the failing test**

`internal/report/scaling_test.go`:
```go
package report_test

import (
	"strings"
	"testing"

	"github.com/oplosy/disavery/internal/report"
)

// TestScaling: recovered runs are grouped by data size (nearest GB) and their
// measures summarised as medians; runs without a size, refused or failed runs
// do not count.
func TestScaling(t *testing.T) {
	const gb = 1 << 30
	run := func(size float64, rto, duration float64, result report.Result) report.HistoryEntry {
		return report.HistoryEntry{
			Scenario: "s1-site-loss", Tier: "pilot-light", Result: result, StartedAt: at(0),
			DataBytes: int64(size * gb), DurationSeconds: duration,
			Measurements: []report.Measurement{{Name: "rto", TargetSeconds: 900, ActualSeconds: rto}},
		}
	}
	entries := []report.HistoryEntry{
		run(1.04, 120, 300, report.Pass),
		run(0.98, 140, 310, report.Pass),
		run(1.01, 130, 320, report.MissedTarget),
		run(10.2, 600, 900, report.Pass),
		run(9.9, 9999, 9999, report.Failed),                            // did not recover
		{Scenario: "s1-site-loss", Tier: "pilot-light", Refused: true}, // never started
		run(0, 50, 60, report.Pass),                                    // no size recorded
	}
	table := report.RenderScaling(report.Scaling(entries))
	for _, want := range []string{
		"| Scenario | Tier | Measure | 1 GB | 10 GB |",
		"| s1-site-loss | pilot-light | drill duration | 5m10s (3) | 15m0s (1) |",
		"| s1-site-loss | pilot-light | rto | 2m10s (3) | 10m0s (1) |",
	} {
		if !strings.Contains(table, want) {
			t.Fatalf("missing %q in\n%s", want, table)
		}
	}
	if strings.Contains(table, "2h") || strings.Contains(table, "50s") {
		t.Fatalf("a failed or sizeless run was counted:\n%s", table)
	}
}
```

Append to `cmd/disavery/main_test.go`:
```go
// TestReportScalingExcludesRestoreTests: S6 restores a random backup set, not
// the current database, so by default it has no place in a table by size.
func TestReportScalingExcludesRestoreTests(t *testing.T) {
	history := filepath.Join(t.TempDir(), "history.jsonl")
	lines := `{"scenario":"s6-restore-test","result":"PASS","data_bytes":1073741824,"started_at":"2026-10-09T12:00:00Z","duration_seconds":60}
{"scenario":"s1-site-loss","tier":"pilot-light","result":"PASS","data_bytes":1073741824,"started_at":"2026-10-09T12:00:00Z","duration_seconds":200}
`
	if err := os.WriteFile(history, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runCLI("report", "scaling", "--history", history)
	if code != 0 || strings.Contains(out, "s6-restore-test") || !strings.Contains(out, "| s1-site-loss | pilot-light | drill duration | 3m20s (1) |") {
		t.Fatalf("exit %d\n%s%s", code, out, errOut)
	}
	if code, out, _ := runCLI("report", "scaling", "--history", history, "--exclude", ""); code != 0 || !strings.Contains(out, "s6-restore-test") {
		t.Fatalf("--exclude '': exit %d\n%s", code, out)
	}
}
```

```bash
go test ./internal/report/ ./cmd/disavery/
```
Expected: compile errors (`report.Scaling`, `report.RenderScaling` undefined); after Step 2's `internal/report` alone, `TestReportScalingExcludesRestoreTests` fails (unknown command).

- [ ] **Step 2: Implement**

`internal/report/scaling.go`:
```go
package report

import (
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"time"
)

// durationMeasure is the pseudo-measure for a drill's wall-clock duration.
const durationMeasure = "drill duration"

// ScalingRow is one measure of one scenario and tier across data sizes.
type ScalingRow struct {
	Scenario, Tier, Measure string
	Median                  map[int]time.Duration // by data size in GB
	Runs                    map[int]int
}

// Scaling summarises recovered runs by data size, rounded to the nearest GB
// (spec §14, RTO vs. data size): the median of every measure and of the
// drill's duration. Runs without a recorded size, refused runs and runs that
// did not recover (FAILED, ERROR) are left out. It returns the sizes found,
// ascending, and the rows.
func Scaling(entries []HistoryEntry) ([]int, []ScalingRow) {
	type key struct{ scenario, tier, measure string }
	values := map[key]map[int][]float64{}
	sizes := map[int]bool{}
	add := func(k key, size int, v float64) {
		if values[k] == nil {
			values[k] = map[int][]float64{}
		}
		values[k][size] = append(values[k][size], v)
	}
	for _, e := range entries {
		if e.DataBytes <= 0 || e.Refused || (e.Result != Pass && e.Result != MissedTarget) {
			continue
		}
		size := int(math.Round(float64(e.DataBytes) / (1 << 30)))
		sizes[size] = true
		add(key{e.Scenario, e.Tier, durationMeasure}, size, e.DurationSeconds)
		for _, m := range e.Measurements {
			add(key{e.Scenario, e.Tier, m.Name}, size, m.ActualSeconds)
		}
	}
	var rows []ScalingRow
	for k, bySize := range values {
		row := ScalingRow{Scenario: k.scenario, Tier: k.tier, Measure: k.measure,
			Median: map[int]time.Duration{}, Runs: map[int]int{}}
		for size, vs := range bySize {
			slices.Sort(vs)
			row.Median[size], row.Runs[size] = seconds(vs[len(vs)/2]), len(vs)
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Scenario != b.Scenario {
			return a.Scenario < b.Scenario
		}
		if a.Tier != b.Tier {
			return a.Tier < b.Tier
		}
		return a.Measure < b.Measure
	})
	out := make([]int, 0, len(sizes))
	for s := range sizes {
		out = append(out, s)
	}
	slices.Sort(out)
	return out, rows
}

// RenderScaling renders Scaling's result as a Markdown table: one column per
// data size, each cell the median and, in parentheses, the number of runs.
func RenderScaling(sizes []int, rows []ScalingRow) string {
	var b strings.Builder
	b.WriteString("| Scenario | Tier | Measure |")
	for _, s := range sizes {
		if s == 0 {
			b.WriteString(" < 0.5 GB |")
		} else {
			fmt.Fprintf(&b, " %d GB |", s)
		}
	}
	b.WriteString("\n|---|---|---|" + strings.Repeat("---|", len(sizes)) + "\n")
	for _, r := range rows {
		tier := r.Tier
		if tier == "" {
			tier = "—"
		}
		fmt.Fprintf(&b, "| %s | %s | %s |", r.Scenario, tier, r.Measure)
		for _, s := range sizes {
			if n := r.Runs[s]; n > 0 {
				fmt.Fprintf(&b, " %s (%d) |", FormatDuration(r.Median[s]), n)
			} else {
				b.WriteString(" — |")
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}
```

`cmd/disavery/main.go` (imports gain `slices`): the usage line after `report badge`
```
  report scaling [--exclude S,...]         recovery times by data size (spec §14), without S6 by default
```
`|| args[1] == "scaling"` in the `report` sub-command condition of the dispatcher, and before `case "report tiers":`
```go
	case "report scaling":
		history := fs.String("history", "", "history file (default <reports>/history.jsonl)")
		// S6 restores a random backup set to a random point: what it restores
		// is not the database size recorded with the run.
		exclude := fs.String("exclude", "s6-restore-test", "comma-separated scenarios to leave out")
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
		skip := strings.Split(*exclude, ",")
		entries = slices.DeleteFunc(entries, func(e report.HistoryEntry) bool { return slices.Contains(skip, e.Scenario) })
		fmt.Fprint(stdout, report.RenderScaling(report.Scaling(entries)))
		return 0
```

- [ ] **Step 3: Run the tests and commit**

```bash
go test -race ./internal/report/ ./cmd/disavery/ && go vet ./... && make lint
git add internal/report cmd/disavery
git commit -m "feat: summarise recovery times by data size"
```
Expected: `ok`; 0 issues.

---

### Task 4: Failback at size, and the `scaling` programme

**Files:**
- Modify: `infra/ansible/roles/pgbackrest/tasks/main.yml`, `infra/ansible/roles/replica/tasks/main.yml`, `scripts/drill-programme.sh`

- [ ] **Step 1: Wait for the archive lock**

`infra/ansible/roles/pgbackrest/tasks/main.yml`, the stanza task becomes:
```yaml
# stanza-create needs the archive lock, which the asynchronous archive-push
# holds while it ships a WAL backlog (minutes on a promoted standby with a few
# GB of data). It fails at once with error 50 instead of waiting, so wait here.
- name: Create the stanza (idempotent)
  ansible.builtin.command: pgbackrest --stanza={{ pgbackrest_stanza }} stanza-create
  become: true
  become_user: postgres
  register: pgbackrest_stanza_create
  until: pgbackrest_stanza_create.rc != 50
  retries: 60
  delay: 10
  changed_when: false
```

- [ ] **Step 2: Wait for a rejoining standby while it makes progress**

`infra/ansible/roles/replica/tasks/main.yml`, the streaming wait becomes:
```yaml
# A rejoining standby first replays from the archive everything the primary
# wrote while it was away (gigabytes after a failback with a large database),
# then streams. Wait as long as replay advances; give up after 60 s without
# progress, so a stuck standby still fails.
- name: Wait until the standby streams from the primary
  ansible.builtin.shell: |
    set -eu
    last=""; idle=0
    until [ "$(psql -XAtc 'SELECT status FROM pg_stat_wal_receiver')" = streaming ]; do
      lsn=$(psql -XAtc 'SELECT pg_last_wal_replay_lsn()')
      if [ "$lsn" = "$last" ]; then
        idle=$((idle + 1))
        if [ "$idle" -ge 30 ]; then
          echo "replay stuck at $lsn for 60 s" >&2
          exit 1
        fi
      else
        idle=0; last=$lsn
      fi
      sleep 2
    done
  args:
    executable: /bin/bash
  become: true
  become_user: postgres
  changed_when: false
```

- [ ] **Step 3: The `scaling` programme**

`scripts/drill-programme.sh`: in the header, after the `weekly` line,
```bash
#   scaling  RTO vs. data size (spec §14), locally: for each size in
#            SCALING_SIZES (default "1GB 5GB 10GB") seed the database up to it,
#            then run S1 + S7 on both tiers SCALING_RUNS times (default 3).
#            Not S6: it restores a random backup set, not the current size.
#            Summarise with `disavery report scaling`.
```
the usage line becomes `# Usage: scripts/drill-programme.sh nightly|weekly|scaling`, and before the `*)` case:
```bash
  scaling)
    for size in ${SCALING_SIZES:-1GB 5GB 10GB}; do
      # Every size starts from the pilot-light baseline; a lab stuck in site b
      # would seed the wrong primary and measure nothing comparable.
      echo "== seed: $size"
      if ! tb build/disavery env reset --tier pilot-light || ! tb build/disavery seed --size "$size"; then
        worst=4
        break
      fi
      for run in $(seq 1 "${SCALING_RUNS:-3}"); do
        echo "== $size, run $run"
        scenario s1-pilot-light
        scenario s1-warm-standby
      done
    done
    ;;
```
with `nightly|weekly|scaling` in the usage message of `*)`.

- [ ] **Step 4: Check and commit**

```bash
docker compose exec -T toolbox bash -c 'cd infra/ansible && ansible-lint playbooks roles'
shellcheck scripts/*.sh
git add infra/ansible/roles scripts/drill-programme.sh
git commit -m "fix: let failback wait for the archive lock and a standby's catch-up"
```
Expected: `Passed`; shellcheck silent.

---

### Task 5: Results, ADR and docs

**Files:**
- Create: `docs/scaling.md`, `docs/adr/0011-recovery-time-by-data-size.md`, `reports/samples/scaling/history.jsonl`
- Modify: `README.md`, `docs/superpowers/specs/2026-10-07-disavery-dr-design.md`, `CLAUDE.md`

- [ ] **Step 1: The study's history**

Copy the prototype study's lines (those with `data_bytes`) from its `reports/history.jsonl`, without the run named in "How this plan was verified" (`20261009T135804Z-s7-failback-warm-standby`), to `reports/samples/scaling/history.jsonl`, and check the table:

```bash
docker compose exec -T toolbox build/disavery report scaling --history reports/samples/scaling/history.jsonl
```
Expected: the table of `docs/scaling.md`, line for line.

- [ ] **Step 2: Write the docs**

`docs/scaling.md`:
````markdown
# Recovery time by data size

How does recovering the service scale with the amount of data? Site loss (S1)
and failback (S7) on both DR tiers, measured at 1, 5 and 10 GB, three runs
each, on a lab built from scratch for the study (2026-10-09). The decision
behind the method is [ADR 0011](adr/0011-recovery-time-by-data-size.md).

## Results

Medians, with the number of runs in parentheses. `< 0.5 GB` is a fresh lab
(75 MB), measured once as the baseline. Reproduce the table from the
committed history:

```bash
docker compose exec toolbox build/disavery report scaling --history reports/samples/scaling/history.jsonl
```

| Scenario | Tier | Measure | < 0.5 GB | 1 GB | 5 GB | 10 GB |
|---|---|---|---|---|---|---|
| s1-site-loss | pilot-light | drill duration | 3m6s (1) | 3m18s (3) | 4m5s (3) | 4m53s (3) |
| s1-site-loss | pilot-light | rpo | 5.23s (1) | 19s (3) | 16s (3) | 18s (3) |
| s1-site-loss | pilot-light | rto | 2m51s (1) | 3m0s (3) | 3m25s (3) | 4m15s (3) |
| s1-site-loss | warm-standby | drill duration | 1m22s (1) | 1m24s (3) | 1m55s (3) | 2m38s (3) |
| s1-site-loss | warm-standby | rpo | 410ms (1) | 270ms (3) | 920ms (3) | 450ms (3) |
| s1-site-loss | warm-standby | rto | 1m1s (1) | 1m0s (3) | 1m0s (3) | 1m1s (3) |
| s7-failback | pilot-light | downtime | 6.01s (1) | 7s (3) | 8.5s (3) | 6.5s (3) |
| s7-failback | pilot-light | drill duration | 2m46s (1) | 3m25s (3) | 4m59s (3) | 4m57s (3) |
| s7-failback | pilot-light | rpo | 0s (1) | 0s (3) | 0s (3) | 0s (3) |
| s7-failback | warm-standby | downtime | 5s (1) | 8s (3) | 9.5s (2) | 9.01s (3) |
| s7-failback | warm-standby | drill duration | 3m9s (1) | 3m52s (3) | 5m58s (2) | 6m52s (3) |
| s7-failback | warm-standby | rpo | 0s (1) | 0s (3) | 0s (2) | 0s (3) |

Every one of these runs passed. The 5 GB warm-standby failback has two runs:
its third failed, which is how finding 1 below was found.

## What it means

- **Pilot light pays for data at recovery time; warm standby does not.**
  Pilot light's RTO is a fixed 2 m 50 s (detection 20 s, the decision 30 s,
  provisioning and configuring site b) plus 6–10 s per GB to restore from the
  vault: 4 m 15 s at 10 GB. Warm standby's RTO stays at 1 minute from 75 MB to
  10 GB, because the data is already there. The tier comparison
  ([sample](../reports/samples/tier-comparison.md)) widens with size.
- **Where pilot light's target breaks, roughly.** At 6–10 s per GB beyond
  10 GB, the 15-minute target is crossed somewhere between 75 and 120 GB
  (about 90 GB on a straight line through 1–10 GB). That is an
  extrapolation from 10 GB on one machine, not a measurement; the cloud profile
  (spec §14) is where to measure it.
- **Data size does not change data loss.** RPO is set by the archive cadence
  (`archive_timeout` 30 s) and the replication stream, not by how much data
  there is: from 1 to 10 GB pilot light loses 16–19 s, warm standby under a
  second.
- **Failback gets longer, not the outage.** The users' outage during S7 stays
  at 5–10 s. What grows is everything around it: the old primary catching up
  (finding 2), rebuilding the standby, and verification — `pg_amcheck` checks
  every page, 41–80 s at 10 GB.

## What the study found

1. **Warm-standby failback broke on an archive lock** (5 GB, third run).
   Failback re-runs the site playbook, whose pgBackRest role calls
   `stanza-create` on the DR primary. That needs the archive lock, held by the
   asynchronous `archive-push` while it ships a WAL backlog; `stanza-create`
   failed at once with error 50 instead of waiting. More data, longer backlog,
   likelier collision. The role now waits for the lock.
2. **A rejoining standby was given one minute.** The old primary first replays
   from the vault everything site b wrote meanwhile, then streams; the role
   gave up after 60 s while replay was progressing. It now waits as long as
   replay advances and fails after 60 s without progress.
3. **The study seeded the wrong primary after a failure.** With the lab left in
   site b, the next size went into db-b. Each size now starts from a verified
   pilot-light baseline, or the study stops.
4. **The random restore test does not measure size.** S6 restores a randomly
   chosen backup set to a random moment: at "10 GB" one run restored the
   morning's 75 MB backup. Its time depends on the set and the WAL replayed,
   not on today's database, so it is left out of this study
   (`report scaling --exclude`); pilot light's rebuild is the restore that
   scales with the current size.

## Method

- Ballast rows (`disavery seed`) are 2 KiB of random bytes and 6 KiB of zeros,
  stored without TOAST compression: the database holds the full size, while
  backups and WAL compress about fourfold with zstd, as typical application
  data would. The application never reads the ballast.
- `scripts/drill-programme.sh scaling` resets the lab to pilot light, seeds up
  to each size (a full backup to both repositories follows), then runs S1 and
  S7 on pilot light and on warm standby, three times.
- Every drill records the database size with its result (`data_bytes`);
  `disavery report scaling` groups recovered runs by size, rounded to the
  nearest GB.
- One run is left out of the published history: the failback that completed
  finding 1's broken run by hand, which started half-way and is not comparable.
- 50 GB, the spec's largest size, did not fit: every size exists about five
  times at once (production, both repositories, site b or the warm standby),
  and the vault keeps everything for 14 days. The study peaked at about 55 GB
  of the host's disk.
````

`docs/adr/0011-recovery-time-by-data-size.md`:
```markdown
# ADR 0011: Recovery time by data size

- Status: accepted
- Date: 2026-10-09

## Context
v1 measured every recovery on a database of a few hundred megabytes. Whether
the targets still hold for a larger database was an open question (spec §14),
and pilot light and warm standby were expected to differ most there: one
restores the data at recovery time, the other keeps a copy streaming.

Two constraints shaped the study. The lab runs on one machine with 87 GB of
free disk, and every gigabyte of data exists about five times at once
(production, both repositories, site b or the warm standby). The vault keeps
every backup and WAL segment for 14 days in compliance mode, so nothing the
study writes there can be cleaned up until then.

## Decision
- **Every drill records the database size** (`data_bytes`, measured after
  preflight). A size study is then a grouping of ordinary drill history
  (`disavery report scaling`), not a separate mode of the drill engine.
- **Sizes 1, 5 and 10 GB**, three runs each, on a lab built for the study and
  destroyed after it. 50 GB does not fit the disk.
- **Ballast compresses like application data**, about fourfold with zstd:
  each 8 KiB row is 2 KiB of random bytes and 6 KiB of zeros, without TOAST
  compression. Random bytes, the v1 seed, are the worst case for backups and
  would have locked about 80 GB in the vault.
- **S1 and S7 on both tiers; not S6.** The random restore test restores a
  randomly chosen backup set, whose size has nothing to do with today's
  database (at "10 GB" one run restored a 75 MB set). Pilot light's rebuild is
  the restore of the current data.
- **Waits are bounded by progress, not by size.** Failing at size, the drills
  showed two fixed limits that were really about data volume: `stanza-create`
  gave up at once on an archive lock held by a WAL backlog, and a rejoining
  standby was given one minute to catch up. The first now retries on lock
  errors for up to ten minutes; the second waits while replay advances and
  fails after 60 s without progress.

## Consequences
- The answer is in [docs/scaling.md](../scaling.md): pilot light's RTO grows by
  6–10 s per GB on top of a fixed 2 m 50 s; warm standby's stays at one minute;
  data loss and the users' outage during failback do not change with size.
- Any history with sizes can be summarised the same way, including nightly CI
  runs (500 MB) and a future cloud profile.
- Repeating the study takes about four hours and a fresh lab; results depend on
  the machine, so they are evidence for this lab, not a sizing rule.
```

`README.md`: a section "Recovery time by data size" after "Continuous drills":
````markdown
## Recovery time by data size

Measured at 1, 5 and 10 GB, three runs each ([details](docs/scaling.md)):
pilot light's RTO grows from 3 m 0 s to 4 m 15 s, about 6–10 s per GB on top of
a fixed 2 m 50 s, because it restores the data at recovery time; warm standby
stays at 1 minute at every size. Data loss and the users' outage during
failback do not change with size.

```bash
scripts/drill-programme.sh scaling                       # about four hours, on a fresh lab with ~60 GB free
docker compose exec toolbox build/disavery report scaling
```
````
and in "What the drills found", one more line: "a warm-standby failback that gave up on a large database while it was still catching up ([ADR 0011](docs/adr/0011-recovery-time-by-data-size.md));".

Spec §14: "RTO vs. data size study (1 / 10 / 50 GB)." becomes "RTO vs. data size study: done for 1 / 5 / 10 GB ([docs/scaling.md](../../scaling.md)); 50 GB needs more disk than the local lab has."

`CLAUDE.md`: this plan becomes the current plan; Status: "v1 complete (…); RTO vs. data size study shipped (PR #N)" once merged.

- [ ] **Step 3: Commit**

```bash
git add docs README.md CLAUDE.md reports/samples/scaling
git commit -m "docs: publish the RTO vs. data size study"
```

---

### Task 6: The lab

- [ ] **Step 1: Run the tooling end to end**

```bash
make up && make smoke
SCALING_SIZES=16MB SCALING_RUNS=1 scripts/drill-programme.sh scaling
docker compose exec -T toolbox build/disavery report scaling
```
Expected: `SMOKE PASS`; every drill `PASS` with a `data size` line; a table with a `< 0.5 GB` column. (The full study takes about four hours and ran on the prototype; repeat it only when the drills change: `scripts/drill-programme.sh scaling`, on a fresh lab, with about 60 GB free.)

- [ ] **Step 2: Final verification and PR**

```bash
go vet ./... && go test -race ./... && make lint && make test-alerts && shellcheck scripts/*.sh
git push -u origin feat/rto-vs-data-size
```
Expected: all green. Open a PR to `main`; CI must be green before merging.

- [ ] **Step 3: Leave the lab clean**

After a full study only: `make destroy && make up`, so the vault's 14-day-locked backups do not keep the disk.
