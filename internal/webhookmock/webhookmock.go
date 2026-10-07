// Package webhookmock stands in for a third-party payment provider. Like a real
// provider it only accepts requests from allow-listed source addresses, which
// makes "update the provider's allowlist" a mandatory step after failover.
package webhookmock

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/oplosy/disavery/internal/docsvc/webhook"
)

// Config controls the mock's behaviour.
type Config struct {
	AllowCIDRs   []netip.Prefix
	Secret       []byte
	Delay        time.Duration // before the callback is sent
	RetryBackoff time.Duration
	MaxAttempts  int
	Client       *http.Client
	Log          *slog.Logger
}

// Server is the mock provider.
type Server struct {
	cfg Config
	wg  sync.WaitGroup
}

// New applies defaults and returns a server.
func New(cfg Config) *Server {
	if cfg.MaxAttempts == 0 {
		cfg.MaxAttempts = 5
	}
	if cfg.RetryBackoff == 0 {
		cfg.RetryBackoff = 2 * time.Second
	}
	if cfg.Client == nil {
		cfg.Client = &http.Client{Timeout: 5 * time.Second}
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return &Server{cfg: cfg}
}

// Handler returns the HTTP routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("POST /payments", s.payments)
	return mux
}

// Wait blocks until all pending callbacks have finished.
func (s *Server) Wait() { s.wg.Wait() }

func (s *Server) payments(w http.ResponseWriter, r *http.Request) {
	addr, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil || !s.allowed(addr.Addr().Unmap()) {
		s.cfg.Log.Warn("rejected payment request", "remote", r.RemoteAddr)
		http.Error(w, "source address not allow-listed", http.StatusForbidden)
		return
	}
	var req webhook.PaymentRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req); err != nil || req.DocumentID == "" {
		http.Error(w, "invalid payment request", http.StatusBadRequest)
		return
	}
	u, err := url.Parse(req.CallbackURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		http.Error(w, "invalid callback_url", http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusAccepted)

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		time.Sleep(s.cfg.Delay)
		s.callback(req)
	}()
}

func (s *Server) allowed(a netip.Addr) bool {
	for _, p := range s.cfg.AllowCIDRs {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

func (s *Server) callback(req webhook.PaymentRequest) {
	body, _ := json.Marshal(webhook.Callback{DocumentID: req.DocumentID, Status: "paid"})
	sig := webhook.Sign(s.cfg.Secret, body)
	for attempt := 1; attempt <= s.cfg.MaxAttempts; attempt++ {
		status, err := s.post(req.CallbackURL, body, sig)
		if err == nil && status < 300 {
			s.cfg.Log.Info("callback delivered", "document_id", req.DocumentID, "attempt", attempt)
			return
		}
		s.cfg.Log.Warn("callback failed", "document_id", req.DocumentID, "attempt", attempt, "status", status, "err", err)
		if attempt < s.cfg.MaxAttempts {
			time.Sleep(s.cfg.RetryBackoff)
		}
	}
	s.cfg.Log.Error("callback abandoned", "document_id", req.DocumentID)
}

func (s *Server) post(target string, body []byte, sig string) (int, error) {
	req, err := http.NewRequest(http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(webhook.SignatureHeader, sig)
	resp, err := s.cfg.Client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}

// ParseCIDRs parses a comma-separated list of prefixes; at least one is required.
func ParseCIDRs(s string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		p, err := netip.ParsePrefix(part)
		if err != nil {
			return nil, fmt.Errorf("parse %q: %w", part, err)
		}
		out = append(out, p.Masked())
	}
	if len(out) == 0 {
		return nil, errors.New("at least one CIDR is required")
	}
	return out, nil
}
