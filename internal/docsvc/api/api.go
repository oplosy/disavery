// Package api implements the docsvc HTTP interface.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/oplosy/disavery/internal/docsvc/blob"
	"github.com/oplosy/disavery/internal/docsvc/store"
	"github.com/oplosy/disavery/internal/docsvc/webhook"
)

const (
	maxTitleRunes   = 200
	canaryListLimit = 100_000
)

// Store is the persistence the API needs; implemented by *store.Store.
type Store interface {
	CreateDocument(ctx context.Context, id, title, attachmentKey string) (store.Document, error)
	GetDocument(ctx context.Context, id string) (store.Document, error)
	SetPaymentStatus(ctx context.Context, id, status string) error
	InsertCanary(ctx context.Context, seq int64) error
	CanarySeqs(ctx context.Context, from int64, limit int) ([]int64, error)
	Ping(ctx context.Context) error
}

// Blob is the attachment storage the API needs; implemented by *blob.Store.
type Blob interface {
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	Ready(ctx context.Context) error
}

// Notifier informs the payment provider; implemented by *webhook.Notifier.
type Notifier interface {
	DocumentCreated(ctx context.Context, id string) error
}

// Server wires handlers to their dependencies.
type Server struct {
	Store          Store
	Blob           Blob
	Notifier       Notifier
	WebhookSecret  []byte
	MaxUploadBytes int64
	Log            *slog.Logger
}

// Handler returns the HTTP routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /readyz", s.readyz)
	mux.HandleFunc("POST /documents", s.createDocument)
	mux.HandleFunc("GET /documents/{id}", s.getDocument)
	mux.HandleFunc("GET /documents/{id}/attachment", s.getAttachment)
	mux.HandleFunc("POST /callbacks/payment", s.paymentCallback)
	mux.HandleFunc("POST /canary", s.writeCanary)
	mux.HandleFunc("GET /canary", s.listCanary)
	return mux
}

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	checks := map[string]string{"database": "ok", "attachments": "ok"}
	status := http.StatusOK
	if err := s.Store.Ping(ctx); err != nil {
		checks["database"] = err.Error()
		status = http.StatusServiceUnavailable
	}
	if err := s.Blob.Ready(ctx); err != nil {
		checks["attachments"] = err.Error()
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, map[string]any{"status": http.StatusText(status), "checks": checks})
}

func (s *Server) createDocument(w http.ResponseWriter, r *http.Request) {
	// Allow 1 MiB on top of the file limit for multipart framing and fields.
	r.Body = http.MaxBytesReader(w, r.Body, s.MaxUploadBytes+1<<20)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, "upload too large")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid multipart form")
		return
	}
	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" || utf8.RuneCountInString(title) > maxTitleRunes {
		writeError(w, http.StatusBadRequest, "title must be 1-200 characters")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file is required")
		return
	}
	defer file.Close()
	if header.Size > s.MaxUploadBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "upload too large")
		return
	}

	id := uuid.NewString()
	key := "documents/" + id
	contentType := header.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	// Object first, row second: a crash in between leaves an orphan object
	// (harmless, found by the consistency check) rather than a row whose
	// attachment is missing (user-visible).
	if err := s.Blob.Put(r.Context(), key, file, header.Size, contentType); err != nil {
		s.Log.Error("store attachment", "id", id, "err", err)
		writeError(w, http.StatusServiceUnavailable, "attachment storage unavailable")
		return
	}
	doc, err := s.Store.CreateDocument(r.Context(), id, title, key)
	if err != nil {
		s.Log.Error("create document", "id", id, "err", err)
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	if err := s.Notifier.DocumentCreated(r.Context(), doc.ID); err != nil {
		// The document stays "pending"; the provider can be retried out of band.
		s.Log.Warn("payment request failed", "id", doc.ID, "err", err)
	}
	writeJSON(w, http.StatusCreated, doc)
}

func (s *Server) lookupDocument(w http.ResponseWriter, r *http.Request) (store.Document, bool) {
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		writeError(w, http.StatusNotFound, "document not found")
		return store.Document{}, false
	}
	doc, err := s.Store.GetDocument(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "document not found")
		return store.Document{}, false
	}
	if err != nil {
		s.Log.Error("get document", "id", id, "err", err)
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return store.Document{}, false
	}
	return doc, true
}

func (s *Server) getDocument(w http.ResponseWriter, r *http.Request) {
	if doc, ok := s.lookupDocument(w, r); ok {
		writeJSON(w, http.StatusOK, doc)
	}
}

func (s *Server) getAttachment(w http.ResponseWriter, r *http.Request) {
	doc, ok := s.lookupDocument(w, r)
	if !ok {
		return
	}
	body, err := s.Blob.Get(r.Context(), doc.AttachmentKey)
	if errors.Is(err, blob.ErrNotFound) {
		writeError(w, http.StatusNotFound, "attachment missing")
		return
	}
	if err != nil {
		s.Log.Error("get attachment", "id", doc.ID, "err", err)
		writeError(w, http.StatusServiceUnavailable, "attachment storage unavailable")
		return
	}
	defer body.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	if _, err := io.Copy(w, body); err != nil {
		s.Log.Warn("stream attachment", "id", doc.ID, "err", err)
	}
}

func (s *Server) paymentCallback(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		writeError(w, http.StatusBadRequest, "unreadable body")
		return
	}
	if !webhook.Verify(s.WebhookSecret, body, r.Header.Get(webhook.SignatureHeader)) {
		writeError(w, http.StatusUnauthorized, "invalid signature")
		return
	}
	var cb webhook.Callback
	if err := json.Unmarshal(body, &cb); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if _, err := uuid.Parse(cb.DocumentID); err != nil {
		writeError(w, http.StatusBadRequest, "invalid document_id")
		return
	}
	if cb.Status != "paid" && cb.Status != "failed" {
		writeError(w, http.StatusBadRequest, "status must be paid or failed")
		return
	}
	err = s.Store.SetPaymentStatus(r.Context(), cb.DocumentID, cb.Status)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "document not found")
		return
	}
	if err != nil {
		s.Log.Error("set payment status", "id", cb.DocumentID, "err", err)
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) writeCanary(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Seq *int64 `json:"seq"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&req); err != nil || req.Seq == nil || *req.Seq <= 0 {
		writeError(w, http.StatusBadRequest, "seq must be a positive integer")
		return
	}
	if err := s.Store.InsertCanary(r.Context(), *req.Seq); err != nil {
		s.Log.Error("insert canary", "seq", *req.Seq, "err", err)
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listCanary(w http.ResponseWriter, r *http.Request) {
	from := int64(1)
	if v := r.URL.Query().Get("from"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "from must be a positive integer")
			return
		}
		from = n
	}
	seqs, err := s.Store.CanarySeqs(r.Context(), from, canaryListLimit)
	if err != nil {
		s.Log.Error("list canary", "err", err)
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	if seqs == nil {
		seqs = []int64{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"seqs": seqs})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
