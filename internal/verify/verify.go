// Package verify holds the built-in verifiers that runbook `check` steps run.
// A verifier reports pass, fail or skip; an error means it could not judge.
package verify

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
)

// Status is a verifier's verdict.
type Status string

// Verdicts.
const (
	Pass Status = "pass"
	Fail Status = "fail"
	Skip Status = "skip"
)

// Measurement is a measured value with its target, e.g. actual RPO.
type Measurement struct {
	Name   string
	Actual time.Duration
	Target time.Duration
}

// Met reports whether the target was met.
func (m Measurement) Met() bool { return m.Actual <= m.Target }

// Result is a verifier's outcome.
type Result struct {
	Name         string             `json:"name"`
	Status       Status             `json:"status"`
	Summary      string             `json:"summary"`
	Metrics      map[string]float64 `json:"metrics,omitempty"`
	Details      []string           `json:"details,omitempty"`
	Measurements []Measurement      `json:"-"`
}

// Params are a check step's inputs.
type Params struct {
	With     map[string]string
	Start    time.Time  // drill start
	Incident *time.Time // start of the inject phase, if any
	Now      time.Time
}

// Require returns an error naming every missing parameter.
func (p Params) Require(keys ...string) error {
	var missing []string
	for _, k := range keys {
		if p.With[k] == "" {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing parameter(s): %s", strings.Join(missing, ", "))
	}
	return nil
}

// timeLayouts accepts RFC 3339 and PostgreSQL's text form ("2026-10-07 12:00:00.123+00").
var timeLayouts = []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999-07", "2006-01-02 15:04:05.999999999-07:00"}

// ParseTime parses a timestamp in any accepted layout.
func ParseTime(s string) (time.Time, error) {
	for _, l := range timeLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse time %q", s)
}

// Time returns an optional time parameter.
func (p Params) Time(key string) (t time.Time, ok bool, err error) {
	v := p.With[key]
	if v == "" {
		return time.Time{}, false, nil
	}
	t, err = ParseTime(v)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("%s: %w", key, err)
	}
	return t, true, nil
}

// Duration returns an optional duration parameter.
func (p Params) Duration(key string, def time.Duration) (time.Duration, error) {
	v := p.With[key]
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return d, nil
}

// Check is a verifier.
type Check interface {
	Run(ctx context.Context, p Params) (Result, error)
}

// Registry maps check names to verifiers.
type Registry map[string]Check

// Names returns the registered names in sorted order.
func (r Registry) Names() []string {
	names := make([]string, 0, len(r))
	for n := range r {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

// Remote runs a command on a lab node (implemented by lab.SSH). On a non-zero
// exit it returns the stdout gathered so far together with the error.
type Remote interface {
	Run(ctx context.Context, host, command string, stdin io.Reader) (string, error)
}

// SQL queries the docsvc database on a host; values come back as text.
type SQL interface {
	Query(ctx context.Context, host, query string) ([][]string, error)
}

// PSQL runs queries with psql on the node as the postgres superuser over the
// local socket, so verification needs no network access to the database.
type PSQL struct {
	Remote   Remote
	Database string
}

// Query runs one statement and parses psql's CSV output.
func (p PSQL) Query(ctx context.Context, host, query string) ([][]string, error) {
	out, err := p.Remote.Run(ctx, host,
		"runuser -u postgres -- psql -X -q --csv -t -v ON_ERROR_STOP=1 -d "+p.Database+" -f -",
		strings.NewReader(query))
	if err != nil {
		return nil, err
	}
	return csv.NewReader(bytes.NewBufferString(out)).ReadAll()
}

// details formats up to max items, noting how many were left out.
func details[T any](label string, items []T, max int) []string {
	if len(items) == 0 {
		return nil
	}
	shown := items[:min(len(items), max)]
	line := fmt.Sprintf("%s: %v", label, shown)
	if len(items) > max {
		line += fmt.Sprintf(" (and %d more)", len(items)-max)
	}
	return []string{line}
}
