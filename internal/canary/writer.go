package canary

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Writer posts one canary write per interval to URL and journals every write
// the application acknowledged. Unacknowledged writes are only logged: they
// were never promised to anyone, so losing them is not data loss.
type Writer struct {
	URL      string // e.g. https://docs.disavery.test/canary
	Client   *http.Client
	Interval time.Duration
	Journal  *Journal
	Now      func() time.Time
	Log      *slog.Logger
}

// NextSeq picks the first sequence number for a (re)started writer: after the
// journal's last entry and never below the current Unix time in milliseconds,
// so a lost journal cannot reuse numbers that are already in the database.
func NextSeq(last int64, now time.Time) int64 { return max(last+1, now.UnixMilli()) }

// Run writes until ctx is cancelled. It returns an error only if the journal
// cannot be written, because measurements would silently become wrong.
func (w *Writer) Run(ctx context.Context, seq int64) error {
	t := time.NewTicker(w.Interval)
	defer t.Stop()
	for {
		if err := w.write(ctx, seq); err != nil {
			return err
		}
		seq++
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

func (w *Writer) write(ctx context.Context, seq int64) error {
	// A write slower than the interval counts as not acknowledged.
	ctx, cancel := context.WithTimeout(ctx, w.Interval)
	defer cancel()
	sent := w.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.URL, strings.NewReader(fmt.Sprintf(`{"seq":%d}`, seq)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := w.Client.Do(req)
	if err != nil {
		w.Log.Warn("canary write not acknowledged", "seq", seq, "err", err)
		return nil
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		w.Log.Warn("canary write not acknowledged", "seq", seq, "status", resp.StatusCode)
		return nil
	}
	if err := w.Journal.Append(Entry{Seq: seq, Sent: sent, Acked: w.Now()}); err != nil {
		return fmt.Errorf("journal seq %d: %w", seq, err)
	}
	return nil
}
