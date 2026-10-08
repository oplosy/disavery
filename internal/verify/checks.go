package verify

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/oplosy/disavery/internal/canary"
	"github.com/oplosy/disavery/internal/prober"
)

// Amcheck runs pg_amcheck (heap plus every index) on the docsvc database.
// Params: host.
type Amcheck struct {
	Remote Remote
	// Bin is the pg_amcheck path; Debian does not put it on PATH.
	Bin string
}

// Run implements Check.
func (a Amcheck) Run(ctx context.Context, p Params) (Result, error) {
	if err := p.Require("host"); err != nil {
		return Result{}, err
	}
	out, err := a.Remote.Run(ctx, p.With["host"], "runuser -u postgres -- "+a.Bin+" --heapallindexed --database=docsvc", nil)
	if report := strings.TrimSpace(out); report != "" {
		// pg_amcheck prints findings on stdout and exits non-zero.
		lines := strings.Split(report, "\n")
		return Result{Status: Fail, Summary: "pg_amcheck reported corruption", Details: details("findings", lines, 20)}, nil
	}
	if err != nil {
		return Result{}, err
	}
	return Result{Status: Pass, Summary: "pg_amcheck found no corruption in docsvc (heap and all indexes)"}, nil
}

// Rules checks business rules and row counts. Params: host; optional as_of
// (a restore target), tolerance (default 1s) and compare_host (production
// database). With as_of, the restore must hold no document created after it,
// and exactly as many documents created before it as compare_host does.
type Rules struct {
	SQL SQL
}

const rulesQuery = `SELECT
  (SELECT count(*) FROM documents),
  (SELECT count(*) FROM canary),
  (SELECT count(*) FROM documents
    WHERE attachment_key <> 'documents/' || id::text
       OR updated_at < created_at
       OR payment_status NOT IN ('pending', 'paid', 'failed'))`

// Run implements Check.
func (r Rules) Run(ctx context.Context, p Params) (Result, error) {
	if err := p.Require("host"); err != nil {
		return Result{}, err
	}
	host := p.With["host"]
	counts, err := r.ints(ctx, host, rulesQuery, 3)
	if err != nil {
		return Result{}, err
	}
	res := Result{Status: Pass, Metrics: map[string]float64{
		"documents": float64(counts[0]), "canary_writes": float64(counts[1]), "rule_violations": float64(counts[2]),
	}}
	var problems []string
	if counts[2] > 0 {
		problems = append(problems, fmt.Sprintf("%d documents break a business rule (key, timestamps or payment status)", counts[2]))
	}
	if counts[1] == 0 {
		problems = append(problems, "no canary writes: the database looks empty")
	}

	asOf, ok, err := p.Time("as_of")
	if err != nil {
		return Result{}, err
	}
	if ok {
		tol, err := p.Duration("tolerance", time.Second)
		if err != nil {
			return Result{}, err
		}
		q := fmt.Sprintf(`SELECT count(*) FILTER (WHERE created_at <= %s), count(*) FILTER (WHERE created_at > %s) FROM documents`,
			pgTime(asOf.Add(-tol)), pgTime(asOf.Add(tol)))
		restored, err := r.ints(ctx, host, q, 2)
		if err != nil {
			return Result{}, err
		}
		res.Metrics["documents_before_target"] = float64(restored[0])
		if restored[1] > 0 {
			problems = append(problems, fmt.Sprintf("%d documents were created after the restore target", restored[1]))
		}
		if cmp := p.With["compare_host"]; cmp != "" {
			prod, err := r.ints(ctx, cmp, q, 2)
			if err != nil {
				return Result{}, err
			}
			res.Metrics["production_documents_before_target"] = float64(prod[0])
			if prod[0] != restored[0] {
				problems = append(problems, fmt.Sprintf("%s has %d documents created before the target, the restore has %d", cmp, prod[0], restored[0]))
			}
		}
	}
	if len(problems) > 0 {
		res.Status, res.Summary, res.Details = Fail, problems[0], problems
		return res, nil
	}
	res.Summary = fmt.Sprintf("%d documents and %d canary writes; every business rule holds", counts[0], counts[1])
	if ok {
		res.Summary += "; document counts match production at the target"
	}
	return res, nil
}

