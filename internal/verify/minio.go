package verify

import (
	"context"

	"github.com/minio/minio-go/v7"
)

// MinioLister lists object versions in one bucket.
type MinioLister struct {
	Client *minio.Client
	Bucket string
}

// ListVersions implements ObjectLister.
func (m MinioLister) ListVersions(ctx context.Context, prefix string) ([]ObjectVersion, error) {
	ctx, cancel := context.WithCancel(ctx) // stops the listing goroutine on an early return
	defer cancel()
	var out []ObjectVersion
	for obj := range m.Client.ListObjects(ctx, m.Bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true, WithVersions: true}) {
		if obj.Err != nil {
			return nil, obj.Err
		}
		out = append(out, ObjectVersion{Key: obj.Key, Modified: obj.LastModified, DeleteMarker: obj.IsDeleteMarker, Latest: obj.IsLatest})
	}
	return out, nil
}
