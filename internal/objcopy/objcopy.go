// Package objcopy copies the current version of every object under a prefix
// from one S3 store to another. It exists because `mc mirror` and `mc cp`
// carry a source's Object Lock retention along, which a target bucket without
// Object Lock (a site store) rejects when the source is the vault.
package objcopy

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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
	// Replicas counts the copied objects written as replicas of a version the
	// vault already holds.
	Replicas int
}

// Copy copies every current object under prefix from src to dst with the given
// number of workers. Objects already in dst with the same size and ETag are
// skipped, so a repeated copy is cheap. Deleted objects (a delete marker as
// the current version) are not copied.
//
// What dst holds comes from listing it, not from asking for each object: a
// MinIO store with bucket replication answers a HEAD for an object it lacks
// from its replication target, so a wiped site store would claim to hold every
// attachment the vault has (found in S4).
//
// dst replicates to the vault, so a plain copy would reach the vault again as
// one more version of an object it already holds (ADR 0010). A version the
// vault holds is therefore written the way MinIO's replication writes it: as a
// replica with the source's version ID, which dst does not replicate further.
// Pass src as vault when copying from the vault; versions from another source
// are looked up in vault one by one, and those it lacks are copied as new
// versions that replicate as usual. A nil vault copies everything as new
// versions.
func Copy(ctx context.Context, src, dst Store, prefix string, workers int, vault *Store) (Stats, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	have := map[string]minio.ObjectInfo{}
	for obj := range dst.Client.ListObjects(ctx, dst.Bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if obj.Err != nil {
			return Stats{}, fmt.Errorf("list %s/%s: %w", dst.Bucket, prefix, obj.Err)
		}
		have[obj.Key] = obj
	}
	objects := src.Client.ListObjects(ctx, src.Bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true})

	var (
		copied, skipped, replicas atomic.Int64
		mu                        sync.Mutex
		errs                      []error
		wg                        sync.WaitGroup
	)
	jobs := make(chan minio.ObjectInfo)
	for range max(workers, 1) {
		wg.Go(func() {
			for obj := range jobs {
				if h, ok := have[obj.Key]; ok && h.Size == obj.Size && h.ETag == obj.ETag {
					skipped.Add(1)
					continue
				}
				replica, err := copyOne(ctx, src, dst, vault, obj)
				switch {
				case err != nil:
					mu.Lock()
					errs = append(errs, fmt.Errorf("%s: %w", obj.Key, err))
					mu.Unlock()
				default:
					copied.Add(1)
					if replica {
						replicas.Add(1)
					}
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
	st := Stats{Copied: int(copied.Load()), Skipped: int(skipped.Load()), Replicas: int(replicas.Load())}
	if listErr != nil {
		errs = append(errs, fmt.Errorf("list %s/%s: %w", src.Bucket, prefix, listErr))
	}
	return st, errors.Join(errs...)
}

// copyOne copies the current version of obj and reports whether it was
// written as a replica of a vault version.
func copyOne(ctx context.Context, src, dst Store, vault *Store, obj minio.ObjectInfo) (bool, error) {
	r, err := src.Client.GetObject(ctx, src.Bucket, obj.Key, minio.GetObjectOptions{})
	if err != nil {
		return false, err
	}
	defer r.Close()
	info, err := r.Stat()
	if err != nil {
		return false, err
	}
	replica, err := inVault(ctx, src, vault, info)
	if err != nil {
		return false, err
	}
	opts := minio.PutObjectOptions{ContentType: info.ContentType}
	if replica {
		// The headers MinIO's own replication sends. Without a source mtime the
		// replica is dated now, so it becomes current even over a newer delete
		// marker in dst. Multipart uploads are disabled because only the single
		// PUT has been verified to keep the version ID.
		opts.DisableMultipart = true
		opts.Internal = minio.AdvancedPutOptions{
			SourceVersionID:    info.VersionID,
			ReplicationStatus:  minio.ReplicationStatusReplica,
			ReplicationRequest: true,
		}
	}
	_, err = dst.Client.PutObject(ctx, dst.Bucket, obj.Key, r, info.Size, opts)
	return replica, err
}

// inVault reports whether the vault holds the source version described by
// info. The vault has no replication rules, so unlike a site store it answers
// a HEAD only for what it holds.
func inVault(ctx context.Context, src Store, vault *Store, info minio.ObjectInfo) (bool, error) {
	if vault == nil || info.VersionID == "" || info.VersionID == "null" {
		return false, nil
	}
	if *vault == src {
		return true, nil
	}
	_, err := vault.Client.StatObject(ctx, vault.Bucket, info.Key, minio.StatObjectOptions{VersionID: info.VersionID})
	if err == nil {
		return true, nil
	}
	if e := minio.ToErrorResponse(err); e.StatusCode == http.StatusNotFound && (e.Code == "NoSuchKey" || e.Code == "NoSuchVersion") {
		return false, nil
	}
	return false, fmt.Errorf("look up version %s in %s: %w", info.VersionID, vault.Bucket, err)
}
