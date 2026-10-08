// Package vault attacks and repairs the immutable vault (S4). Attack uses the
// production writer's credentials, as ransomware holding a stolen key would,
// and records what the vault refused; Undelete removes the delete markers such
// an attack leaves behind, which only the vault administrator may do.
package vault

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/lifecycle"
)

// Attempt counts one kind of destructive request against one bucket.
type Attempt struct {
	Action string `json:"action"`
	Bucket string `json:"bucket"`
	Tried  int    `json:"tried"`
	Denied int    `json:"denied"`
	// Codes counts the S3 error codes the vault answered with.
	Codes map[string]int `json:"codes,omitempty"`
}

// Evidence is what an attack achieved.
type Evidence struct {
	StartedAt time.Time `json:"started_at"`
	Attempts  []Attempt `json:"attempts"`
	// Hidden counts the delete markers the attack placed. The writer may
	// place them (pgBackRest expires backups that way) and they destroy no
	// locked version, but they hide every backup from a restore until the
	// vault administrator removes them (ADR 0006).
	Hidden int `json:"hidden"`
}

// Breached lists the destructive actions the vault allowed at least once.
func (e Evidence) Breached() []string {
	var out []string
	for _, a := range e.Attempts {
		if a.Denied < a.Tried {
			out = append(out, fmt.Sprintf("%s on %s (%d of %d allowed)", a.Action, a.Bucket, a.Tried-a.Denied, a.Tried))
		}
	}
	return out
}

// workers bounds the concurrent requests of an attack or a repair. The vault
// holds tens of thousands of versions; done one by one, the attack took long
// enough to change S4's measured data loss.
const workers = 16

// Attack tries to destroy every bucket's contents and protection, then hides
// the current objects behind delete markers. Hiding comes last and fast: from
// the moment the markers cover the archive's metadata production can no longer
// archive WAL. A request the vault refuses counts as denied; an error that is
// not an S3 answer (the vault unreachable) aborts the attack, because it would
// prove nothing.
func Attack(ctx context.Context, c *minio.Client, buckets []string, now func() time.Time) (Evidence, error) {
	ev := Evidence{StartedAt: now().UTC()}
	all := map[string][]minio.ObjectInfo{}
	var infra []error
	for _, b := range buckets {
		versions, err := listVersions(ctx, c, b)
		if err != nil {
			return ev, err
		}
		if len(versions) == 0 {
			return ev, fmt.Errorf("bucket %s holds no object to attack", b)
		}
		all[b] = versions
		first := versions[0]
		try := func(action string, n int, call func(i int) error) {
			a := Attempt{Action: action, Bucket: b, Tried: n, Codes: map[string]int{}}
			for _, err := range each(n, call) {
				code := minio.ToErrorResponse(err).Code
				if code == "" {
					infra = append(infra, fmt.Errorf("%s on %s: %w", action, b, err))
					continue
				}
				a.Denied++
				a.Codes[code]++
			}
			if len(a.Codes) == 0 {
				a.Codes = nil
			}
			ev.Attempts = append(ev.Attempts, a)
		}
		try("delete-version", len(versions), func(i int) error {
			return c.RemoveObject(ctx, b, versions[i].Key, minio.RemoveObjectOptions{VersionID: versions[i].VersionID})
		})
		try("bypass-governance", 1, func(int) error {
			return c.RemoveObject(ctx, b, first.Key, minio.RemoveObjectOptions{VersionID: first.VersionID, GovernanceBypass: true})
		})
		try("shorten-retention", 1, func(int) error {
			mode, until := minio.Governance, now().Add(time.Minute)
			return c.PutObjectRetention(ctx, b, first.Key, minio.PutObjectRetentionOptions{
				VersionID: first.VersionID, Mode: &mode, RetainUntilDate: &until, GovernanceBypass: true})
		})
		try("remove-default-retention", 1, func(int) error { return c.SetObjectLockConfig(ctx, b, nil, nil, nil) })
		try("suspend-versioning", 1, func(int) error { return c.SuspendVersioning(ctx, b) })
		try("expire-everything", 1, func(int) error {
			cfg := lifecycle.NewConfiguration()
			cfg.Rules = []lifecycle.Rule{{ID: "ransom", Status: "Enabled",
				Expiration:                  lifecycle.Expiration{Days: 1},
				NoncurrentVersionExpiration: lifecycle.NoncurrentVersionExpiration{NoncurrentDays: 1}}}
			return c.SetBucketLifecycle(ctx, b, cfg)
		})
		try("delete-bucket", 1, func(int) error { return c.RemoveBucket(ctx, b) })
	}
	if err := errors.Join(infra...); err != nil {
		return ev, err
	}
	for _, b := range buckets {
		var current []minio.ObjectInfo
		for _, v := range all[b] {
			if v.IsLatest {
				current = append(current, v)
			}
		}
		if errs := each(len(current), func(i int) error {
			return c.RemoveObject(ctx, b, current[i].Key, minio.RemoveObjectOptions{})
		}); len(errs) > 0 {
			return ev, fmt.Errorf("hide objects in %s: %w", b, errors.Join(errs...))
		}
		ev.Hidden += len(current)
	}
	return ev, nil
}

