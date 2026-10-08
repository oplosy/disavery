package verify_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oplosy/disavery/internal/canary"
	"github.com/oplosy/disavery/internal/prober"
	"github.com/oplosy/disavery/internal/verify"
)

// fakeSQL returns fixed rows for any query.
type fakeSQL [][]string

func (f fakeSQL) Query(context.Context, string, string) ([][]string, error) { return f, nil }

func TestSwitchoverWithoutIncident(t *testing.T) {
	start := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	var entries []canary.Entry
	var samples []prober.Sample
	for i := range 10 {
		at := start.Add(time.Duration(i) * time.Second)
		entries = append(entries, canary.Entry{Seq: int64(i + 1), Sent: at, Acked: at.Add(10 * time.Millisecond)})
		// Down from 3 s to 5 s.
		samples = append(samples, prober.Sample{At: at, OK: i < 3 || i >= 5})
	}
	p := verify.Params{Start: start, Now: start.Add(time.Minute)}
	journal := func() ([]canary.Entry, error) { return entries, nil }
	survivors := func(keep int64) func(context.Context, int64) (map[int64]bool, error) {
		return func(context.Context, int64) (map[int64]bool, error) {
			m := map[int64]bool{}
			for s := int64(1); s <= keep; s++ {
				m[s] = true
			}
			return m, nil
		}
	}

	res, err := verify.LiveRPO{Journal: journal, Survivors: survivors(10), Target: 5 * time.Second}.Run(context.Background(), p)
	if err != nil || res.Status != verify.Pass || res.Measurements[0].Actual != 0 {
		t.Fatalf("no loss: %+v %v", res, err)
	}
	res, err = verify.LiveRPO{Journal: journal, Survivors: survivors(8), Target: 5 * time.Second}.Run(context.Background(), p)
	if err != nil || res.Status != verify.Fail || !strings.Contains(res.Summary, "2 of 10 acknowledged writes were lost") {
		t.Fatalf("loss: %+v %v", res, err)
	}

	rto := verify.LiveRTO{Samples: func() []prober.Sample { return samples }, Target: 2 * time.Minute, Streak: 5}
	res, err = rto.Run(context.Background(), p)
	if err != nil || res.Status != verify.Pass || res.Measurements[0].Name != "downtime" || res.Measurements[0].Actual != 2*time.Second {
		t.Fatalf("downtime: %+v %v", res, err)
	}
	samples[len(samples)-1].OK = false
	if res, _ := rto.Run(context.Background(), p); res.Status != verify.Fail {
		t.Fatalf("still down: %+v", res)
	}
}

func TestTopology(t *testing.T) {
	current := func() (string, string, error) { return "b", "", nil }
	tests := []struct {
		with   map[string]string
		status verify.Status
	}{
		{map[string]string{"active_site": "b", "standby_site": ""}, verify.Pass},
		{map[string]string{"active_site": "b"}, verify.Pass},
		{map[string]string{"active_site": "a", "standby_site": "b"}, verify.Fail},
	}
	for _, tc := range tests {
		res, err := verify.Topology{Current: current}.Run(context.Background(), verify.Params{With: tc.with})
		if err != nil || res.Status != tc.status {
			t.Fatalf("%v: %+v %v", tc.with, res, err)
		}
	}
	if _, err := (verify.Topology{Current: current}).Run(context.Background(), verify.Params{With: map[string]string{}}); err == nil {
		t.Fatal("want error without active_site")
	}
	failing := verify.Topology{Current: func() (string, string, error) { return "", "", errors.New("no inventory") }}
	if _, err := failing.Run(context.Background(), params("active_site", "a")); err == nil {
		t.Fatal("want inventory error")
	}
}

func TestReplication(t *testing.T) {
	tests := []struct {
		name   string
		rows   fakeSQL
		status verify.Status
		want   string
	}{
		{"streaming", fakeSQL{{"db-b", "streaming", "0.004"}}, verify.Pass, "db-b streams from db-a"},
		{"catching up", fakeSQL{{"db-b", "catchup", "0"}}, verify.Fail, "is catchup, not streaming"},
		{"lagging", fakeSQL{{"db-b", "streaming", "12.5"}}, verify.Fail, "replays 12.5s behind"},
		{"absent", fakeSQL{{"other", "streaming", "0"}}, verify.Fail, "db-b is not connected to db-a"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, err := verify.Replication{SQL: tc.rows}.Run(context.Background(), params("primary", "db-a", "standby", "db-b"))
			if err != nil || res.Status != tc.status || !strings.Contains(res.Summary, tc.want) {
				t.Fatalf("%+v %v", res, err)
			}
		})
	}
}