func (r Rules) ints(ctx context.Context, host, query string, n int) ([]int64, error) {
	rows, err := r.SQL.Query(ctx, host, query)
	if err != nil {
		return nil, err
	}
	if len(rows) != 1 || len(rows[0]) != n {
		return nil, fmt.Errorf("unexpected result shape from %s: %v", host, rows)
	}
	out := make([]int64, n)
	for i, v := range rows[0] {
		if out[i], err = strconv.ParseInt(v, 10, 64); err != nil {
			return nil, fmt.Errorf("unexpected value from %s: %w", host, err)
		}
	}
	return out, nil
}

// pgTime renders t as a PostgreSQL timestamptz literal.
func pgTime(t time.Time) string { return "TIMESTAMPTZ '" + t.UTC().Format(time.RFC3339Nano) + "'" }

// Query runs one SQL statement on a host's docsvc database and compares its
// single value with an expected one, e.g. a count captured before an incident.
// Params: host, sql, want.
type Query struct {
	SQL SQL
}

// Run implements Check.
func (q Query) Run(ctx context.Context, p Params) (Result, error) {
	if err := p.Require("host", "sql", "want"); err != nil {
		return Result{}, err
	}
	rows, err := q.SQL.Query(ctx, p.With["host"], p.With["sql"])
	if err != nil {
		return Result{}, err
	}
	if len(rows) != 1 || len(rows[0]) != 1 {
		return Result{}, fmt.Errorf("query must return one value, got %v", rows)
	}
	got, want := rows[0][0], p.With["want"]
	res := Result{Details: []string{p.With["sql"]}}
	if got != want {
		res.Status, res.Summary = Fail, fmt.Sprintf("query on %s returned %q, want %q", p.With["host"], got, want)
		return res, nil
	}
	res.Status, res.Summary = Pass, fmt.Sprintf("query on %s returned %q as expected", p.With["host"], got)
	return res, nil
}

// ObjectVersion is one version of an object in a store.
type ObjectVersion struct {
	Key          string
	Modified     time.Time
	DeleteMarker bool
	Latest       bool
}

// ObjectLister lists object versions in an attachment store.
type ObjectLister interface {
	ListVersions(ctx context.Context, prefix string) ([]ObjectVersion, error)
}

// Consistency compares documents with their attachments. Params: host,
// store ("vault" or a site store such as "obj-a"); optional as_of and grace
// (default 60s). Against the vault, any retained version counts, because the
// vault keeps history; against a site store the current version must exist.
// Missing attachments fail the check; orphan objects older than the grace
// period are reported but harmless (an upload whose database insert failed).
type Consistency struct {
	SQL    SQL
	Stores func(ctx context.Context, name string) (ObjectLister, error)
}

// Run implements Check.
func (c Consistency) Run(ctx context.Context, p Params) (Result, error) {
	if err := p.Require("host", "store"); err != nil {
		return Result{}, err
	}
	grace, err := p.Duration("grace", time.Minute)
	if err != nil {
		return Result{}, err
	}
	cutoff := p.Now
	if asOf, ok, err := p.Time("as_of"); err != nil {
		return Result{}, err
	} else if ok {
		cutoff = asOf
	}
	cutoff = cutoff.Add(-grace)

	rows, err := c.SQL.Query(ctx, p.With["host"], "SELECT attachment_key FROM documents")
	if err != nil {
		return Result{}, err
	}
	store, err := c.Stores(ctx, p.With["store"])
	if err != nil {
		return Result{}, err
	}
	versions, err := store.ListVersions(ctx, "documents/")
	if err != nil {
		return Result{}, err
	}

	history := p.With["store"] == "vault"
	present := map[string]bool{}
	firstSeen := map[string]time.Time{}
	for _, v := range versions {
		if v.DeleteMarker {
			continue
		}
		if history || v.Latest {
			present[v.Key] = true
		}
		if f, ok := firstSeen[v.Key]; !ok || v.Modified.Before(f) {
			firstSeen[v.Key] = v.Modified
		}
	}
	keys := map[string]bool{}
	var missing, orphans []string
	for _, row := range rows {
		keys[row[0]] = true
		if !present[row[0]] {
			missing = append(missing, row[0])
		}
	}
	for k := range present {
		if !keys[k] && !firstSeen[k].After(cutoff) {
			orphans = append(orphans, k)
		}
	}
	res := Result{Status: Pass, Metrics: map[string]float64{
		"documents": float64(len(rows)), "objects": float64(len(present)),
		"missing": float64(len(missing)), "orphans": float64(len(orphans)),
	}}
	res.Details = append(details("missing attachments", missing, 10), details("orphan objects", orphans, 10)...)
	if len(missing) > 0 {
		res.Status = Fail
		res.Summary = fmt.Sprintf("%d of %d documents have no attachment in %s", len(missing), len(rows), p.With["store"])
		return res, nil
	}
	res.Summary = fmt.Sprintf("all %d documents have their attachment in %s; %d orphan object(s)", len(rows), p.With["store"], len(orphans))
	return res, nil
}

