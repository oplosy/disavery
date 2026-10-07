// Package restorepoint chooses what the random restore test (S6) restores: a
// random backup set and a random point in time that the WAL archive can reach
// and the canary journal can judge.
package restorepoint

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"
)

// Backup is one backup set in a repository.
type Backup struct {
	Label string
	Type  string // full, diff or incr
	Repo  int
	Start time.Time
	Stop  time.Time
}

// ParseInfo extracts the backups of the first stanza in repo from
// `pgbackrest info --output=json`.
func ParseInfo(data []byte, repo int) ([]Backup, error) {
	var info []struct {
		Name   string `json:"name"`
		Backup []struct {
			Label    string `json:"label"`
			Type     string `json:"type"`
			Error    bool   `json:"error"`
			Database struct {
				RepoKey int `json:"repo-key"`
			} `json:"database"`
			Timestamp struct {
				Start int64 `json:"start"`
				Stop  int64 `json:"stop"`
			} `json:"timestamp"`
		} `json:"backup"`
	}
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("parse pgbackrest info: %w", err)
	}
	if len(info) == 0 {
		return nil, errors.New("pgbackrest info lists no stanza")
	}
	var out []Backup
	for _, b := range info[0].Backup {
		if b.Database.RepoKey != repo || b.Error {
			continue
		}
		out = append(out, Backup{
			Label: b.Label, Type: b.Type, Repo: repo,
			Start: time.Unix(b.Timestamp.Start, 0).UTC(), Stop: time.Unix(b.Timestamp.Stop, 0).UTC(),
		})
	}
	return out, nil
}

// Point is the chosen restore: printed as JSON for the runbook to capture.
type Point struct {
	Repo   int    `json:"repo"`
	Set    string `json:"set"`
	Type   string `json:"type"`
	Target string `json:"target"` // PostgreSQL timestamptz text, as pgBackRest and PostgreSQL expect it
	// TargetTime is Target as RFC 3339.
	TargetTime  time.Time `json:"target_time"`
	WindowStart time.Time `json:"window_start"`
	WindowEnd   time.Time `json:"window_end"`
	Candidates  int       `json:"candidates"`
	Seed        int64     `json:"seed"`
}

// TargetLayout formats targets the way PostgreSQL prints timestamptz.
const TargetLayout = "2006-01-02 15:04:05.000-07"

// Pick chooses uniformly among backup sets that have a usable window and then
// a uniform target inside it. A window starts margin after both the set's end
// and the start of canary coverage, and ends margin before the newest
// archived WAL, so the target is reachable and has canary evidence around it.
func Pick(backups []Backup, coverage, archived time.Time, margin time.Duration, seed int64) (Point, error) {
	type window struct {
		b          Backup
		start, end time.Time
	}
	var ws []window
	for _, b := range backups {
		start := b.Stop
		if coverage.After(start) {
			start = coverage
		}
		start, end := start.Add(margin), archived.Add(-margin)
		if start.Before(end) {
			ws = append(ws, window{b, start, end})
		}
	}
	if len(ws) == 0 {
		return Point{}, fmt.Errorf("no backup set has a restorable window yet (%d sets, canary coverage from %s, WAL archived until %s); wait a few minutes",
			len(backups), coverage.UTC().Format(time.RFC3339), archived.UTC().Format(time.RFC3339))
	}
	rng := rand.New(rand.NewPCG(uint64(seed), 0))
	w := ws[rng.IntN(len(ws))]
	target := w.start.Add(time.Duration(rng.Int64N(int64(w.end.Sub(w.start))))).Truncate(time.Millisecond).UTC()
	return Point{
		Repo: w.b.Repo, Set: w.b.Label, Type: w.b.Type,
		Target: target.Format(TargetLayout), TargetTime: target,
		WindowStart: w.start, WindowEnd: w.end, Candidates: len(ws), Seed: seed,
	}, nil
}
