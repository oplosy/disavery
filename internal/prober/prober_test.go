package prober_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/oplosy/disavery/internal/prober"
)

var t0 = time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

// samples builds one sample per 500 ms from pattern: '+' success, '-' failure.
func samples(pattern string) []prober.Sample {
	out := make([]prober.Sample, len(pattern))
	for i, c := range pattern {
		out[i] = prober.Sample{At: t0.Add(time.Duration(i) * 500 * time.Millisecond), OK: c == '+'}
	}
	return out
}

func TestRTO(t *testing.T) {
	incident := t0.Add(time.Second) // sample index 2
	tests := []struct {
		name     string
		pattern  string
		rto      time.Duration
		down, ok bool
	}{
		{"never down", "++++++++", 0, false, true},
		{"recovers", "++---+++++", 1500 * time.Millisecond, true, true},
		{"flaps before recovering", "++--++-+++++", 2500 * time.Millisecond, true, true},
		{"successes while the outage takes effect", "++++--+++++", 2 * time.Second, true, true},
		{"never recovers", "++--++++", 0, true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rto, down, ok := prober.RTO(samples(tc.pattern), incident, 5)
			if rto != tc.rto || down != tc.down || ok != tc.ok {
				t.Fatalf("RTO = %s down=%v ok=%v, want %s %v %v", rto, down, ok, tc.rto, tc.down, tc.ok)
			}
		})
	}
	if total, failed := prober.Availability(samples("++-+-")); total != 5 || failed != 2 {
		t.Fatalf("availability %d/%d", failed, total)
	}
}

func TestProbeRoundTrip(t *testing.T) {
	var mu sync.Mutex
	failReads := false
	mux := http.NewServeMux()
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("POST /documents", func(w http.ResponseWriter, r *http.Request) {
		if f, _, err := r.FormFile("file"); err != nil || r.FormValue("title") != "prober" {
			w.WriteHeader(http.StatusBadRequest)
			return
		} else {
			f.Close()
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"doc-1"}`))
	})
	mux.HandleFunc("GET /documents/doc-1", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if failReads {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	p := &prober.Prober{BaseURL: srv.URL, Client: srv.Client(), Interval: 5 * time.Millisecond, Timeout: time.Second, Now: time.Now}
	var got []prober.Sample
	ctx, cancel := context.WithCancel(context.Background())
	p.Run(ctx, func(s prober.Sample) {
		got = append(got, s)
		switch len(got) {
		case 1:
			mu.Lock()
			failReads = true
			mu.Unlock()
		case 2:
			cancel()
		}
	})
	if len(got) != 2 || !got[0].OK || got[1].OK || !strings.Contains(got[1].Err, "read: status 503") {
		t.Fatalf("samples %+v", got)
	}
}

func TestLongestOutage(t *testing.T) {
	tests := []struct {
		pattern   string
		longest   time.Duration
		recovered bool
	}{
		{"++++", 0, true},
		{"+--+---+", 1500 * time.Millisecond, true},
		{"+--+--", time.Second, false},
	}
	for _, tc := range tests {
		longest, recovered := prober.LongestOutage(samples(tc.pattern))
		if longest != tc.longest || recovered != tc.recovered {
			t.Fatalf("%s: %s %v", tc.pattern, longest, recovered)
		}
	}
}
