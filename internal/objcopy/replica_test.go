package objcopy_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
	"github.com/testcontainers/testcontainers-go/network"

	"github.com/oplosy/disavery/internal/objcopy"
)

// TestCopyBackKeepsVaultVersions refills a site store that replicates to a
// vault, as S4 (from the vault) and S7 (from the other site) do: versions the
// vault holds come back as replicas with the vault's version ID and do not
// reach the vault again; a version the vault lacks is copied as a new version
// and replicates as usual (ADR 0010).
func TestCopyBackKeepsVaultVersions(t *testing.T) {
	ctx := context.Background()
	if testing.Short() {
		t.Skip("integration test: needs Docker")
	}
	nw, err := network.New(ctx)
	testcontainers.CleanupNetwork(t, nw)
	if err != nil {
		t.Fatal(err)
	}
	vaultCtr, vc := runMinio(t, network.WithNetwork([]string{"vault"}, nw))
	siteCtr, sc := runMinio(t, network.WithNetwork([]string{"site"}, nw))

	if err := vc.MakeBucket(ctx, "attachments", minio.MakeBucketOptions{ObjectLocking: true}); err != nil {
		t.Fatal(err)
	}
	mode, days, unit := minio.Compliance, uint(1), minio.Days
	if err := vc.SetObjectLockConfig(ctx, "attachments", &mode, &days, &unit); err != nil {
		t.Fatal(err)
	}
	for _, b := range []string{"attachments", "other"} { // other: another site's store, without replication
		if err := sc.MakeBucket(ctx, b, minio.MakeBucketOptions{}); err != nil {
			t.Fatal(err)
		}
		if err := sc.EnableVersioning(ctx, b); err != nil {
			t.Fatal(err)
		}
	}
	mc := func(args ...string) {
		t.Helper()
		code, out, err := siteCtr.Exec(ctx, append([]string{"mc"}, args...), tcexec.Multiplexed())
		if err != nil || code != 0 {
			b, _ := io.ReadAll(out)
			t.Fatalf("mc %v: exit %d, %v: %s", args, code, err, b)
		}
	}
	mc("alias", "set", "local", "http://localhost:9000", siteCtr.Username, siteCtr.Password)
	mc("replicate", "add", "local/attachments",
		"--remote-bucket", fmt.Sprintf("http://%s:%s@vault:9000/attachments", vaultCtr.Username, vaultCtr.Password),
		"--replicate", "delete-marker,existing-objects", "--priority", "1")

	versions := func(c *minio.Client, bucket, key string) []minio.ObjectInfo {
		t.Helper()
		var out []minio.ObjectInfo
		for o := range c.ListObjects(ctx, bucket, minio.ListObjectsOptions{Prefix: key, WithVersions: true}) {
			if o.Err != nil {
				t.Fatal(o.Err)
			}
			out = append(out, o)
		}
		return out
	}
	waitInVault := func(key string) {
		t.Helper()
		for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); time.Sleep(500 * time.Millisecond) {
			if len(versions(vc, "attachments", key)) > 0 {
				return
			}
		}
		t.Fatalf("%s never replicated to the vault", key)
	}
	put := func(bucket, key, body string) {
		t.Helper()
		if _, err := sc.PutObject(ctx, bucket, key, bytes.NewReader([]byte(body)), int64(len(body)), minio.PutObjectOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	wipe := func(bucket string) { // version deletes: not replicated, like the S4 attacker's
		t.Helper()
		for _, v := range versions(sc, bucket, "documents/") {
			if err := sc.RemoveObject(ctx, bucket, v.Key, minio.RemoveObjectOptions{VersionID: v.VersionID}); err != nil {
				t.Fatal(err)
			}
		}
	}

	put("attachments", "documents/a", "alpha")
	put("attachments", "documents/b", "bravo")
	waitInVault("documents/a")
	waitInVault("documents/b")
	vaultVersion := versions(vc, "attachments", "documents/a")[0].VersionID
	wipe("attachments")

	site := objcopy.Store{Client: sc, Bucket: "attachments"}
	vault := objcopy.Store{Client: vc, Bucket: "attachments"}
	other := objcopy.Store{Client: sc, Bucket: "other"}
	cctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	// S4: refill the wiped store from the vault.
	if st, err := objcopy.Copy(cctx, vault, site, "documents/", 4, &vault); err != nil || st != (objcopy.Stats{Copied: 2, Replicas: 2}) {
		t.Fatalf("copy from the vault: %+v %v", st, err)
	}
	info, err := sc.StatObject(ctx, "attachments", "documents/a", minio.StatObjectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if info.VersionID != vaultVersion || info.ReplicationStatus != string(minio.ReplicationStatusReplica) {
		t.Fatalf("site copy is version %s (%s), want the vault's %s as a replica", info.VersionID, info.ReplicationStatus, vaultVersion)
	}

	// S7: the other site holds the same versions and one the vault lacks.
	if st, err := objcopy.Copy(cctx, site, other, "documents/", 4, &vault); err != nil || st.Replicas != 2 {
		t.Fatalf("copy to the other site: %+v %v", st, err)
	}
	put("other", "documents/c", "charlie")
	wipe("attachments")
	if st, err := objcopy.Copy(cctx, other, site, "documents/", 4, &vault); err != nil || st != (objcopy.Stats{Copied: 3, Replicas: 2}) {
		t.Fatalf("copy from the other site: %+v %v", st, err)
	}

	// c reaching the vault shows replication ran after the copies.
	waitInVault("documents/c")
	for _, key := range []string{"documents/a", "documents/b", "documents/c"} {
		if n := len(versions(vc, "attachments", key)); n != 1 {
			t.Errorf("vault holds %d versions of %s, want 1", n, key)
		}
	}
}