func TestWebhookRoundTrip(t *testing.T) {
	var reads atomic.Int32
	var paidAfter atomic.Int32
	paidAfter.Store(2)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /documents", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"d1","payment_status":"pending"}`))
	})
	mux.HandleFunc("GET /documents/d1", func(w http.ResponseWriter, _ *http.Request) {
		status := "pending"
		if reads.Add(1) >= paidAfter.Load() {
			status = "paid"
		}
		_, _ = w.Write([]byte(`{"id":"d1","payment_status":"` + status + `"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	check := verify.WebhookRoundTrip{
		BaseURL: srv.URL, Poll: time.Millisecond,
		Client: func(context.Context) (*http.Client, error) { return srv.Client(), nil },
	}
	res, err := check.Run(context.Background(), params())
	if err != nil || res.Status != verify.Pass || !strings.Contains(res.Summary, "marked paid") {
		t.Fatalf("paid: %+v %v", res, err)
	}
	paidAfter.Store(1 << 30)
	res, err = check.Run(context.Background(), params("timeout", "20ms"))
	if err != nil || res.Status != verify.Fail || !strings.Contains(res.Summary, "callback did not arrive") {
		t.Fatalf("never paid: %+v %v", res, err)
	}
}

type fakeLister []verify.ObjectVersion

func (f fakeLister) ListVersions(context.Context, string) ([]verify.ObjectVersion, error) {
	return f, nil
}

func TestStoreSync(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	old, fresh := now.Add(-time.Hour), now.Add(-10*time.Second)
	stores := map[string]fakeLister{
		"obj-a": {
			{Key: "documents/1", Modified: old, Latest: true},
			{Key: "documents/2", Modified: old, Latest: true},
			{Key: "documents/3", Modified: fresh, Latest: true},                   // still replicating
			{Key: "documents/4", Modified: old, Latest: true, DeleteMarker: true}, // deleted
		},
		"obj-b": {{Key: "documents/1", Modified: old, Latest: true}},
	}
	check := verify.StoreSync{Stores: func(_ context.Context, name string) (verify.ObjectLister, error) { return stores[name], nil }}
	p := params("source", "obj-a", "replica", "obj-b")
	p.Now = now
	res, err := check.Run(context.Background(), p)
	if err != nil || res.Status != verify.Fail || res.Metrics["missing"] != 1 || !strings.Contains(res.Details[0], "documents/2") {
		t.Fatalf("%+v %v", res, err)
	}
	stores["obj-b"] = append(stores["obj-b"], verify.ObjectVersion{Key: "documents/2", Modified: old, Latest: true})
	if res, err := check.Run(context.Background(), p); err != nil || res.Status != verify.Pass {
		t.Fatalf("in sync: %+v %v", res, err)
	}
}

func TestQuery(t *testing.T) {
	p := verify.Params{With: map[string]string{"host": "db-a", "sql": "SELECT count(*) FROM documents", "want": "28"}}
	tests := []struct {
		name string
		rows fakeSQL
		want verify.Status
	}{
		{"match", fakeSQL{{"28"}}, verify.Pass},
		{"mismatch", fakeSQL{{"0"}}, verify.Fail},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, err := verify.Query{SQL: tc.rows}.Run(context.Background(), p)
			if err != nil || res.Status != tc.want {
				t.Fatalf("%+v %v", res, err)
			}
		})
	}
	if _, err := (verify.Query{SQL: fakeSQL{{"1", "2"}}}).Run(context.Background(), p); err == nil {
		t.Fatal("want an error for more than one value")
	}
	if _, err := (verify.Query{SQL: fakeSQL{{"1"}}}).Run(context.Background(), verify.Params{With: map[string]string{"host": "db-a"}}); err == nil {
		t.Fatal("want an error for missing parameters")
	}
}