// Undelete removes every delete marker placed in the buckets since `since`,
// so the versions they hid are current again; older markers (expired backups,
// deleted documents) stay. It returns how many markers it removed.
func Undelete(ctx context.Context, c *minio.Client, buckets []string, since time.Time) (int, error) {
	n := 0
	for _, b := range buckets {
		lctx, cancel := context.WithCancel(ctx) // stops the listing goroutine on an early return
		var markers []minio.ObjectInfo
		for obj := range c.ListObjects(lctx, b, minio.ListObjectsOptions{Recursive: true, WithVersions: true}) {
			if obj.Err != nil {
				cancel()
				return n, fmt.Errorf("list %s: %w", b, obj.Err)
			}
			if obj.IsDeleteMarker && !obj.LastModified.Before(since) {
				markers = append(markers, obj)
			}
		}
		cancel()
		if errs := each(len(markers), func(i int) error {
			return c.RemoveObject(ctx, b, markers[i].Key, minio.RemoveObjectOptions{VersionID: markers[i].VersionID})
		}); len(errs) > 0 {
			return n, fmt.Errorf("remove delete markers in %s: %w", b, errors.Join(errs...))
		}
		n += len(markers)
	}
	return n, nil
}

// each calls f for 0..n-1 on up to `workers` goroutines and returns the
// errors it returned.
func each(n int, f func(i int) error) []error {
	var (
		mu   sync.Mutex
		errs []error
		wg   sync.WaitGroup
	)
	idx := make(chan int)
	for range min(workers, max(n, 1)) {
		wg.Go(func() {
			for i := range idx {
				if err := f(i); err != nil {
					mu.Lock()
					errs = append(errs, err)
					mu.Unlock()
				}
			}
		})
	}
	for i := range n {
		idx <- i
	}
	close(idx)
	wg.Wait()
	return errs
}

// listVersions returns every object version (not delete markers) in a bucket,
// in key order.
func listVersions(ctx context.Context, c *minio.Client, bucket string) ([]minio.ObjectInfo, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var out []minio.ObjectInfo
	for obj := range c.ListObjects(ctx, bucket, minio.ListObjectsOptions{Recursive: true, WithVersions: true}) {
		if obj.Err != nil {
			return nil, fmt.Errorf("list %s: %w", bucket, obj.Err)
		}
		if !obj.IsDeleteMarker {
			out = append(out, obj)
		}
	}
	slices.SortStableFunc(out, func(a, b minio.ObjectInfo) int {
		if a.Key < b.Key {
			return -1
		}
		if a.Key > b.Key {
			return 1
		}
		return 0
	})
	return out, nil
}
