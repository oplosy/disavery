package blob_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/testcontainers/testcontainers-go"
	tcminio "github.com/testcontainers/testcontainers-go/modules/minio"

	"github.com/oplosy/disavery/internal/docsvc/blob"
)

// minioImage is built locally from pinned sources by `make minio-image`.
const minioImage = "disavery/minio:local"

type env struct {
	endpoint, user, password string
}

func startMinIO(t *testing.T) env {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test: needs Docker")
	}
	ctx := context.Background()
	ctr, err := tcminio.Run(ctx, minioImage)
	testcontainers.CleanupContainer(t, ctr)
	if err != nil {
		t.Fatalf("start minio (run `make minio-image` first): %v", err)
	}
	endpoint, err := ctr.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("endpoint: %v", err)
	}
	admin, err := minio.New(endpoint, &minio.Options{Creds: credentials.NewStaticV4(ctr.Username, ctr.Password, "")})
	if err != nil {
		t.Fatalf("admin client: %v", err)
	}
	if err := admin.MakeBucket(ctx, "attachments", minio.MakeBucketOptions{}); err != nil {
		t.Fatalf("make bucket: %v", err)
	}
	return env{endpoint, ctr.Username, ctr.Password}
}

func TestPutGetRoundTrip(t *testing.T) {
	e := startMinIO(t)
	b, err := blob.New(e.endpoint, e.user, e.password, "attachments", false)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	data := []byte("hello attachment")
	if err := b.Put(ctx, "documents/x", bytes.NewReader(data), int64(len(data)), "text/plain"); err != nil {
		t.Fatalf("put: %v", err)
	}
	rc, err := b.Get(ctx, "documents/x")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if !bytes.Equal(got, data) {
		t.Fatalf("got %q, want %q", got, data)
	}
}

func TestGetMissing(t *testing.T) {
	e := startMinIO(t)
	b, _ := blob.New(e.endpoint, e.user, e.password, "attachments", false)
	if _, err := b.Get(context.Background(), "documents/missing"); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestReady(t *testing.T) {
	e := startMinIO(t)
	ok, _ := blob.New(e.endpoint, e.user, e.password, "attachments", false)
	if err := ok.Ready(context.Background()); err != nil {
		t.Fatalf("ready: %v", err)
	}
	missing, _ := blob.New(e.endpoint, e.user, e.password, "nope", false)
	if err := missing.Ready(context.Background()); err == nil {
		t.Fatal("want error for missing bucket")
	}
}
