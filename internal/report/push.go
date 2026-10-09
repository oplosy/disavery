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
