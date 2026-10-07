package verify_test

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/testcontainers/testcontainers-go"
	tcminio "github.com/testcontainers/testcontainers-go/modules/minio"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/oplosy/disavery/internal/canary"
	"github.com/oplosy/disavery/internal/docsvc/store"
	"github.com/oplosy/disavery/internal/verify"
)

// pgxSQL implements verify.SQL against real databases, returning text values
// exactly as psql would. Hosts map to pools.
type pgxSQL map[string]*pgxpool.Pool

func (p pgxSQL) Query(ctx context.Context, host, q string) ([][]string, error) {
	pool, ok := p[host]
	if !ok {
		return nil, fmt.Errorf("unknown host %s", host)
	}
	rows, err := pool.Query(ctx, q, pgx.QueryResultFormats{pgx.TextFormatCode})
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][]string
	for rows.Next() {
		raw := rows.RawValues()
		row := make([]string, len(raw))
		for i, v := range raw {
			row[i] = string(v)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// docsvcDB starts PostgreSQL with the docsvc schema and returns a store and a pool.
func docsvcDB(t *testing.T) (*store.Store, *pgxpool.Pool) {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test: needs Docker")
	}
	ctx := context.Background()
	ctr, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("docsvc"), postgres.WithUsername("docsvc"), postgres.WithPassword("docsvc"),
		postgres.BasicWaitStrategies())
	testcontainers.CleanupContainer(t, ctr)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	url, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return s, pool
}

func TestRulesIntegration(t *testing.T) {
	ctx := context.Background()
	restoreStore, restore := docsvcDB(t)
	prodStore, prod := docsvcDB(t)
	sql := pgxSQL{"restore": restore, "db-a": prod}

	for _, s := range []*store.Store{restoreStore, prodStore} {
		id := uuid.NewString()
		if _, err := s.CreateDocument(ctx, id, "invoice", "documents/"+id); err != nil {
			t.Fatal(err)
		}
		if err := s.InsertCanary(ctx, 1); err != nil {
			t.Fatal(err)
		}
	}
	asOf := time.Now().Add(5 * time.Second).UTC().Format(time.RFC3339Nano)

	res, err := verify.Rules{SQL: sql}.Run(ctx, params("host", "restore", "as_of", asOf, "compare_host", "db-a"))
	if err != nil || res.Status != verify.Pass || res.Metrics["documents"] != 1 {
		t.Fatalf("matching restore: %+v %v", res, err)
	}

	// Production has a document the restore lacks.
	id := uuid.NewString()
	if _, err := prodStore.CreateDocument(ctx, id, "late", "documents/"+id); err != nil {
		t.Fatal(err)
	}
	res, err = verify.Rules{SQL: sql}.Run(ctx, params("host", "restore", "as_of", asOf, "compare_host", "db-a"))
	if err != nil || res.Status != verify.Fail || res.Metrics["production_documents_before_target"] != 2 {
		t.Fatalf("diverging restore: %+v %v", res, err)
	}

	// A broken row fails the rules even without as_of.
	if _, err := restore.Exec(ctx, "UPDATE documents SET attachment_key = 'documents/other'"); err != nil {
		t.Fatal(err)
	}
	res, err = verify.Rules{SQL: sql}.Run(ctx, params("host", "restore"))
	if err != nil || res.Status != verify.Fail || res.Metrics["rule_violations"] != 1 {
		t.Fatalf("broken row: %+v %v", res, err)
	}
}

