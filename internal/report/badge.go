package report

// Badge is a shields.io endpoint badge (https://shields.io/badges/endpoint-badge).
type Badge struct {
	SchemaVersion int    `json:"schemaVersion"`
	Label         string `json:"label"`
	Message       string `json:"message"`
	Color         string `json:"color"`
}

var badgeColors = map[Result]string{Pass: "brightgreen", MissedTarget: "yellow", Failed: "red", Error: "lightgrey"}

// NewBadge describes the newest run of a scenario in the history, e.g.
// "PASS · 2026-10-08 02:31 UTC". The time is when that run finished: a static
// badge cannot say "6 h ago", and the date tells a reader the same.
func NewBadge(entries []HistoryEntry, scenario, label string) Badge {
	b := Badge{SchemaVersion: 1, Label: label, Message: "no run yet", Color: "lightgrey"}
	var last *HistoryEntry
	for i := range entries {
		if e := &entries[i]; e.Scenario == scenario && (last == nil || e.StartedAt.After(last.StartedAt)) {
			last = e
		}
	}
	if last == nil {
		return b
	}
	finished := last.StartedAt.Add(seconds(last.DurationSeconds)).UTC()
	b.Message = string(last.Result) + " · " + finished.Format("2006-01-02 15:04") + " UTC"
	b.Color = badgeColors[last.Result]
	return b
}
