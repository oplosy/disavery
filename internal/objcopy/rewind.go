package objcopy

import (
	"context"
	"fmt"
	"time"

	"github.com/minio/minio-go/v7"
)

// RewindStats counts what a rewind did.
type RewindStats struct {
	// Restored objects were deleted or overwritten after the point in time
	// and got that moment's version back.
	Restored int
	// Unchanged objects were already current as of the point in time.
	Unchanged int
	// Newer objects did not exist at the point in time and were left alone.
	Newer int
}

// Rewind makes every object under prefix in a versioned bucket current as it
// was at `at` (S3, a bad migration that deleted attachments). An object
// deleted or overwritten since then gets its version from that moment back as
// a new current version. Copying, instead of removing newer versions or delete
// markers, keeps the history intact and replicates to the vault like any other
// write. Objects created after `at`, and objects already deleted at `at`, are
// left alone: to the rewound database they are orphans, which are harmless.
func Rewind(ctx context.Context, s Store, prefix string, at time.Time) (RewindStats, error) {
	ctx, cancel := context.WithCancel(ctx) // stops the listing goroutine on an early return
	defer cancel()
	type state struct{ latest, atTime *minio.ObjectInfo }
	keys := map[string]*state{}
	var order []string
	for obj := range s.Client.ListObjects(ctx, s.Bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true, WithVersions: true}) {
		if obj.Err != nil {
			return RewindStats{}, fmt.Errorf("list %s/%s: %w", s.Bucket, prefix, obj.Err)
		}
		st, ok := keys[obj.Key]
		if !ok {
			st = &state{}
			keys[obj.Key] = st
			order = append(order, obj.Key)
		}
		v := obj
		if v.IsLatest {
			st.latest = &v
		}
		if !v.LastModified.After(at) && (st.atTime == nil || v.LastModified.After(st.atTime.LastModified)) {
			st.atTime = &v
		}
	}
	var stats RewindStats
	for _, key := range order {
		st := keys[key]
		switch {
		case st.atTime == nil:
			stats.Newer++
		// Same content counts as current, so a repeated rewind copies nothing.
		case st.atTime.IsDeleteMarker, st.latest != nil && !st.latest.IsDeleteMarker && st.latest.ETag == st.atTime.ETag:
			stats.Unchanged++
		default:
			_, err := s.Client.CopyObject(ctx,
				minio.CopyDestOptions{Bucket: s.Bucket, Object: key},
				minio.CopySrcOptions{Bucket: s.Bucket, Object: key, VersionID: st.atTime.VersionID})
			if err != nil {
				return stats, fmt.Errorf("restore %s version %s: %w", key, st.atTime.VersionID, err)
			}
			stats.Restored++
		}
	}
	return stats, nil
}
