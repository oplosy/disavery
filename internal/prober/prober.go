// Package prober measures availability from the outside: every interval it
// checks readiness and does a document write/read round trip through the
// public endpoint, and turns the samples into the actual RTO (spec §6).
package prober

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"
)

// Sample is one probe. Latency is in nanoseconds in JSON.
type Sample struct {
	At      time.Time     `json:"at"`
	OK      bool          `json:"ok"`
	Latency time.Duration `json:"latency"`
	Err     string        `json:"err,omitempty"`
}

// Prober probes BaseURL every Interval.
type Prober struct {
	BaseURL  string // e.g. https://docs.disavery.test
	Client   *http.Client
	Interval time.Duration
	Timeout  time.Duration // per probe
	Now      func() time.Time
}

// Run probes until ctx is cancelled and hands every sample to record.
func (p *Prober) Run(ctx context.Context, record func(Sample)) {
	t := time.NewTicker(p.Interval)
	defer t.Stop()
	for {
		record(p.probe(ctx))
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (p *Prober) probe(ctx context.Context) Sample {
	ctx, cancel := context.WithTimeout(ctx, p.Timeout)
	defer cancel()
	start := p.Now()
	err := p.roundTrip(ctx)
	s := Sample{At: start, OK: err == nil, Latency: p.Now().Sub(start)}
	if err != nil {
		s.Err = err.Error()
	}
	return s
}

func (p *Prober) roundTrip(ctx context.Context) error {
	if err := p.do(ctx, http.MethodGet, "/readyz", nil, "", http.StatusOK, nil); err != nil {
		return fmt.Errorf("readyz: %w", err)
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("title", "prober")
	fw, err := mw.CreateFormFile("file", "probe.txt")
	if err != nil {
		return err
	}
	_, _ = fw.Write([]byte("probe"))
	if err := mw.Close(); err != nil {
		return err
	}
	var doc struct {
		ID string `json:"id"`
	}
	if err := p.do(ctx, http.MethodPost, "/documents", &body, mw.FormDataContentType(), http.StatusCreated, &doc); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	if err := p.do(ctx, http.MethodGet, "/documents/"+doc.ID, nil, "", http.StatusOK, nil); err != nil {
		return fmt.Errorf("read: %w", err)
	}
	return nil
}

func (p *Prober) do(ctx context.Context, method, path string, body io.Reader, contentType string, want int, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, p.BaseURL+path, body)
	if err != nil {
		return err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := p.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		_, _ = io.Copy(io.Discard, resp.Body)
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// RTO measures recovery after an incident: from the incident to the start of
// the first run of streak consecutive successful probes that follows the
// first failed probe. Measuring from the first failure keeps probes that still
// succeeded while the outage was taking effect from counting as recovery.
// down is false if no probe failed after the incident (RTO 0); ok is false if
// the service failed and never recovered within the samples.
func RTO(samples []Sample, incident time.Time, streak int) (rto time.Duration, down, ok bool) {
	run := 0
	var first time.Time
	for _, s := range samples {
		if s.At.Before(incident) {
			continue
		}
		if !s.OK {
			down, run = true, 0
			continue
		}
		if !down {
			continue
		}
		if run == 0 {
			first = s.At
		}
		run++
		if run == streak {
			return first.Sub(incident), true, true
		}
	}
	if !down {
		return 0, false, true
	}
	return 0, true, false
}

// Availability counts samples and failed samples.
func Availability(samples []Sample) (total, failed int) {
	for _, s := range samples {
		if !s.OK {
			failed++
		}
	}
	return len(samples), failed
}

// LongestOutage returns the longest run of failed probes, from its first
// failure to the next success. recovered is false if the last probe failed.
func LongestOutage(samples []Sample) (longest time.Duration, recovered bool) {
	var start time.Time
	inOutage := false
	for _, s := range samples {
		switch {
		case !s.OK && !inOutage:
			start, inOutage = s.At, true
		case s.OK && inOutage:
			longest, inOutage = max(longest, s.At.Sub(start)), false
		}
	}
	return longest, !inOutage
}
