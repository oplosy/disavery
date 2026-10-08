package objcopy_test

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"

	"github.com/oplosy/disavery/internal/objcopy"
)

// TestRewind restores a versioned bucket to a point in time, as S3 does after
// a bad migration: deleted and overwritten objects get their old version back
// as a new version, newer objects stay, and a second rewind changes nothing.
func TestRewind(t *testing.T) {
	ctx := context.Background()
	c := startMinio(t)
	if err := c.MakeBucket(ctx, "site", minio.MakeBucketOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := c.EnableVersioning(ctx, "site"); err != nil {
		t.Fatal(err)
	}
	put := func(key, body string) {
		t.Helper()
		if _, err := c.PutObject(ctx, "site", key, bytes.NewReader([]byte(body)), int64(len(body)), minio.PutObjectOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	read := func(key string) (string, error) {
		r, err := c.GetObject(ctx, "site", key, minio.GetObjectOptions{})
		if err != nil {
			return "", err
		}
		defer r.Close()
		b, err := io.ReadAll(r)
		return string(b), err
	}
	put("documents/deleted", "keep me")
	put("documents/overwritten", "original")
	put("documents/untouched", "same")
	put("documents/gone-before", "old")
	if err := c.RemoveObject(ctx, "site", "documents/gone-before", minio.RemoveObjectOptions{}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond) // listings carry second or millisecond precision
	at := time.Now()
	time.Sleep(1100 * time.Millisecond)
	if err := c.RemoveObject(ctx, "site", "documents/deleted", minio.RemoveObjectOptions{}); err != nil {
		t.Fatal(err)
	}
	put("documents/overwritten", "migrated")
	put("documents/new", "created after the point in time")

	s := objcopy.Store{Client: c, Bucket: "site"}
	st, err := objcopy.Rewind(ctx, s, "documents/", at)
	if err != nil || st != (objcopy.RewindStats{Restored: 2, Unchanged: 2, Newer: 1}) {
		t.Fatalf("stats %+v err %v", st, err)
	}
	for key, want := range map[string]string{"documents/deleted": "keep me", "documents/overwritten": "original", "documents/new": "created after the point in time"} {
		if got, err := read(key); err != nil || got != want {
			t.Fatalf("%s = %q, %v; want %q", key, got, err, want)
		}
	}
	if _, err := read("documents/gone-before"); err == nil {
		t.Fatal("an object deleted before the point in time came back")
	}
	if st, err := objcopy.Rewind(ctx, s, "documents/", at); err != nil || st.Restored != 0 {
		t.Fatalf("second rewind %+v %v", st, err)
	}
}
