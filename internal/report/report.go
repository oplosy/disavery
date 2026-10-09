// Package report writes drill evidence: report.json (schema-versioned),
// report.md for humans, and reports/history.jsonl for trends (spec §8).
package report

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/oplosy/disavery/internal/executor"
	"github.com/oplosy/disavery/internal/verify"
)

// SchemaVersion of report.json; bump on incompatible changes.
const SchemaVersion = 1

// Result is a drill's verdict.
type Result string

// Verdicts and their exit codes (spec §8).
const (
	Pass         Result = "PASS"          // recovered, all checks pass, targets met
	MissedTarget Result = "MISSED_TARGET" // recovered and verified, but a target was exceeded
	Failed       Result = "FAILED"        // recovery or verification failed
	Error        Result = "ERROR"         // tool or infrastructure error, preflight failure or abort
)

// ExitCode maps a verdict to the CLI exit code.
func (r Result) ExitCode() int {
	switch r {
	case Pass:
		return 0
	case MissedTarget:
		return 2
	case Failed:
		return 3
	default:
		return 4
	}
}

// Measurement is a measured value against its target, in seconds.
type Measurement struct {
	Name          string  `json:"name"`
	TargetSeconds float64 `json:"target_seconds"`
	ActualSeconds float64 `json:"actual_seconds"`
	Met           bool    `json:"met"`
}

// NewMeasurement converts a verifier measurement.
func NewMeasurement(m verify.Measurement) Measurement {
	return Measurement{Name: m.Name, TargetSeconds: m.Target.Seconds(), ActualSeconds: m.Actual.Seconds(), Met: m.Met()}
}

// Check is a preflight result.
type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"` // pass | fail
	Detail string `json:"detail"`
}

// Availability summarises the prober during the drill.
type Availability struct {
	Probes int `json:"probes"`
	Failed int `json:"failed"`
}

// Report is the machine-readable result of one drill.
type Report struct {
	SchemaVersion int                    `json:"schema_version"`
	RunID         string                 `json:"run_id"`
	Scenario      string                 `json:"scenario"`
	Description   string                 `json:"description,omitempty"`
	Tier          string                 `json:"tier,omitempty"`
	Env           string                 `json:"env"`
	Result        Result                 `json:"result"`
	Refused       bool                   `json:"refused,omitempty"`    // preflight failed: never started, changed nothing
	DataBytes     int64                  `json:"data_bytes,omitempty"` // production database size when the drill started
	Notes         []string               `json:"notes,omitempty"`
	Seed          int64                  `json:"seed"`
	StartedAt     time.Time              `json:"started_at"`
	FinishedAt    time.Time              `json:"finished_at"`
	IncidentAt    *time.Time             `json:"incident_at,omitempty"`
	Data          map[string]any         `json:"data,omitempty"`
	Measurements  []Measurement          `json:"measurements"`
	Preflight     []Check                `json:"preflight"`
	Phases        []executor.PhaseRecord `json:"phases"`
	Steps         []executor.StepRecord  `json:"steps"`
	Cleanup       []executor.StepRecord  `json:"cleanup"`
	Availability  *Availability          `json:"availability,omitempty"`
}

// Duration is the drill's wall-clock duration.
func (r *Report) Duration() time.Duration { return r.FinishedAt.Sub(r.StartedAt) }

// WriteDir writes report.json and report.md into dir.
func (r *Report) WriteDir(dir string) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "report.json"), append(b, '\n'), 0o644); err != nil {
		return err
	}
	md, err := Markdown(r)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "report.md"), md, 0o644)
}

// ReadFile loads a report.json.
func ReadFile(path string) (*Report, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r Report
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	return &r, nil
}
