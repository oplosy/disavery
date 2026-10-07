// Package objcopy copies the current version of every object under a prefix
// from one S3 store to another. It exists because `mc mirror` and `mc cp`
// carry a source's Object Lock retention along, which a target bucket without
// Object Lock (a site store) rejects when the source is the vault.
package objcopy

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/minio/minio-go/v7"
)

// Store is a bucket on a server.
type Store struct {
	Client *minio.Client
	Bucket string
}

// Stats counts what a copy did.
type Stats struct {
	Copied, Skipped int
}

// Copy copies every current object under prefix from src to dst with the given
// number of workers. Objects already in dst with the same size and ETag are
// skipped, so a repeated copy is cheap. Deleted objects (a delete marker as
// the current version) are not copied.
func Copy(ctx context.Context, src, dst Store, prefix string, workers int) (Stats, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	objects := src.Client.ListObjects(ctx, src.Bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true})

	var (
		copied, skipped atomic.Int64
		mu              sync.Mutex
		errs            []error
		wg              sync.WaitGroup
	)
	jobs := make(chan minio.ObjectInfo)
	for range max(workers, 1) {
		wg.Go(func() {
			for obj := range jobs {
				done, err := copyOne(ctx, src, dst, obj)
				switch {
				case err != nil:
					mu.Lock()
					errs = append(errs, fmt.Errorf("%s: %w", obj.Key, err))
					mu.Unlock()
				case done:
					copied.Add(1)
				default:
					skipped.Add(1)
				}
			}
		})
	}
	var listErr error
	for obj := range objects {
		if obj.Err != nil {
			listErr = obj.Err
			break
		}
		jobs <- obj
	}
	close(jobs)
	wg.Wait()
	st := Stats{Copied: int(copied.Load()), Skipped: int(skipped.Load())}
	if listErr != nil {
		errs = append(errs, fmt.Errorf("list %s/%s: %w", src.Bucket, prefix, listErr))
	}
	return st, errors.Join(errs...)
}

func copyOne(ctx context.Context, src, dst Store, obj minio.ObjectInfo) (bool, error) {
	if have, err := dst.Client.StatObject(ctx, dst.Bucket, obj.Key, minio.StatObjectOptions{}); err == nil &&
		have.Size == obj.Size && have.ETag == obj.ETag {
		return false, nil
	}
	r, err := src.Client.GetObject(ctx, src.Bucket, obj.Key, minio.GetObjectOptions{})
	if err != nil {
		return false, err
	}
	defer r.Close()
	info, err := r.Stat()
	if err != nil {
		return false, err
	}
	_, err = dst.Client.PutObject(ctx, dst.Bucket, obj.Key, r, info.Size, minio.PutObjectOptions{ContentType: info.ContentType})
	return err == nil, err
}
