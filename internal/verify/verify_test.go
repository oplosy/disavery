package verify_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/oplosy/disavery/internal/canary"
	"github.com/oplosy/disavery/internal/prober"
	"github.com/oplosy/disavery/internal/verify"
)

type fakeRemote struct {
	out     string
	err     error
	command string
	stdin   string
}

func (f *fakeRemote) Run(_ context.Context, _, command string, stdin io.Reader) (string, error) {
	f.command = command
	if stdin != nil {
		b, _ := io.ReadAll(stdin)
		f.stdin = string(b)
	}
	return f.out, f.err
}

func params(kv ...string) verify.Params {
	p := verify.Params{With: map[string]string{}, Now: time.Now()}
	for i := 0; i+1 < len(kv); i += 2 {
		p.With[kv[i]] = kv[i+1]
	}
	return p
}

func TestAmcheck(t *testing.T) {
	tests := []struct {
		name    string
		remote  fakeRemote
		status  verify.Status
		wantErr bool
	}{
		{"clean", fakeRemote{}, verify.Pass, false},
		{"corruption", fakeRemote{out: "heap table \"docsvc.public.documents\", block 3: ...\n", err: errors.New("exit status 2")}, verify.Fail, false},
		{"cannot connect", fakeRemote{err: errors.New("ssh: connection refused")}, "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, err := verify.Amcheck{Remote: &tc.remote, Bin: "pg_amcheck"}.Run(context.Background(), params("host", "restore"))
			if (err != nil) != tc.wantErr || res.Status != tc.status {
				t.Fatalf("result %+v err %v", res, err)
			}
			if !strings.Contains(tc.remote.command, "pg_amcheck --heapallindexed --database=docsvc") {
				t.Fatalf("command %q", tc.remote.command)
			}
		})
	}
	if _, err := (verify.Amcheck{Remote: &fakeRemote{}}).Run(context.Background(), params()); err == nil {
		t.Fatal("want error for missing host")
	}
}

func TestPSQLParsesCSV(t *testing.T) {
	r := &fakeRemote{out: "1,\"a,b\"\n2,c\n"}
	rows, err := verify.PSQL{Remote: r, Database: "docsvc"}.Query(context.Background(), "db-a", "SELECT 1")
	if err != nil || len(rows) != 2 || rows[0][1] != "a,b" || r.stdin != "SELECT 1" || !strings.Contains(r.command, "-d docsvc") {
		t.Fatalf("rows %v err %v stdin %q command %q", rows, err, r.stdin, r.command)
	}
}

func TestParseTime(t *testing.T) {
	want := time.Date(2026, 10, 7, 12, 0, 1, 123_000_000, time.UTC)
	for _, s := range []string{"2026-10-07T12:00:01.123Z", "2026-10-07 12:00:01.123+00", "2026-10-07 14:00:01.123+02:00"} {
		got, err := verify.ParseTime(s)
		if err != nil || !got.Equal(want) {
			t.Fatalf("%q: %v %v", s, got, err)
		}
	}
	if _, err := verify.ParseTime("yesterday"); err == nil {
		t.Fatal("want error")
	}
}

func TestLiveRPOAndRTO(t *testing.T) {
	start := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	incident := start.Add(5 * time.Second)
	var entries []canary.Entry
	var samples []prober.Sample
	for i := range 20 {
		at := start.Add(time.Duration(i) * time.Second)
		entries = append(entries, canary.Entry{Seq: int64(100 + i), Sent: at, Acked: at.Add(50 * time.Millisecond)})
		for _, half := range []time.Duration{0, 500 * time.Millisecond} {
			ts := at.Add(half)
			// Down from 5 s to 8 s.
			samples = append(samples, prober.Sample{At: ts, OK: ts.Before(incident) || !ts.Before(start.Add(8*time.Second))})
		}
	}
	p := verify.Params{Start: start, Incident: &incident, Now: start.Add(time.Minute)}

	rpo, err := verify.LiveRPO{
		Journal: func() ([]canary.Entry, error) { return entries, nil },
		Survivors: func(_ context.Context, from int64) (map[int64]bool, error) {
			if from != 100 {
				t.Errorf("from %d", from)
			}
			s := map[int64]bool{}
			for seq := int64(100); seq <= 103; seq++ { // writes up to 3.05 s survived
				s[seq] = true
			}
			return s, nil
		},
		Target: 5 * time.Second,
	}.Run(context.Background(), p)
	if err != nil || len(rpo.Measurements) != 1 || rpo.Measurements[0].Actual != 1950*time.Millisecond || !rpo.Measurements[0].Met() {
		t.Fatalf("rpo %+v %v", rpo, err)
	}

	rto, err := verify.LiveRTO{Samples: func() []prober.Sample { return samples }, Target: 2 * time.Second, Streak: 5}.Run(context.Background(), p)
	if err != nil || rto.Measurements[0].Actual != 3*time.Second || rto.Measurements[0].Met() {
		t.Fatalf("rto %+v %v", rto, err)
	}
}

// TestLiveRPOLooksBeforeTheDrill pins a measurement bug found in a pilot-light
// drill: the drill started half a second before the incident, every write of
// the last 72 s was lost, and judging only writes since the drill started
// reported an RPO of 0.5 s instead of 72 s.
func TestLiveRPOLooksBeforeTheDrill(t *testing.T) {
	incident := time.Date(2026, 10, 7, 16, 37, 35, 878_000_000, time.UTC)
	var entries []canary.Entry
	for i := range 120 {
		sent := incident.Add(time.Duration(i-120) * time.Second)
		entries = append(entries, canary.Entry{Seq: int64(1000 + i), Sent: sent, Acked: sent.Add(47 * time.Millisecond)})
	}
	p := verify.Params{Start: incident.Add(-487 * time.Millisecond), Incident: &incident, Now: incident.Add(3 * time.Minute)}
	check := verify.LiveRPO{
		Journal: func() ([]canary.Entry, error) { return entries, nil },
		Survivors: func(_ context.Context, from int64) (map[int64]bool, error) {
			if from != 1000 {
				t.Errorf("survivors queried from %d, want the start of the lookback window", from)
			}
			s := map[int64]bool{}
			for seq := int64(1000); seq <= 1047; seq++ { // writes up to 72 s before the incident survived
				s[seq] = true
			}
			return s, nil
		},
		Target: time.Minute,
	}
	res, err := check.Run(context.Background(), p)
	if err != nil || res.Measurements[0].Actual != 72953*time.Millisecond || res.Measurements[0].Met() || res.Metrics["lost"] != 72 {
		t.Fatalf("%+v %v", res, err)
	}

	// Nothing survived inside the window: fail rather than report the window as RPO.
	check.Survivors = func(context.Context, int64) (map[int64]bool, error) { return map[int64]bool{}, nil }
	if res, err := check.Run(context.Background(), p); err != nil || res.Status != verify.Fail {
		t.Fatalf("total loss: %+v %v", res, err)
	}
}