// PITR checks a point-in-time restore against the canary journal: every write
// acknowledged before the target must be present and none sent after it.
// Params: host, target; optional tolerance (default from bia.yaml).
type PITR struct {
	SQL       SQL
	Journal   func() ([]canary.Entry, error)
	Tolerance time.Duration
}

// PITRWindow bounds the journal entries judged around the target.
const PITRWindow = 2 * time.Minute

// Run implements Check.
func (c PITR) Run(ctx context.Context, p Params) (Result, error) {
	if err := p.Require("host", "target"); err != nil {
		return Result{}, err
	}
	target, _, err := p.Time("target")
	if err != nil {
		return Result{}, err
	}
	tol, err := p.Duration("tolerance", c.Tolerance)
	if err != nil {
		return Result{}, err
	}
	entries, err := c.Journal()
	if err != nil {
		return Result{}, err
	}
	var sel []canary.Entry
	for _, e := range entries {
		if !e.Sent.Before(target.Add(-PITRWindow)) && !e.Sent.After(target.Add(PITRWindow)) {
			sel = append(sel, e)
		}
	}
	if len(sel) == 0 {
		return Result{}, fmt.Errorf("canary journal has no writes within %s of %s", PITRWindow, target.Format(time.RFC3339))
	}
	rows, err := c.SQL.Query(ctx, p.With["host"],
		fmt.Sprintf("SELECT seq FROM canary WHERE seq BETWEEN %d AND %d", sel[0].Seq, sel[len(sel)-1].Seq))
	if err != nil {
		return Result{}, err
	}
	present := map[int64]bool{}
	for _, r := range rows {
		seq, err := strconv.ParseInt(r[0], 10, 64)
		if err != nil {
			return Result{}, err
		}
		present[seq] = true
	}
	res := canary.CheckPITR(sel, target, tol, func(s int64) bool { return present[s] })
	out := Result{Metrics: map[string]float64{
		"must_exist": float64(res.MustExist), "must_not_exist": float64(res.MustNotExist),
		"missing": float64(len(res.Missing)), "unexpected": float64(len(res.Unexpected)),
	}}
	if res.LastPresent != nil {
		out.Metrics["newest_write_before_target_seconds"] = target.Sub(res.LastPresent.Acked).Seconds()
	}
	out.Details = append(details("missing writes (seq)", res.Missing, 10), details("unexpected writes (seq)", res.Unexpected, 10)...)
	switch {
	case res.OK():
		out.Status = Pass
		out.Summary = fmt.Sprintf("restore matches %s: all %d writes acknowledged before it are present, none of the %d sent after it",
			target.UTC().Format(time.RFC3339Nano), res.MustExist, res.MustNotExist)
	case res.MustExist == 0 || res.MustNotExist == 0:
		out.Status = Fail
		out.Summary = "not enough canary writes on both sides of the target to judge the restore"
	default:
		out.Status = Fail
		out.Summary = fmt.Sprintf("restore does not match %s: %d acknowledged writes missing, %d later writes present",
			target.UTC().Format(time.RFC3339Nano), len(res.Missing), len(res.Unexpected))
	}
	return out, nil
}

// rpoLookback is how far before an incident LiveRPO looks for the last
// surviving write; it bounds the largest RPO it can report.
const rpoLookback = 15 * time.Minute

// LiveRPO measures actual data loss from the canary journal and the writes
// that survived in production. With an incident (an inject phase) it reports
// the spec's actual RPO; without one, as in a controlled switchover (S7), any
// lost acknowledged write since the drill started fails the check.
type LiveRPO struct {
	Journal   func() ([]canary.Entry, error)
	Survivors func(ctx context.Context, from int64) (map[int64]bool, error)
	Target    time.Duration
}

