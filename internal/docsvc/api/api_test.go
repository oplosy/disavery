package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/oplosy/disavery/internal/docsvc/api"
	"github.com/oplosy/disavery/internal/docsvc/blob"
	"github.com/oplosy/disavery/internal/docsvc/store"
	"github.com/oplosy/disavery/internal/docsvc/webhook"
)

var errDown = errors.New("down")

type fakeStore struct {
	mu     sync.Mutex
	docs   map[string]store.Document
	canary map[int64]bool
	err    error
}

func (f *fakeStore) CreateDocument(_ context.Context, id, title, key string) (store.Document, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return store.Document{}, f.err
	}
	now := time.Now()
	d := store.Document{ID: id, Title: title, AttachmentKey: key, PaymentStatus: "pending", CreatedAt: now, UpdatedAt: now}
	f.docs[id] = d
	return d, nil
}

func (f *fakeStore) GetDocument(_ context.Context, id string) (store.Document, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return store.Document{}, f.err
	}
	d, ok := f.docs[id]
	if !ok {
		return store.Document{}, store.ErrNotFound
	}
	return d, nil
}

func (f *fakeStore) SetPaymentStatus(_ context.Context, id, status string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	d, ok := f.docs[id]
	if !ok {
		return store.ErrNotFound
	}
	d.PaymentStatus = status
	f.docs[id] = d
	return nil
}

func (f *fakeStore) InsertCanary(_ context.Context, seq int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.canary[seq] = true
	return nil
}

