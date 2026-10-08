package canary_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oplosy/disavery/internal/canary"
)

var t0 = time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

// journal builds one entry per second starting at t0: write i is sent at
// t0+i s and acknowledged 100 ms later. Sequence numbers start at 1.
func journal(n int) []canary.Entry {
	out := make([]canary.Entry, n)
	for i := range out {
		sent := t0.Add(time.Duration(i) * time.Second)
		out[i] = canary.Entry{Seq: int64(i + 1), Sent: sent, Acked: sent.Add(100 * time.Millisecond)}
	}
	return out
}

func set(seqs ...int64) func(int64) bool {
	return func(s int64) bool { return slices.Contains(seqs, s) }
}

func upTo(n int64) func(int64) bool { return func(s int64) bool { return s <= n } }

func TestJournalRoundTripIgnoresTornLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "journal.jsonl")
	j, err := canary.OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range journal(3) {
		if err := j.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	j.Close()
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	_, _ = f.WriteString(`{"seq":4,"sent":"2026-`)
	f.Close()

	got, err := canary.ReadJournal(path)
	if err != nil || len(got) != 3 || got[2].Seq != 3 || !got[2].Acked.Equal(journal(3)[2].Acked) {
		t.Fatalf("got %+v, %v", got, err)
	}
	if missing, err := canary.ReadJournal(filepath.Join(t.TempDir(), "none")); err != nil || len(missing) != 0 {
		t.Fatalf("missing journal: %v %v", missing, err)
	}
}

func TestWriterJournalsOnlyAcknowledgedWrites(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Seq int64 }
		_ = json.NewDecoder(r.Body).Decode(&body)
		if calls.Add(1) == 2 { // the second write fails
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "journal.jsonl")
	j, err := canary.OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	w := &canary.Writer{
		URL: srv.URL, Client: srv.Client(), Interval: 10 * time.Millisecond, Journal: j,
		Now: time.Now, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	ctx, cancel := context.WithCancel(context.Background())
	// The writer is sequential: once the fourth write arrives, the third has
	// been acknowledged and journaled. Stopping at the third raced with it.
	go func() {
		for calls.Load() < 4 {
			time.Sleep(time.Millisecond)
		}
		cancel()
	}()
	if err := w.Run(ctx, 100); err != nil {
		t.Fatalf("run: %v", err)
	}
	j.Close()
	got, _ := canary.ReadJournal(path)
	if len(got) < 2 || got[0].Seq != 100 || got[1].Seq != 102 || got[0].Acked.Before(got[0].Sent) {
		t.Fatalf("journal %+v", got)
	}
}

func TestNextSeq(t *testing.T) {
	now := time.UnixMilli(5_000)
	if got := canary.NextSeq(0, now); got != 5_000 {
		t.Fatalf("empty journal: %d", got)
	}
	if got := canary.NextSeq(9_000, now); got != 9_001 {
		t.Fatalf("journal ahead of clock: %d", got)
	}
}

func TestLiveRPO(t *testing.T) {
	j := journal(10) // acks at t0+0.1s .. t0+9.1s
	incident := t0.Add(6 * time.Second)
	until := t0.Add(time.Minute)

	// A crash: writes 5 and 6 were lost, the service came back for write 7.
	r := canary.LiveRPO(j, t0, incident, until, func(s int64) bool { return s <= 4 || s >= 7 })
	if r.LastSurvivor == nil || r.LastSurvivor.Seq != 4 || r.Value != incident.Sub(j[3].Acked) {
		t.Fatalf("rpo %+v", r)
	}
	if !slices.Equal(r.Lost, []int64{5, 6}) || len(r.Anomalies) != 0 || r.Considered != 10 {
		t.Fatalf("lost %v anomalies %v considered %d", r.Lost, r.Anomalies, r.Considered)
	}

	// A point-in-time rewind (S3) also discards writes acknowledged after the
	// incident: RPO runs to the newest of them.
	r = canary.LiveRPO(j, t0, incident, until, upTo(4))
	if r.LastSurvivor.Seq != 4 || r.Value != j[9].Acked.Sub(j[3].Acked) || !slices.Equal(r.Lost, []int64{5, 6, 7, 8, 9, 10}) {
		t.Fatalf("rewind %+v", r)
	}

	// Writes 5 lost but 6 survived: an ordering anomaly.
	r = canary.LiveRPO(j, t0, incident, until, set(1, 2, 3, 4, 6))
	if !slices.Equal(r.Anomalies, []int64{5}) || r.LastSurvivor.Seq != 6 {
		t.Fatalf("anomaly case %+v", r)
	}

	// Nothing from before the incident survived: RPO is the whole window.
	r = canary.LiveRPO(j, t0, incident, until, func(s int64) bool { return s >= 7 })
	if r.LastSurvivor != nil || r.Value != 6*time.Second {
		t.Fatalf("total loss %+v", r)
	}

	// Writes acknowledged after the incident that survived do not shrink RPO.
	r = canary.LiveRPO(j, t0, incident, until, upTo(10))
	if r.LastSurvivor.Seq != 6 || r.Value != incident.Sub(j[5].Acked) || len(r.Lost) != 0 {
		t.Fatalf("no loss %+v", r)
	}
}