func TestConsistencyIntegration(t *testing.T) {
	ctx := context.Background()
	s, pool := docsvcDB(t)
	ctr, err := tcminio.Run(ctx, "disavery/minio:local") // built by `make minio-image`
	testcontainers.CleanupContainer(t, ctr)
	if err != nil {
		t.Fatalf("start minio (run `make minio-image` first): %v", err)
	}
	endpoint, _ := ctr.ConnectionString(ctx)
	client, err := minio.New(endpoint, &minio.Options{Creds: credentials.NewStaticV4(ctr.Username, ctr.Password, "")})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.MakeBucket(ctx, "attachments", minio.MakeBucketOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := client.EnableVersioning(ctx, "attachments"); err != nil {
		t.Fatal(err)
	}
	put := func(key string) {
		t.Helper()
		if _, err := client.PutObject(ctx, "attachments", key, bytes.NewReader([]byte("x")), 1, minio.PutObjectOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	withAttachment, deleted := uuid.NewString(), uuid.NewString()
	for _, id := range []string{withAttachment, deleted} {
		if _, err := s.CreateDocument(ctx, id, "doc", "documents/"+id); err != nil {
			t.Fatal(err)
		}
		put("documents/" + id)
	}
	put("documents/orphan")
	if err := client.RemoveObject(ctx, "attachments", "documents/"+deleted, minio.RemoveObjectOptions{}); err != nil {
		t.Fatal(err)
	}

	check := verify.Consistency{
		SQL: pgxSQL{"db": pool},
		Stores: func(context.Context, string) (verify.ObjectLister, error) {
			return verify.MinioLister{Client: client, Bucket: "attachments"}, nil
		},
	}
	now := time.Now().Add(time.Hour) // every object is older than the grace period

	// Site store: the deleted attachment is missing.
	p := params("host", "db", "store", "obj-a")
	p.Now = now
	res, err := check.Run(ctx, p)
	if err != nil || res.Status != verify.Fail || res.Metrics["missing"] != 1 || res.Metrics["orphans"] != 1 {
		t.Fatalf("site store: %+v %v", res, err)
	}
	// Vault: an older version still holds it.
	p.With["store"] = "vault"
	res, err = check.Run(ctx, p)
	if err != nil || res.Status != verify.Pass || res.Metrics["orphans"] != 1 {
		t.Fatalf("vault: %+v %v", res, err)
	}
	// Orphans newer than the cutoff are not reported.
	p.Now = time.Now()
	res, err = check.Run(ctx, p)
	if err != nil || res.Metrics["orphans"] != 0 {
		t.Fatalf("grace period: %+v %v", res, err)
	}
}

func TestPITRIntegration(t *testing.T) {
	ctx := context.Background()
	s, pool := docsvcDB(t)
	target := time.Date(2026, 10, 7, 12, 0, 10, 500_000_000, time.UTC)
	var journal []canary.Entry
	for i := range 20 {
		sent := target.Add(time.Duration(i-10) * time.Second)
		journal = append(journal, canary.Entry{Seq: int64(1000 + i), Sent: sent, Acked: sent.Add(100 * time.Millisecond)})
	}
	// The restore contains exactly the writes acknowledged before the target.
	for _, e := range journal {
		if e.Acked.Before(target) {
			if err := s.InsertCanary(ctx, e.Seq); err != nil {
				t.Fatal(err)
			}
		}
	}
	check := verify.PITR{SQL: pgxSQL{"restore": pool}, Journal: func() ([]canary.Entry, error) { return journal, nil }, Tolerance: 200 * time.Millisecond}
	p := params("host", "restore", "target", "2026-10-07 12:00:10.500+00")
	res, err := check.Run(ctx, p)
	if err != nil || res.Status != verify.Pass || res.Metrics["must_exist"] != 10 || res.Metrics["must_not_exist"] != 9 {
		t.Fatalf("exact restore: %+v %v", res, err)
	}
	// One write too many: the restore went past the target.
	if err := s.InsertCanary(ctx, 1015); err != nil {
		t.Fatal(err)
	}
	res, err = check.Run(ctx, p)
	if err != nil || res.Status != verify.Fail || res.Metrics["unexpected"] != 1 {
		t.Fatalf("overshoot: %+v %v", res, err)
	}
	p.With["target"] = "2020-01-01T00:00:00Z"
	if _, err := check.Run(ctx, p); err == nil {
		t.Fatal("want error for a target outside the journal")
	}
}
