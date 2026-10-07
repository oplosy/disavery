package canary

import (
	"sort"
	"time"
)

// RPO compares the journal with the writes that survived an incident.
type RPO struct {
	Incident time.Time
	// Considered counts acknowledged writes in the evaluated window.
	Considered int
	// LastSurvivor is the newest write acknowledged before the incident that
	// survived; nil if none did.
	LastSurvivor *Entry
	// Value is Incident minus LastSurvivor's acknowledgement (the spec's
	// actual RPO), or Incident minus the window start if nothing survived.
	Value time.Duration
	// Lost lists acknowledged writes that did not survive.
	Lost []int64
	// Anomalies lists lost writes that are older than a surviving write: with
	// ordered replication or WAL shipping this should never happen.
	Anomalies []int64
}

// LiveRPO evaluates the writes acknowledged in [since, until] against the set
// of surviving sequence numbers.
func LiveRPO(entries []Entry, since, incident, until time.Time, survived func(int64) bool) RPO {
	r := RPO{Incident: incident}
	var newestSurvivor int64
	var lost []int64
	for i := range entries {
		e := &entries[i]
		if e.Acked.Before(since) || e.Acked.After(until) {
			continue
		}
		r.Considered++
		if !survived(e.Seq) {
			lost = append(lost, e.Seq)
			continue
		}
		newestSurvivor = max(newestSurvivor, e.Seq)
		if !e.Acked.After(incident) && (r.LastSurvivor == nil || e.Acked.After(r.LastSurvivor.Acked)) {
			r.LastSurvivor = e
		}
	}
	for _, seq := range lost {
		r.Lost = append(r.Lost, seq)
		if seq < newestSurvivor {
			r.Anomalies = append(r.Anomalies, seq)
		}
	}
	ref := since
	if r.LastSurvivor != nil {
		ref = r.LastSurvivor.Acked
	}
	r.Value = max(0, incident.Sub(ref))
	return r
}

// PITR compares a point-in-time restore with the journal.
type PITR struct {
	Target    time.Time
	Tolerance time.Duration
	// MustExist counts writes acknowledged before Target-Tolerance.
	MustExist int
	// MustNotExist counts writes sent after Target+Tolerance.
	MustNotExist int
	// Missing are writes that should be in the restore but are not.
	Missing []int64
	// Unexpected are writes from after the target that the restore contains.
	Unexpected []int64
	// LastPresent is the newest journal entry found in the restore.
	LastPresent *Entry
}

// OK reports whether the restore matches the target exactly and the journal
// held evidence on both sides of it.
func (p PITR) OK() bool {
	return p.MustExist > 0 && p.MustNotExist > 0 && len(p.Missing) == 0 && len(p.Unexpected) == 0
}

// CheckPITR evaluates entries against the writes present in a restore to
// target. Writes in flight within the tolerance of the target may land on
// either side and are not judged.
func CheckPITR(entries []Entry, target time.Time, tolerance time.Duration, present func(int64) bool) PITR {
	p := PITR{Target: target, Tolerance: tolerance}
	before, after := target.Add(-tolerance), target.Add(tolerance)
	for i := range entries {
		e := &entries[i]
		in := present(e.Seq)
		if in && (p.LastPresent == nil || e.Seq > p.LastPresent.Seq) {
			p.LastPresent = e
		}
		switch {
		case e.Acked.Before(before):
			p.MustExist++
			if !in {
				p.Missing = append(p.Missing, e.Seq)
			}
		case e.Sent.After(after):
			p.MustNotExist++
			if in {
				p.Unexpected = append(p.Unexpected, e.Seq)
			}
		}
	}
	return p
}

// CoverageStart returns when this journal starts to describe the database:
// the acknowledgement time of the first entry at or after firstPresent, the
// lowest journal sequence number the database contains. Older entries belong
// to a previous incarnation of the lab. Entries must be in ascending seq order.
func CoverageStart(entries []Entry, firstPresent int64) (time.Time, bool) {
	i := sort.Search(len(entries), func(i int) bool { return entries[i].Seq >= firstPresent })
	if i == len(entries) {
		return time.Time{}, false
	}
	return entries[i].Acked, true
}
