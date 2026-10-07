package report

import (
	"bytes"
	_ "embed"
	"fmt"
	"strings"
	"text/template"
	"time"

	"github.com/oplosy/disavery/internal/executor"
)

//go:embed report.md.tmpl
var reportTemplate string

var funcs = template.FuncMap{
	"dur":   FormatDuration,
	"secs":  func(s float64) string { return FormatDuration(time.Duration(s * float64(time.Second))) },
	"since": func(a, b time.Time) string { return FormatDuration(b.Sub(a)) },
	"stamp": func(t time.Time) string { return t.UTC().Format("2006-01-02 15:04:05") },
	"clock": func(t time.Time) string { return t.UTC().Format("15:04:05") },
	"dash": func(s string) string {
		if s == "" {
			return "—"
		}
		return s
	},
	"yesno": func(b bool) string {
		if b {
			return "yes"
		}
		return "**no**"
	},
	"cell": func(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, "|", `\|`), "\n", " ") },
	"pct": func(a Availability) string {
		if a.Probes == 0 {
			return "n/a"
		}
		return fmt.Sprintf("%.2f %%", 100*float64(a.Probes-a.Failed)/float64(a.Probes))
	},
	"ganttTag": func(s executor.Status) string {
		if s == executor.Failed {
			return "crit"
		}
		return "done"
	},
}

var tmpl = template.Must(template.New("report").Funcs(funcs).Parse(reportTemplate))

// Markdown renders the human-readable report.
func Markdown(r *Report) ([]byte, error) {
	var b bytes.Buffer
	if err := tmpl.Execute(&b, r); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// FormatDuration rounds for reading: milliseconds under ten seconds, else seconds.
func FormatDuration(d time.Duration) string {
	if d < 10*time.Second {
		return d.Round(10 * time.Millisecond).String()
	}
	return d.Round(time.Second).String()
}

// AllSteps returns phase steps followed by cleanup steps.
func (r *Report) AllSteps() []executor.StepRecord {
	return append(append([]executor.StepRecord{}, r.Steps...), r.Cleanup...)
}

// CheckSteps returns the steps that ran a check.
func (r *Report) CheckSteps() []executor.StepRecord {
	var out []executor.StepRecord
	for _, s := range r.AllSteps() {
		if s.Check != nil {
			out = append(out, s)
		}
	}
	return out
}

// DecisionSteps returns the manual steps that recorded a decision.
func (r *Report) DecisionSteps() []executor.StepRecord {
	var out []executor.StepRecord
	for _, s := range r.AllSteps() {
		if s.Decision != nil {
			out = append(out, s)
		}
	}
	return out
}

// GanttSection is one phase of the timeline.
type GanttSection struct {
	Name  string
	Steps []executor.StepRecord
}

// Gantt groups executed (not skipped) steps by phase, in order.
func (r *Report) Gantt() []GanttSection {
	var out []GanttSection
	for _, s := range r.AllSteps() {
		if s.Status == executor.Skipped {
			continue
		}
		if len(out) == 0 || out[len(out)-1].Name != s.Phase {
			out = append(out, GanttSection{Name: s.Phase})
		}
		out[len(out)-1].Steps = append(out[len(out)-1].Steps, s)
	}
	return out
}
