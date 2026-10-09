package drill

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oplosy/disavery/internal/executor"
	"github.com/oplosy/disavery/internal/runbook"
)

func TestWaitAlert(t *testing.T) {
	var polls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/alerts" || r.URL.Query().Get("active") != "true" {
			t.Errorf("unexpected request %s", r.URL)
		}
		if polls.Add(1) < 2 {
			_, _ = w.Write([]byte(`[{"labels":{"alertname":"ReplicationLagHigh"}}]`))
			return
		}
		_, _ = w.Write([]byte(`[{"labels":{"alertname":"ReplicationLagHigh"}},{"labels":{"alertname":"PostgresPrimaryDown"}},` +
			`{"labels":{"alertname":"RestoreTestStale","blocks_drills":"false"}}]`))
	}))
	defer srv.Close()

	r := waitAlertRunner{URL: srv.URL, Client: srv.Client()}
	step := executor.Step{Step: runbook.Step{ID: "detect", WaitAlert: "PostgresPrimaryDown"}}
	if _, err := r.Run(context.Background(), step, io.Discard); err != nil || polls.Load() != 2 {
		t.Fatalf("err %v after %d polls", err, polls.Load())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	step.WaitAlert = "NeverFires"
	if _, err := r.Run(ctx, step, io.Discard); err == nil || !strings.Contains(err.Error(), "NeverFires did not fire") {
		t.Fatalf("want timeout error, got %v", err)
	}

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
