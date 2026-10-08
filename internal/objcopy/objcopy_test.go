package objcopy_test

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/testcontainers/testcontainers-go"
	tcminio "github.com/testcontainers/testcontainers-go/modules/minio"

	"github.com/oplosy/disavery/internal/objcopy"
)

// startMinio starts a MinIO server for an integration test.
func startMinio(t *testing.T) *minio.Client {
	t.Helper()
	_, c := runMinio(t)
	return c
}

// runMinio starts a MinIO server with the given options and returns the
// container too, for tests that run mc inside it.
func runMinio(t *testing.T, opts ...testcontainers.ContainerCustomizer) (*tcminio.MinioContainer, *minio.Client) {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test: needs Docker")
	}
	ctx := context.Background()
	ctr, err := tcminio.Run(ctx, "disavery/minio:local", opts...) // built by `make minio-image`
	testcontainers.CleanupContainer(t, ctr)
	if err != nil {
		t.Fatalf("start minio (run `make minio-image` first): %v", err)
	}
	endpoint, _ := ctr.ConnectionString(ctx)
	c, err := minio.New(endpoint, &minio.Options{Creds: credentials.NewStaticV4(ctr.Username, ctr.Password, "")})
	if err != nil {
		t.Fatal(err)
	}
	return ctr, c
}

// TestCopyFromLockedBucket copies from a bucket with Object Lock and default
// compliance retention (like the vault) into a plain versioned bucket (like a
// site store): what `mc mirror` cannot do.
func TestCopyFromLockedBucket(t *testing.T) {
	ctx := context.Background()
	c := startMinio(t)
	if err := c.MakeBucket(ctx, "vault", minio.MakeBucketOptions{ObjectLocking: true}); err != nil {
		t.Fatal(err)
	}
	mode, days, unit := minio.Compliance, uint(1), minio.Days
	if err := c.SetObjectLockConfig(ctx, "vault", &mode, &days, &unit); err != nil {
		t.Fatal(err)
	}
	if err := c.MakeBucket(ctx, "site", minio.MakeBucketOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := c.EnableVersioning(ctx, "site"); err != nil {
		t.Fatal(err)
	}
	put := func(bucket, key, body string) {
		t.Helper()
		if _, err := c.PutObject(ctx, bucket, key, bytes.NewReader([]byte(body)), int64(len(body)), minio.PutObjectOptions{ContentType: "text/plain"}); err != nil {
			t.Fatal(err)
		}
	}
	put("vault", "documents/a", "alpha")
	put("vault", "documents/b", "bravo")
	put("vault", "documents/gone", "deleted later")
	put("vault", "other/x", "outside the prefix")
	if err := c.RemoveObject(ctx, "vault", "documents/gone", minio.RemoveObjectOptions{}); err != nil { // a delete marker; the version stays locked
		t.Fatal(err)
	}
	put("site", "documents/b", "bravo") // already there: skipped

	src, dst := objcopy.Store{Client: c, Bucket: "vault"}, objcopy.Store{Client: c, Bucket: "site"}
	cctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	st, err := objcopy.Copy(cctx, src, dst, "documents/", 4, nil)
	if err != nil || st.Copied != 1 || st.Skipped != 1 {
		t.Fatalf("stats %+v err %v", st, err)
	}
	r, err := c.GetObject(ctx, "site", "documents/a", minio.GetObjectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(r)
	r.Close()
	if string(body) != "alpha" {
		t.Fatalf("copied body %q", body)
	}
	for _, key := range []string{"documents/gone", "other/x"} {
		if _, err := c.StatObject(ctx, "site", key, minio.StatObjectOptions{}); err == nil {
			t.Fatalf("%s must not be copied", key)
		}
	}
	if st, err := objcopy.Copy(cctx, src, dst, "documents/", 4, nil); err != nil || st.Copied != 0 || st.Skipped != 2 {
		t.Fatalf("second copy %+v %v", st, err)
	}
}
