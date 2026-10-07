package store_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/oplosy/disavery/internal/docsvc/store"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test: needs Docker")
	}
	ctx := context.Background()
	ctr, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("docsvc"),
		postgres.WithUsername("docsvc"),
		postgres.WithPassword("docsvc"),
		postgres.BasicWaitStrategies(),
	)
	testcontainers.CleanupContainer(t, ctr)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	url, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	s, err := store.Open(ctx, url)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(s.Close)
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return s
}

func TestMigrateIsIdempotent(t *testing.T) {
	s := newStore(t)
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
}

func TestDocumentLifecycle(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	id := uuid.NewString()

	created, err := s.CreateDocument(ctx, id, "invoice", "documents/"+id)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ID != id || created.PaymentStatus != "pending" || created.AttachmentKey != "documents/"+id {
		t.Fatalf("unexpected document: %+v", created)
	}

	if err := s.SetPaymentStatus(ctx, id, "paid"); err != nil {
		t.Fatalf("set status: %v", err)
	}
	got, err := s.GetDocument(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.PaymentStatus != "paid" || got.UpdatedAt.Before(got.CreatedAt) {
		t.Fatalf("status not updated: %+v", got)
	}
}

func TestUnknownDocument(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	missing := uuid.NewString()
	if _, err := s.GetDocument(ctx, missing); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("get: want ErrNotFound, got %v", err)
	}
	if err := s.SetPaymentStatus(ctx, missing, "paid"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("set status: want ErrNotFound, got %v", err)
	}
}

func TestCanary(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	for _, seq := range []int64{1, 2, 3, 2} { // 2 twice: client retries must be idempotent
		if err := s.InsertCanary(ctx, seq); err != nil {
			t.Fatalf("insert %d: %v", seq, err)
		}
	}
	tests := []struct {
		from  int64
		limit int
		want  []int64
	}{
		{2, 10, []int64{2, 3}},
		{2, 1, []int64{2}},
		{10, 10, []int64{}},
	}
	for _, tc := range tests {
		got, err := s.CanarySeqs(ctx, tc.from, tc.limit)
		if err != nil {
			t.Fatalf("seqs: %v", err)
		}
		if got == nil || !slices.Equal(got, tc.want) {
			t.Fatalf("CanarySeqs(%d,%d) = %#v, want %#v", tc.from, tc.limit, got, tc.want)
		}
	}
}