// Run implements Check.
func (c LiveRPO) Run(ctx context.Context, p Params) (Result, error) {
	entries, err := c.Journal()
	if err != nil {
		return Result{}, err
	}
	// With an incident, look back well before it: the writes that were lost
	// were acknowledged before the drill started, and the last survivor may
	// be minutes old. A switchover only answers for its own writes.
	since, incident := p.Start, p.Now
	if p.Incident != nil {
		lookback, err := p.Duration("lookback", rpoLookback)
		if err != nil {
			return Result{}, err
		}
		since, incident = p.Incident.Add(-lookback), *p.Incident
	}
	var from int64
	for _, e := range entries {
		if !e.Acked.Before(since) {
			from = e.Seq
			break
		}
	}
	if from == 0 {
		return Result{}, errors.New("canary journal has no writes in the evaluated window")
	}
	survived, err := c.Survivors(ctx, from)
	if err != nil {
		return Result{}, err
	}
	r := canary.LiveRPO(entries, since, incident, p.Now, func(s int64) bool { return survived[s] })
	res := Result{
		Status:  Pass,
		Metrics: map[string]float64{"lost": float64(len(r.Lost)), "anomalies": float64(len(r.Anomalies)), "acknowledged": float64(r.Considered)},
		Details: append(details("lost writes (seq)", r.Lost, 10), details("ordering anomalies: older write lost while a newer one survived (seq)", r.Anomalies, 10)...),
	}
	if p.Incident == nil {
		if len(r.Lost) > 0 {
			res.Status = Fail
			res.Summary = fmt.Sprintf("%d of %d acknowledged writes were lost; a controlled switchover must lose none", len(r.Lost), r.Considered)
			return res, nil
		}
		res.Summary = fmt.Sprintf("all %d writes acknowledged since the drill started survived", r.Considered)
		res.Measurements = []Measurement{{Name: "rpo", Actual: 0, Target: c.Target}}
		return res, nil
	}
	if r.LastSurvivor == nil {
		res.Status = Fail
		res.Summary = fmt.Sprintf("no write acknowledged in the %s before the incident survived", incident.Sub(since))
		return res, nil
	}
	res.Metrics["rpo_seconds"] = r.Value.Seconds()
	res.Summary = fmt.Sprintf("actual RPO %s (target %s); %d of %d acknowledged writes lost",
		r.Value.Round(time.Millisecond), c.Target, len(r.Lost), r.Considered)
	res.Measurements = []Measurement{{Name: "rpo", Actual: r.Value, Target: c.Target}}
	return res, nil
}

// LiveRTO measures availability from the prober samples. With an incident it
// reports the actual RTO; without one (a controlled switchover) it reports
// the longest outage as downtime against the same target.
type LiveRTO struct {
	Samples func() []prober.Sample
	Target  time.Duration
	Streak  int
	// Settle is how long to wait for the streak of successful probes when
	// verification starts right after the last recovery step; zero judges
	// the samples as they are.
	Settle time.Duration
	Poll   time.Duration
}

// Run implements Check.
func (c LiveRTO) Run(ctx context.Context, p Params) (Result, error) {
	if p.Incident != nil && c.Settle > 0 {
		deadline := time.Now().Add(c.Settle)
		for {
			if _, _, ok := prober.RTO(c.Samples(), *p.Incident, c.Streak); ok || time.Now().After(deadline) {
				break
			}
			select {
			case <-ctx.Done():
				return Result{}, ctx.Err()
			case <-time.After(c.Poll):
			}
		}
	}
	samples := c.Samples()
	total, failed := prober.Availability(samples)
	metrics := map[string]float64{"probes": float64(total), "failed_probes": float64(failed)}
	if p.Incident == nil {
		outage, recovered := prober.LongestOutage(samples)
		metrics["downtime_seconds"] = outage.Seconds()
		if !recovered {
			return Result{Status: Fail, Summary: "the service was still down when verification started", Metrics: metrics}, nil
		}
		return Result{Status: Pass, Metrics: metrics,
			Summary:      fmt.Sprintf("longest outage %s (target %s)", outage.Round(time.Millisecond), c.Target),
			Measurements: []Measurement{{Name: "downtime", Actual: outage, Target: c.Target}}}, nil
	}
	rto, down, ok := prober.RTO(samples, *p.Incident, c.Streak)
	if !ok {
		return Result{Status: Fail, Summary: fmt.Sprintf("service did not recover: no %d consecutive successful probes after the outage", c.Streak), Metrics: metrics}, nil
	}
	metrics["rto_seconds"] = rto.Seconds()
	summary := fmt.Sprintf("actual RTO %s (target %s)", rto.Round(time.Millisecond), c.Target)
	if !down {
		summary = "no failed probe after the incident"
	}
	return Result{Status: Pass, Summary: summary, Metrics: metrics,
		Measurements: []Measurement{{Name: "rto", Actual: rto, Target: c.Target}}}, nil
}
