package verify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"time"
)

// Topology checks which site serves and which runs a standby, as the
// inventory says. Params: active_site, standby_site (may be empty). Used in
// runbook preflight, so a drill starts from the state it expects.
type Topology struct {
	Current func() (active, standby string, err error)
}

// Run implements Check.
func (c Topology) Run(_ context.Context, p Params) (Result, error) {
	want, ok := p.With["active_site"]
	if !ok {
		return Result{}, fmt.Errorf("missing parameter(s): active_site")
	}
	wantStandby := p.With["standby_site"]
	active, standby, err := c.Current()
	if err != nil {
		return Result{}, err
	}
	desc := func(a, s string) string {
		if s == "" {
			return fmt.Sprintf("site %s active, no standby", a)
		}
		return fmt.Sprintf("site %s active, site %s standby", a, s)
	}
	if active != want || standby != wantStandby {
		return Result{Status: Fail, Summary: fmt.Sprintf("lab is %s, the runbook expects %s", desc(active, standby), desc(want, wantStandby))}, nil
	}
	return Result{Status: Pass, Summary: desc(active, standby)}, nil
}

// Replication checks a standby streams from the primary within a lag.
// Params: primary (host), standby (its application_name, the node name);
// optional max_lag (default 5s).
type Replication struct {
	SQL SQL
}

// Run implements Check.
func (c Replication) Run(ctx context.Context, p Params) (Result, error) {
	if err := p.Require("primary", "standby"); err != nil {
		return Result{}, err
	}
	maxLag, err := p.Duration("max_lag", 5*time.Second)
	if err != nil {
		return Result{}, err
	}
	rows, err := c.SQL.Query(ctx, p.With["primary"], `SELECT application_name, state,
  coalesce(extract(epoch FROM replay_lag), 0)
FROM pg_stat_replication`)
	if err != nil {
		return Result{}, err
	}
	for _, r := range rows {
		if len(r) != 3 || r[0] != p.With["standby"] {
			continue
		}
		secs, err := strconv.ParseFloat(r[2], 64)
		if err != nil {
			return Result{}, err
		}
		lag := time.Duration(secs * float64(time.Second))
		res := Result{Metrics: map[string]float64{"replay_lag_seconds": secs}}
		switch {
		case r[1] != "streaming":
			res.Status, res.Summary = Fail, fmt.Sprintf("%s is %s, not streaming", r[0], r[1])
		case lag > maxLag:
			res.Status, res.Summary = Fail, fmt.Sprintf("%s replays %s behind (limit %s)", r[0], lag.Round(time.Millisecond), maxLag)
		default:
			res.Status, res.Summary = Pass, fmt.Sprintf("%s streams from %s, replay lag %s", r[0], p.With["primary"], lag.Round(time.Millisecond))
		}
		return res, nil
	}
	return Result{Status: Fail, Summary: fmt.Sprintf("%s is not connected to %s", p.With["standby"], p.With["primary"])}, nil
}

// WebhookRoundTrip creates a document through the public endpoint and waits
// for the payment provider's signed callback to mark it paid. It proves the
// provider's allowlist, DNS, TLS and the HMAC secret all follow a failover.
// Optional param: timeout (default 30s).
type WebhookRoundTrip struct {
	BaseURL string
	Client  func(ctx context.Context) (*http.Client, error)
	Poll    time.Duration
}

// Run implements Check.
func (c WebhookRoundTrip) Run(ctx context.Context, p Params) (Result, error) {
	timeout, err := p.Duration("timeout", 30*time.Second)
	if err != nil {
		return Result{}, err
	}
	client, err := c.Client(ctx)
	if err != nil {
		return Result{}, err
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("title", "webhook round trip")
	fw, err := mw.CreateFormFile("file", "check.txt")
	if err != nil {
		return Result{}, err
	}
	_, _ = fw.Write([]byte("webhook round trip"))
	if err := mw.Close(); err != nil {
		return Result{}, err
	}
	var doc struct {
		ID            string `json:"id"`
		PaymentStatus string `json:"payment_status"`
	}
	if err := c.call(ctx, client, http.MethodPost, "/documents", &body, mw.FormDataContentType(), http.StatusCreated, &doc); err != nil {
		return Result{}, fmt.Errorf("create document: %w", err)
	}
	start := time.Now()
	for {
		if doc.PaymentStatus == "paid" {
			waited := time.Since(start)
			return Result{Status: Pass, Summary: fmt.Sprintf("document %s marked paid by the provider's callback after %s", doc.ID, waited.Round(10*time.Millisecond)),
				Metrics: map[string]float64{"callback_seconds": waited.Seconds()}}, nil
		}
		if time.Since(start) > timeout {
			return Result{Status: Fail, Summary: fmt.Sprintf("document %s is still %q after %s: the payment callback did not arrive", doc.ID, doc.PaymentStatus, timeout)}, nil
		}
		select {
		case <-ctx.Done():
			return Result{}, ctx.Err()
		case <-time.After(c.Poll):
		}
		if err := c.call(ctx, client, http.MethodGet, "/documents/"+doc.ID, nil, "", http.StatusOK, &doc); err != nil {
			return Result{}, fmt.Errorf("read document: %w", err)
		}
	}
}

func (c WebhookRoundTrip) call(ctx context.Context, client *http.Client, method, path string, body io.Reader, contentType string, want int, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// StoreSync checks a standby attachment store holds every current object of
// the active one. Params: source, replica (store names); optional grace
// (default 60s): newer objects may still be replicating.
type StoreSync struct {
	Stores func(ctx context.Context, name string) (ObjectLister, error)
}

// Run implements Check.
func (c StoreSync) Run(ctx context.Context, p Params) (Result, error) {
	if err := p.Require("source", "replica"); err != nil {
		return Result{}, err
	}
	grace, err := p.Duration("grace", time.Minute)
	if err != nil {
		return Result{}, err
	}
	current := func(name string) (map[string]time.Time, error) {
		s, err := c.Stores(ctx, name)
		if err != nil {
			return nil, err
		}
		versions, err := s.ListVersions(ctx, "documents/")
		if err != nil {
			return nil, err
		}
		out := map[string]time.Time{}
		for _, v := range versions {
			if v.Latest && !v.DeleteMarker {
				out[v.Key] = v.Modified
			}
		}
		return out, nil
	}
	src, err := current(p.With["source"])
	if err != nil {
		return Result{}, err
	}
	dst, err := current(p.With["replica"])
	if err != nil {
		return Result{}, err
	}
	cutoff := p.Now.Add(-grace)
	var missing []string
	for k, mod := range src {
		if _, ok := dst[k]; !ok && mod.Before(cutoff) {
			missing = append(missing, k)
		}
	}
	res := Result{Metrics: map[string]float64{"source_objects": float64(len(src)), "missing": float64(len(missing))},
		Details: details("missing in "+p.With["replica"], missing, 10)}
	if len(missing) > 0 {
		res.Status, res.Summary = Fail, fmt.Sprintf("%s lacks %d of %d attachments older than %s", p.With["replica"], len(missing), len(src), grace)
		return res, nil
	}
	res.Status, res.Summary = Pass, fmt.Sprintf("%s holds all %d attachments of %s", p.With["replica"], len(src), p.With["source"])
	return res, nil
}
