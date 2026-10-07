package webhookmock_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oplosy/disavery/internal/docsvc/webhook"
	"github.com/oplosy/disavery/internal/webhookmock"
)

var secret = []byte("s3cret")

func newServer(t *testing.T, allow string) *webhookmock.Server {
	t.Helper()
	cidrs, err := webhookmock.ParseCIDRs(allow)
	if err != nil {
		t.Fatal(err)
	}
	return webhookmock.New(webhookmock.Config{
		AllowCIDRs: cidrs, Secret: secret, RetryBackoff: 10 * time.Millisecond, MaxAttempts: 3,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

func payment(remote, callbackURL string) *http.Request {
	body := `{"document_id":"doc-1","callback_url":"` + callbackURL + `"}`
	req := httptest.NewRequest(http.MethodPost, "/payments", strings.NewReader(body))
	req.RemoteAddr = remote
	return req
}

func TestAllowedRequestGetsSignedCallback(t *testing.T) {
	var got webhook.Callback
	var sigOK atomic.Bool
	cb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		sigOK.Store(webhook.Verify(secret, body, r.Header.Get(webhook.SignatureHeader)))
		_ = json.Unmarshal(body, &got)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer cb.Close()

	srv := newServer(t, "172.31.0.21/32")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, payment("172.31.0.21:40000", cb.URL))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("code %d", rec.Code)
	}
	srv.Wait()
	if !sigOK.Load() || got.DocumentID != "doc-1" || got.Status != "paid" {
		t.Fatalf("callback %+v, signature ok %v", got, sigOK.Load())
	}
}

func TestDisallowedSourceIsRejected(t *testing.T) {
	srv := newServer(t, "172.31.0.21/32")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, payment("172.31.0.31:40000", "http://unused"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code %d, want 403", rec.Code)
	}
}

func TestInvalidRequests(t *testing.T) {
	srv := newServer(t, "0.0.0.0/0")
	for _, cbURL := range []string{"", "ftp://x/cb", "not a url"} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, payment("10.0.0.1:1", cbURL))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("callback_url %q: code %d, want 400", cbURL, rec.Code)
		}
	}
}

func TestCallbackIsRetried(t *testing.T) {
	var attempts atomic.Int32
	cb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer cb.Close()

	srv := newServer(t, "10.0.0.0/8")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, payment("10.1.2.3:1", cb.URL))
	srv.Wait()
	if attempts.Load() != 2 {
		t.Fatalf("attempts %d, want 2", attempts.Load())
	}
}

func TestParseCIDRs(t *testing.T) {
	got, err := webhookmock.ParseCIDRs(" 172.31.0.21/32, 10.0.0.0/8 ")
	if err != nil || len(got) != 2 || got[1] != netip.MustParsePrefix("10.0.0.0/8") {
		t.Fatalf("got %v, %v", got, err)
	}
	for _, bad := range []string{"", " , ", "300.1.1.1/32", "10.0.0.1"} {
		if _, err := webhookmock.ParseCIDRs(bad); err == nil {
			t.Fatalf("ParseCIDRs(%q): want error", bad)
		}
	}
}