func TestCheckPITR(t *testing.T) {
	j := journal(20)
	target := t0.Add(10*time.Second + 500*time.Millisecond) // between write 11 (ack 10.1s) and 12 (sent 11s)
	tol := 200 * time.Millisecond

	tests := []struct {
		name       string
		present    func(int64) bool
		ok         bool
		missing    []int64
		unexpected []int64
	}{
		{"exact", upTo(11), true, nil, nil},
		{"lost an acknowledged write", set(1, 2, 3, 4, 5, 6, 7, 8, 9, 11), false, []int64{10}, nil},
		{"restored too far", upTo(13), false, nil, []int64{12, 13}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := canary.CheckPITR(j, target, tol, tc.present)
			if p.OK() != tc.ok || !slices.Equal(p.Missing, tc.missing) || !slices.Equal(p.Unexpected, tc.unexpected) {
				t.Fatalf("%+v", p)
			}
		})
	}

	// Write 12 is sent 0.5 s after the target: within a 1 s tolerance it may go either way.
	if p := canary.CheckPITR(j, target, time.Second, upTo(12)); !p.OK() || p.MustNotExist != 8 {
		t.Fatalf("in-flight write judged: %+v", p)
	}
	// No journal entries after the target: not enough evidence.
	if p := canary.CheckPITR(j[:5], target, tol, upTo(5)); p.OK() {
		t.Fatalf("one-sided evidence accepted: %+v", p)
	}
}

func TestCoverageStart(t *testing.T) {
	j := journal(5)
	if at, ok := canary.CoverageStart(j, 3); !ok || !at.Equal(j[2].Acked) {
		t.Fatalf("got %v %v", at, ok)
	}
	// The database has a write the journal never recorded (lost response): start at the next entry.
	j = slices.Delete(j, 2, 3)
	if at, ok := canary.CoverageStart(j, 3); !ok || !at.Equal(j[2].Acked) || j[2].Seq != 4 {
		t.Fatalf("got %v %v", at, ok)
	}
	if _, ok := canary.CoverageStart(j, 99); ok {
		t.Fatal("want no coverage")
	}
}

func TestLiveRPOIgnoresEarlierIncidents(t *testing.T) {
	j := journal(600) // ten minutes of writes, one per second
	// An earlier drill lost writes 100-130; writes 131-520 survived; this
	// incident at 9:00 lost writes 521-540 (acked 8:40.1 to 8:59.1); writes
	// after it survived.
	survived := func(s int64) bool { return (s < 100 || s > 130) && (s <= 520 || s > 540) }
	incident := t0.Add(9 * time.Minute)
	r := canary.LiveRPO(j, t0, incident, t0.Add(10*time.Minute), survived)
	if r.LastSurvivor.Seq != 520 || r.Value != incident.Sub(j[519].Acked) {
		t.Fatalf("rpo %+v", r)
	}
	if len(r.Lost) != 20 || r.Lost[0] != 521 || len(r.Anomalies) != 0 {
		t.Fatalf("lost %d (first %d), anomalies %v", len(r.Lost), r.Lost[0], r.Anomalies)
	}
}

func TestSteady(t *testing.T) {
	j := journal(60) // one write per second, acked at t0+0.1s .. t0+59.1s
	to := t0.Add(60 * time.Second)
	w, err := canary.Steady(j, t0.Add(30*time.Second), to, 2*time.Second)
	if err != nil || len(w) != 30 || w[0].Seq != 31 {
		t.Fatalf("steady: %d entries, %v", len(w), err)
	}
	// A recovery a moment ago: writes 41-50 never happened.
	gappy := slices.Concat(j[:40], j[50:])
	if _, err := canary.Steady(gappy, t0.Add(30*time.Second), to, 2*time.Second); err == nil ||
		!strings.Contains(err.Error(), "silent for 11s from 12:00:39") {
		t.Fatalf("gap: %v", err)
	}
	// Silent now.
	if _, err := canary.Steady(j[:50], t0.Add(30*time.Second), to, 2*time.Second); err == nil {
		t.Fatal("want an error when the canary stopped")
	}
}