func (f *fakeStore) CanarySeqs(_ context.Context, from int64, limit int) ([]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []int64{}
	for seq := range f.canary {
		if seq >= from {
			out = append(out, seq)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, f.err
}

func (f *fakeStore) Ping(context.Context) error { return f.err }

type fakeBlob struct {
	mu      sync.Mutex
	objects map[string][]byte
	err     error
}

func (f *fakeBlob) Put(_ context.Context, key string, r io.Reader, _ int64, _ string) error {
	if f.err != nil {
		return f.err
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objects[key] = data
	return nil
}

func (f *fakeBlob) Get(_ context.Context, key string) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	data, ok := f.objects[key]
	if !ok {
		return nil, blob.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (f *fakeBlob) Ready(context.Context) error { return f.err }

type fakeNotifier struct {
	ids []string
	err error
}

func (f *fakeNotifier) DocumentCreated(_ context.Context, id string) error {
	f.ids = append(f.ids, id)
	return f.err
}

const secret = "s3cret"

type harness struct {
	h        http.Handler
	store    *fakeStore
	blob     *fakeBlob
	notifier *fakeNotifier
}

func newHarness() *harness {
	h := &harness{
		store:    &fakeStore{docs: map[string]store.Document{}, canary: map[int64]bool{}},
		blob:     &fakeBlob{objects: map[string][]byte{}},
		notifier: &fakeNotifier{},
	}
	srv := &api.Server{
		Store: h.store, Blob: h.blob, Notifier: h.notifier,
		WebhookSecret: []byte(secret), MaxUploadBytes: 1024,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	h.h = srv.Handler()
	return h
}

func (h *harness) do(req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.h.ServeHTTP(rec, req)
	return rec
}

func uploadRequest(t *testing.T, title string, file []byte) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if title != "" {
		_ = mw.WriteField("title", title)
	}
	if file != nil {
		fw, err := mw.CreateFormFile("file", "doc.txt")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = fw.Write(file)
	}
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/documents", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return v
}

func (h *harness) createDoc(t *testing.T) store.Document {
	t.Helper()
	rec := h.do(uploadRequest(t, "invoice", []byte("pdf-bytes")))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	return decode[store.Document](t, rec)
}

func TestCreateDocument(t *testing.T) {
	h := newHarness()
	doc := h.createDoc(t)
	if _, err := uuid.Parse(doc.ID); err != nil || doc.Title != "invoice" || doc.PaymentStatus != "pending" {
		t.Fatalf("unexpected document: %+v", doc)
	}
	if string(h.blob.objects["documents/"+doc.ID]) != "pdf-bytes" {
		t.Fatal("attachment not stored under documents/<id>")
	}
	if !slices.Equal(h.notifier.ids, []string{doc.ID}) {
		t.Fatalf("notifier called with %v", h.notifier.ids)
	}
}

func TestCreateDocumentValidation(t *testing.T) {
	tests := []struct {
		name  string
		title string
		file  []byte
		code  int
	}{
		{"missing title", "", []byte("x"), http.StatusBadRequest},
		{"blank title", "   ", []byte("x"), http.StatusBadRequest},
		{"title too long", strings.Repeat("é", 201), []byte("x"), http.StatusBadRequest},
		{"missing file", "invoice", nil, http.StatusBadRequest},
		{"file too large", "invoice", bytes.Repeat([]byte("x"), 4096), http.StatusRequestEntityTooLarge},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness()
			rec := h.do(uploadRequest(t, tc.title, tc.file))
			if rec.Code != tc.code {
				t.Fatalf("code %d, want %d (%s)", rec.Code, tc.code, rec.Body)
			}
			if decode[map[string]string](t, rec)["error"] == "" {
				t.Fatal("missing JSON error message")
			}
			if len(h.store.docs) != 0 || len(h.blob.objects) != 0 {
				t.Fatal("rejected upload left data behind")
			}
		})
	}
}

func TestCreateDocumentBlobDown(t *testing.T) {
	h := newHarness()
	h.blob.err = errDown
	rec := h.do(uploadRequest(t, "invoice", []byte("x")))
	if rec.Code != http.StatusServiceUnavailable || len(h.store.docs) != 0 {
		t.Fatalf("code %d, docs %d", rec.Code, len(h.store.docs))
	}
}

func TestCreateDocumentNotifierFailureKeepsDocument(t *testing.T) {
	h := newHarness()
	h.notifier.err = errDown
	doc := h.createDoc(t)
	if doc.PaymentStatus != "pending" {
		t.Fatalf("status %q", doc.PaymentStatus)
	}
}

func TestGetDocument(t *testing.T) {
	h := newHarness()
	doc := h.createDoc(t)
	if rec := h.do(httptest.NewRequest(http.MethodGet, "/documents/"+doc.ID, nil)); rec.Code != http.StatusOK || decode[store.Document](t, rec).ID != doc.ID {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}
	for _, id := range []string{uuid.NewString(), "not-a-uuid"} {
		if rec := h.do(httptest.NewRequest(http.MethodGet, "/documents/"+id, nil)); rec.Code != http.StatusNotFound {
			t.Fatalf("get %s: %d", id, rec.Code)
		}
	}
}

func TestGetAttachment(t *testing.T) {
	h := newHarness()
	doc := h.createDoc(t)
	rec := h.do(httptest.NewRequest(http.MethodGet, "/documents/"+doc.ID+"/attachment", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "pdf-bytes" {
		t.Fatalf("attachment: %d %q", rec.Code, rec.Body)
	}
	delete(h.blob.objects, "documents/"+doc.ID) // DB row without object: a DR consistency failure
	rec = h.do(httptest.NewRequest(http.MethodGet, "/documents/"+doc.ID+"/attachment", nil))
	if rec.Code != http.StatusNotFound || decode[map[string]string](t, rec)["error"] != "attachment missing" {
		t.Fatalf("missing attachment: %d %s", rec.Code, rec.Body)
	}
}

func callbackRequest(body, signature string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/callbacks/payment", strings.NewReader(body))
	req.Header.Set(webhook.SignatureHeader, signature)
	return req
}

func signed(body string) *http.Request {
	return callbackRequest(body, webhook.Sign([]byte(secret), []byte(body)))
}

func TestPaymentCallback(t *testing.T) {
	h := newHarness()
	doc := h.createDoc(t)
	paid := `{"document_id":"` + doc.ID + `","status":"paid"}`

	if rec := h.do(signed(paid)); rec.Code != http.StatusNoContent {
		t.Fatalf("valid callback: %d %s", rec.Code, rec.Body)
	}
	if h.store.docs[doc.ID].PaymentStatus != "paid" {
		t.Fatal("status not updated")
	}
	if rec := h.do(signed(paid)); rec.Code != http.StatusNoContent {
		t.Fatalf("replayed callback: %d", rec.Code)
	}

	tests := []struct {
		name string
		req  *http.Request
		code int
	}{
		{"missing signature", callbackRequest(paid, ""), http.StatusUnauthorized},
		{"wrong signature", callbackRequest(paid, webhook.Sign([]byte("other"), []byte(paid))), http.StatusUnauthorized},
		{"unknown document", signed(`{"document_id":"` + uuid.NewString() + `","status":"paid"}`), http.StatusNotFound},
		{"invalid status", signed(`{"document_id":"` + doc.ID + `","status":"refunded"}`), http.StatusBadRequest},
		{"invalid id", signed(`{"document_id":"x","status":"paid"}`), http.StatusBadRequest},
		{"invalid json", signed(`{`), http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if rec := h.do(tc.req); rec.Code != tc.code {
				t.Fatalf("code %d, want %d (%s)", rec.Code, tc.code, rec.Body)
			}
		})
	}
}

func TestCanary(t *testing.T) {
	h := newHarness()
	for _, body := range []string{`{"seq":1}`, `{"seq":2}`, `{"seq":2}`} {
		if rec := h.do(httptest.NewRequest(http.MethodPost, "/canary", strings.NewReader(body))); rec.Code != http.StatusNoContent {
			t.Fatalf("write %s: %d", body, rec.Code)
		}
	}
	for _, body := range []string{`{"seq":0}`, `{"seq":-3}`, `{}`, `nope`} {
		if rec := h.do(httptest.NewRequest(http.MethodPost, "/canary", strings.NewReader(body))); rec.Code != http.StatusBadRequest {
			t.Fatalf("write %s: %d, want 400", body, rec.Code)
		}
	}
	rec := h.do(httptest.NewRequest(http.MethodGet, "/canary?from=2", nil))
	if got := decode[map[string][]int64](t, rec)["seqs"]; !slices.Equal(got, []int64{2}) {
		t.Fatalf("seqs %v", got)
	}
	rec = h.do(httptest.NewRequest(http.MethodGet, "/canary?from=99", nil))
	if !strings.Contains(rec.Body.String(), `"seqs":[]`) {
		t.Fatalf("empty list must be [], got %s", rec.Body)
	}
	if rec := h.do(httptest.NewRequest(http.MethodGet, "/canary?from=0", nil)); rec.Code != http.StatusBadRequest {
		t.Fatalf("from=0: %d", rec.Code)
	}
}

func TestHealthAndReadiness(t *testing.T) {
	h := newHarness()
	if rec := h.do(httptest.NewRequest(http.MethodGet, "/healthz", nil)); rec.Code != http.StatusOK {
		t.Fatalf("healthz %d", rec.Code)
	}
	if rec := h.do(httptest.NewRequest(http.MethodGet, "/readyz", nil)); rec.Code != http.StatusOK {
		t.Fatalf("readyz %d", rec.Code)
	}
	h.blob.err = errDown
	rec := h.do(httptest.NewRequest(http.MethodGet, "/readyz", nil))
	body := decode[struct {
		Checks map[string]string `json:"checks"`
	}](t, rec)
	if rec.Code != http.StatusServiceUnavailable || body.Checks["attachments"] != "down" || body.Checks["database"] != "ok" {
		t.Fatalf("readyz with blob down: %d %s", rec.Code, rec.Body)
	}
}
