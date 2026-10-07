package restorepoint_test

import (
	"strings"
	"testing"
	"time"

	"github.com/oplosy/disavery/internal/restorepoint"
	"github.com/oplosy/disavery/internal/verify"
)

// info is trimmed `pgbackrest info --output=json` output from the lab.
const info = `[{"backup":[
 {"database":{"id":1,"repo-key":1},"error":false,"label":"20261007-125300F","timestamp":{"start":1791377580,"stop":1791377590},"type":"full"},
 {"database":{"id":1,"repo-key":2},"error":false,"label":"20261007-125318F","timestamp":{"start":1791377598,"stop":1791377604},"type":"full"},
 {"database":{"id":1,"repo-key":2},"error":true,"label":"20261007-125900F","timestamp":{"start":1791377940,"stop":1791377950},"type":"full"},
 {"database":{"id":1,"repo-key":2},"error":false,"label":"20261007-125318F_20261007-130007I","timestamp":{"start":1791378007,"stop":1791378008},"type":"incr"}
],"name":"main"}]`

func TestParseInfo(t *testing.T) {
	got, err := restorepoint.ParseInfo([]byte(info), 2)
	if err != nil || len(got) != 2 || got[0].Label != "20261007-125318F" || got[1].Type != "incr" || got[0].Stop != time.Unix(1791377604, 0).UTC() {
		t.Fatalf("got %+v %v", got, err)
	}
	if _, err := restorepoint.ParseInfo([]byte(`[]`), 2); err == nil {
		t.Fatal("want error without a stanza")
	}
}

func TestPick(t *testing.T) {
	backups, _ := restorepoint.ParseInfo([]byte(info), 2)
	full, incr := backups[0].Stop, backups[1].Stop
	archived := incr.Add(10 * time.Minute)
	margin := 10 * time.Second

	seen := map[string]bool{}
	for seed := range int64(50) {
		p, err := restorepoint.Pick(backups, full.Add(-time.Hour), archived, margin, seed)
		if err != nil {
			t.Fatal(err)
		}
		seen[p.Set] = true
		var stop time.Time
		for _, b := range backups {
			if b.Label == p.Set {
				stop = b.Stop
			}
		}
		if p.TargetTime.Before(stop.Add(margin)) || p.TargetTime.After(archived.Add(-margin)) || p.Candidates != 2 {
			t.Fatalf("seed %d: target %s outside [%s, %s]", seed, p.TargetTime, stop.Add(margin), archived.Add(-margin))
		}
		parsed, err := verify.ParseTime(p.Target)
		if err != nil || !parsed.Equal(p.TargetTime) {
			t.Fatalf("target text %q does not round-trip: %v %v", p.Target, parsed, err)
		}
	}
	if !seen["20261007-125318F"] || !seen["20261007-125318F_20261007-130007I"] {
		t.Fatalf("both sets should be picked across seeds: %v", seen)
	}

	a, _ := restorepoint.Pick(backups, full, archived, margin, 7)
	b, _ := restorepoint.Pick(backups, full, archived, margin, 7)
	if a != b {
		t.Fatal("the same seed must choose the same point")
	}

	// Canary coverage starts after the incremental: both windows start there.
	cov := incr.Add(5 * time.Minute)
	p, _ := restorepoint.Pick(backups, cov, archived, margin, 1)
	if p.TargetTime.Before(cov.Add(margin)) {
		t.Fatalf("target %s before coverage", p.TargetTime)
	}

	// The archive has not caught up yet.
	if _, err := restorepoint.Pick(backups, full, full.Add(15*time.Second), margin, 1); err == nil || !strings.Contains(err.Error(), "no backup set has a restorable window") {
		t.Fatalf("got %v", err)
	}
}
