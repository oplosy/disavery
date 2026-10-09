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
	// A drill refused by its preflight changed nothing and is not pushed.
	refused := &report.Report{Scenario: "s7-failback", Tier: "pilot-light", Result: report.Error, Refused: true, StartedAt: at(0), FinishedAt: at(1)}
	if err := report.Push(context.Background(), srv.Client(), srv.URL, refused); err != nil {
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
