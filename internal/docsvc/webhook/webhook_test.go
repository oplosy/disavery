package webhook_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/oplosy/disavery/internal/docsvc/webhook"
)

func TestSignVerify(t *testing.T) {
	secret, body := []byte("s3cret"), []byte(`{"document_id":"x","status":"paid"}`)
	sig := webhook.Sign(secret, body)
	if !webhook.Verify(secret, body, sig) {
		t.Fatal("valid signature rejected")
	}
	if webhook.Verify(secret, []byte(`{"document_id":"x","status":"failed"}`), sig) {
		t.Fatal("tampered body accepted")
	}
	if webhook.Verify([]byte("other"), body, sig) {
		t.Fatal("wrong secret accepted")
	}
	if webhook.Verify(secret, body, "") {
		t.Fatal("missing signature accepted")
	}
}

func TestNotifierPostsPaymentRequest(t *testing.T) {
	var got webhook.PaymentRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("unexpected request %s %s", r.Method, r.Header.Get("Content-Type"))
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	n := &webhook.Notifier{URL: srv.URL, CallbackURL: "https://docs.disavery.test/callbacks/payment", Client: srv.Client()}
	if err := n.DocumentCreated(context.Background(), "doc-1"); err != nil {
		t.Fatalf("notify: %v", err)
	}
	if got.DocumentID != "doc-1" || got.CallbackURL != "https://docs.disavery.test/callbacks/payment" {
		t.Fatalf("unexpected payload: %+v", got)
	}
}

func TestNotifierRejectsUnexpectedStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	n := &webhook.Notifier{URL: srv.URL, CallbackURL: "https://x/cb", Client: srv.Client()}
	if err := n.DocumentCreated(context.Background(), "doc-1"); err == nil {
		t.Fatal("want error for 403")
	}
}

func TestNotifierDisabledWithoutURL(t *testing.T) {
	n := &webhook.Notifier{}
	if err := n.DocumentCreated(context.Background(), "doc-1"); err != nil {
		t.Fatalf("disabled notifier returned %v", err)
	}
}
