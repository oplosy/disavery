package vault_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/testcontainers/testcontainers-go"
	tcminio "github.com/testcontainers/testcontainers-go/modules/minio"

	"github.com/oplosy/disavery/internal/vault"
)

// TestAttackAndUndelete attacks a locked bucket with the lab's real
// production writer policy: every destructive request is refused, the delete
// markers it may place hide the objects, and only the administrator can
// remove those markers again.
func TestAttackAndUndelete(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: needs Docker")
	}
	ctx := context.Background()
	ctr, err := tcminio.Run(ctx, "disavery/minio:local") // built by `make minio-image`
	testcontainers.CleanupContainer(t, ctr)
	if err != nil {
		t.Fatalf("start minio (run `make minio-image` first): %v", err)
	}
	if err := ctr.CopyFileToContainer(ctx, "../../infra/ansible/roles/minio/templates/prod-writer.json.j2", "/tmp/prod-writer.json", 0o644); err != nil {
		t.Fatal(err)
	}
	setup := "mc alias set local http://localhost:9000 " + ctr.Username + " " + ctr.Password +
		" && mc admin policy create local prod-writer /tmp/prod-writer.json" +
		" && mc admin user add local prodwriter prodwriter-secret" +
		" && mc admin policy attach local prod-writer --user prodwriter"
	if code, out, err := ctr.Exec(ctx, []string{"sh", "-c", setup}); err != nil || code != 0 {
		t.Fatalf("create the writer: %d %v %v", code, out, err)
	}
	endpoint, _ := ctr.ConnectionString(ctx)
	client := func(user, pass string) *minio.Client {
		c, err := minio.New(endpoint, &minio.Options{Creds: credentials.NewStaticV4(user, pass, "")})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	admin, writer := client(ctr.Username, ctr.Password), client("prodwriter", "prodwriter-secret")

	if err := admin.MakeBucket(ctx, "pgbackrest", minio.MakeBucketOptions{ObjectLocking: true}); err != nil {
		t.Fatal(err)
	}
	mode, days, unit := minio.Compliance, uint(1), minio.Days
	if err := admin.SetObjectLockConfig(ctx, "pgbackrest", &mode, &days, &unit); err != nil {
		t.Fatal(err)
	}
	put := func(key string) {
		t.Helper()
		if _, err := writer.PutObject(ctx, "pgbackrest", key, bytes.NewReader([]byte(key)), int64(len(key)), minio.PutObjectOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	current := func() int {
		n := 0
		for obj := range admin.ListObjects(ctx, "pgbackrest", minio.ListObjectsOptions{Recursive: true}) {
			if obj.Err != nil {
				t.Fatal(obj.Err)
			}
			n++
		}
		return n
	}
	put("repo/backup.info")
	put("repo/archive/000000010000000000000001")
	put("repo/expired")
	// An earlier, legitimate deletion (pgBackRest expiring a backup).
	if err := writer.RemoveObject(ctx, "pgbackrest", "repo/expired", minio.RemoveObjectOptions{}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)

	ev, err := vault.Attack(ctx, writer, []string{"pgbackrest"}, time.Now)
	if err != nil {
		t.Fatalf("attack: %v", err)
	}
	if b := ev.Breached(); len(b) != 0 {
		t.Fatalf("the vault allowed: %v (%+v)", b, ev.Attempts)
	}
	if len(ev.Attempts) != 7 || ev.Attempts[0].Action != "delete-version" || ev.Attempts[0].Tried != 3 {
		t.Fatalf("attempts %+v", ev.Attempts)
	}
	if ev.Hidden != 2 || current() != 0 {
		t.Fatalf("hidden %d, %d objects still visible", ev.Hidden, current())
	}

	if _, err := vault.Undelete(ctx, writer, []string{"pgbackrest"}, ev.StartedAt); err == nil ||
		!strings.Contains(err.Error(), "Access Denied") {
		t.Fatalf("the writer removed delete markers: %v", err)
	}
	n, err := vault.Undelete(ctx, admin, []string{"pgbackrest"}, ev.StartedAt)
	if err != nil || n != 2 || current() != 2 {
		t.Fatalf("undelete removed %d markers (%v); %d objects visible", n, err, current())
	}
	if _, err := admin.StatObject(ctx, "pgbackrest", "repo/expired", minio.StatObjectOptions{}); err == nil {
		t.Fatal("an object deleted before the attack came back")
	}
}
