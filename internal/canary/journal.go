// Package canary writes one acknowledged write per second through the public
// endpoint, journals every acknowledgement, and turns the journal plus the
// writes that survived into data-loss measurements (spec §6).
package canary

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Entry is one acknowledged canary write. Times are wall-clock readings of the
// toolbox host, the single clock every drill measurement uses.
type Entry struct {
	Seq   int64     `json:"seq"`
	Sent  time.Time `json:"sent"`
	Acked time.Time `json:"acked"`
}

// ReadJournal returns the entries in file order (ascending seq). A missing
// file is an empty journal; a torn last line (writer killed mid-write) is ignored.
func ReadJournal(path string) ([]Entry, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	lines := bytes.Split(data, []byte("\n"))
	out := make([]Entry, 0, len(lines))
	for i, line := range lines {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			if i == len(lines)-1 {
				break
			}
			return nil, fmt.Errorf("%s:%d: %w", path, i+1, err)
		}
		out = append(out, e)
	}
	return out, nil
}

// Journal appends entries to a file.
type Journal struct {
	f *os.File
}

// OpenJournal opens path for appending, creating it and its directory.
func OpenJournal(path string) (*Journal, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	return &Journal{f: f}, nil
}

// Append writes one entry as a JSON line.
func (j *Journal) Append(e Entry) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = j.f.Write(append(b, '\n'))
	return err
}

// Close closes the file.
func (j *Journal) Close() error { return j.f.Close() }
