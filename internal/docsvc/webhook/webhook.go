// Package webhook talks to the payment provider: outgoing payment requests and
// HMAC-signed incoming callbacks.
package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// SignatureHeader carries the HMAC of a callback body.
const SignatureHeader = "X-Signature"

// PaymentRequest is sent to the provider when a document is created.
type PaymentRequest struct {
	DocumentID  string `json:"document_id"`
	CallbackURL string `json:"callback_url"`
}

// Callback is posted back by the provider.
type Callback struct {
	DocumentID string `json:"document_id"`
	Status     string `json:"status"`
}

// Sign returns the signature header value for body.
func Sign(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// Verify checks a signature header value in constant time.
func Verify(secret, body []byte, header string) bool {
	return hmac.Equal([]byte(Sign(secret, body)), []byte(header))
}

// Notifier sends payment requests. A zero URL disables it.
type Notifier struct {
	URL         string
	CallbackURL string
	Client      *http.Client
}

// DocumentCreated asks the provider to start a payment for document id.
func (n *Notifier) DocumentCreated(ctx context.Context, id string) error {
	if n.URL == "" {
		return nil
	}
	body, err := json.Marshal(PaymentRequest{DocumentID: id, CallbackURL: n.CallbackURL})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := n.Client.Do(req)
	if err != nil {
		return fmt.Errorf("payment webhook: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("payment webhook: unexpected status %d", resp.StatusCode)
	}
	return nil
}
