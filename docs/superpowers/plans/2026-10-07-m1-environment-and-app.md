# M1 — Environment, Application and Backups Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A single `make up` builds the local disaster-recovery lab: global zone (CoreDNS, Caddy edge, webhook mock), site-a (PostgreSQL 16 + pgBackRest, docsvc, MinIO) and an immutable vault, with backups landing in two repositories; `make smoke` proves it end to end.

**Architecture:** Systemd+SSH Debian containers ("nodes") are created by Terraform (Docker provider) and configured by Ansible over SSH, both running inside a pinned toolbox container. Secrets live in a SOPS/age-encrypted file inside a Docker volume, with an escrow copy of the age key in a second volume. `docsvc` is a small Go service storing records in PostgreSQL and attachments in MinIO, calling a fake payment provider that calls back over HTTPS.

**Tech Stack:** Go 1.26 (pgx/v5, minio-go/v7, google/uuid, testcontainers-go), Docker, Terraform 1.9 (kreuzwerker/docker, carlpett/sops, hashicorp/local), Ansible-core 2.17 (community.sops, community.postgresql), PostgreSQL 16 (PGDG), pgBackRest, MinIO, CoreDNS, Caddy, SOPS + age, step CLI.

**Spec:** `docs/superpowers/specs/2026-10-07-disavery-dr-design.md`

## Roadmap (this is plan 1 of 5)

The spec's build order (§13) is split into one plan per milestone; each plan is written when the previous one ships, so it can use what was learned.

| Plan | Milestone | Status |
|---|---|---|
| **1** | **Environment + docsvc + backups to both repos (this plan)** | **written** |
| 2 | `disavery` CLI core (runbook, executor, canary, prober, verify, report), `docs/bia.yaml`, S6 | later |
| 3 | Site-b (warm + pilot light), Prometheus/Alertmanager core alerts (needed by `wait_alert`), S1 on both tiers, S7, tier comparison table | later |
| 4 | S2–S5 | later |
| 5 | Remaining alerts + Grafana, nightly/weekly CI drills, `drill-history` branch + badge, ADRs, README polish | later |

Note: the spec lists monitoring in milestone 5, but S1's `detect` phase needs Alertmanager, so the minimal alerting stack moves into plan 3.

## Global Constraints

- Go module path: `github.com/oplosy/disavery`; `go 1.26` in `go.mod`.
- All code, comments, docs and commit messages in English.
- All text files use LF line endings (enforced by `.gitattributes`); the host is Windows, every script runs in Linux containers.
- PostgreSQL major version: 16. pgBackRest stanza name: `main`.
- Secrets are only read from `/secrets/local.sops.yaml` (Docker volume `disavery-secrets`), decrypted with `SOPS_AGE_KEY_FILE=/secrets/age/keys.txt`. Escrow copy of the age key: volume `disavery-escrow`, file `/escrow/age-keys.txt`. Never commit plaintext secrets.
- Every Terraform-managed container and network carries the label `disavery.env=drill`.
- WAN network `disavery-wan` = `172.31.0.0/24` (created by `compose.yaml`). Fixed addresses: toolbox `.2`, dns `.10`, edge `.11`, webhook `.12`, site-a nodes `.21` app / `.22` db / `.23` obj, site-b nodes `.31/.32/.33`, vault `.40`. Zone networks: site-a `172.31.1.0/24`, site-b `172.31.2.0/24`, vault `172.31.3.0/24`.
- Public name: `docs.disavery.test` (via edge); `direct.docs.disavery.test` (directly to the active site's app node). DNS TTL 30 s.
- Vault Object Lock: compliance mode, 14 days. Repo2 encryption: `aes-256-cbc`.
- `archive_timeout = 30`.
- Never commit on `main`; one task = one or more conventional commits on the feature branch. Every commit message ends with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Review Focus

1. **Windows checkout line endings** — with `core.autocrlf=true`, shell scripts and templates would reach Linux containers with CRLF and fail with `$'\r': command not found`. Expect every text file to be LF in the working tree. Pinned in Task 1 (Step 6) and in CI (Task 15).
2. **Bad uploads** — empty title, title over 200 characters, missing file, file over the size limit. Expect 400/413 with a JSON error and nothing stored. Pinned in Task 5 tests.
3. **Forged or replayed payment callbacks** — missing/wrong signature, unknown document, invalid status, same callback twice. Expect 401/404/400 and idempotent 204. Pinned in Task 5 tests.
4. **Re-running `make up` on an existing environment** — expect no Terraform changes and Ansible `changed=0` on every host. Pinned in Task 15 (Step 4).
5. **docsvc starting before its database is reachable** (normal after a site rebuild) — expect a fast non-zero exit with a clear error, not a hang, so systemd restarts it. Pinned in Task 7 (Step 3).

---

### Task 1: Repository scaffolding and docsvc configuration

**Files:**
- Create: `.gitattributes`, `.gitignore`, `.golangci.yml`, `Makefile`, `go.mod`
- Create: `internal/docsvc/config/config.go`
- Test: `internal/docsvc/config/config_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `config.Config` (fields `Listen, DatabaseURL, S3Endpoint, S3AccessKey, S3SecretKey, S3Bucket string; S3UseTLS bool; PaymentWebhookURL, PublicBaseURL, WebhookSecret string; MaxUploadBytes int64`) and `config.FromEnv(getenv func(string) string) (config.Config, error)`.

- [ ] **Step 1: Create the feature branch and repository files**

Prerequisite: the `docs/dr-design-spec` PR (spec + this plan) is merged into `main`, and Docker Desktop is running.

```bash
git switch main && git pull && git switch -c feat/m1-environment
```

`.gitattributes`:
```
* text=auto eol=lf
*.png binary
*.jpg binary
```

`.gitignore`:
```
/build/
/infra/ansible/inventory/hosts.yml
**/.terraform/
*.tfstate
*.tfstate.*
terraform.tfstate.d/
```

`.golangci.yml`:
```yaml
version: "2"
linters:
  default: standard
  enable:
    - bodyclose
    - errorlint
    - misspell
```

`Makefile`:
```make
SHELL := bash
.DEFAULT_GOAL := help

.PHONY: help
help: ## Show available targets
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-12s %s\n", $$1, $$2}'

.PHONY: test
test: ## Run all Go tests (integration tests need Docker)
	go test ./...

.PHONY: test-short
test-short: ## Run unit tests only
	go test -short ./...

.PHONY: lint
lint: ## Run golangci-lint
	golangci-lint run
```

```bash
go mod init github.com/oplosy/disavery
go mod edit -go=1.26
```

- [ ] **Step 2: Write the failing config tests**

`internal/docsvc/config/config_test.go`:
```go
package config_test

import (
	"strings"
	"testing"

	"github.com/oplosy/disavery/internal/docsvc/config"
)

func validEnv() map[string]string {
	return map[string]string{
		"DOCSVC_DATABASE_URL":   "postgres://docsvc@db-a/docsvc",
		"DOCSVC_S3_ENDPOINT":    "obj-a:9000",
		"DOCSVC_S3_ACCESS_KEY":  "docsvc",
		"DOCSVC_S3_SECRET_KEY":  "secret",
		"DOCSVC_WEBHOOK_SECRET": "hmac",
	}
}

func getenv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestFromEnvDefaults(t *testing.T) {
	c, err := config.FromEnv(getenv(validEnv()))
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if c.Listen != ":8080" || c.S3Bucket != "attachments" || c.MaxUploadBytes != 10<<20 || c.S3UseTLS {
		t.Fatalf("unexpected defaults: %+v", c)
	}
}

func TestFromEnvOverrides(t *testing.T) {
	env := validEnv()
	env["DOCSVC_LISTEN"] = ":9090"
	env["DOCSVC_S3_USE_TLS"] = "true"
	env["DOCSVC_MAX_UPLOAD_BYTES"] = "2048"
	c, err := config.FromEnv(getenv(env))
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if c.Listen != ":9090" || !c.S3UseTLS || c.MaxUploadBytes != 2048 {
		t.Fatalf("overrides not applied: %+v", c)
	}
}

func TestFromEnvErrors(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]string)
		want   string
	}{
		{"missing database url", func(m map[string]string) { delete(m, "DOCSVC_DATABASE_URL") }, "DOCSVC_DATABASE_URL is required"},
		{"missing webhook secret", func(m map[string]string) { delete(m, "DOCSVC_WEBHOOK_SECRET") }, "DOCSVC_WEBHOOK_SECRET is required"},
		{"webhook url without public url", func(m map[string]string) { m["DOCSVC_PAYMENT_WEBHOOK_URL"] = "http://webhook:8081/payments" }, "DOCSVC_PUBLIC_BASE_URL is required"},
		{"bad tls flag", func(m map[string]string) { m["DOCSVC_S3_USE_TLS"] = "maybe" }, "DOCSVC_S3_USE_TLS"},
		{"zero upload limit", func(m map[string]string) { m["DOCSVC_MAX_UPLOAD_BYTES"] = "0" }, "DOCSVC_MAX_UPLOAD_BYTES must be a positive integer"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := validEnv()
			tc.mutate(env)
			_, err := config.FromEnv(getenv(env))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/docsvc/config/`
Expected: FAIL — `no non-test Go files` / package does not exist.

- [ ] **Step 4: Implement the config package**

`internal/docsvc/config/config.go`:
```go
// Package config loads docsvc settings from environment variables.
package config

import (
	"cmp"
	"errors"
	"fmt"
	"strconv"
)

// Config holds docsvc runtime settings.
type Config struct {
	Listen            string
	DatabaseURL       string
	S3Endpoint        string
	S3AccessKey       string
	S3SecretKey       string
	S3Bucket          string
	S3UseTLS          bool
	PaymentWebhookURL string
	PublicBaseURL     string
	WebhookSecret     string
	MaxUploadBytes    int64
}

// FromEnv builds a Config using getenv (normally os.Getenv). It reports every
// problem at once so a misconfigured node shows all errors in one log line.
func FromEnv(getenv func(string) string) (Config, error) {
	c := Config{
		Listen:            cmp.Or(getenv("DOCSVC_LISTEN"), ":8080"),
		DatabaseURL:       getenv("DOCSVC_DATABASE_URL"),
		S3Endpoint:        getenv("DOCSVC_S3_ENDPOINT"),
		S3AccessKey:       getenv("DOCSVC_S3_ACCESS_KEY"),
		S3SecretKey:       getenv("DOCSVC_S3_SECRET_KEY"),
		S3Bucket:          cmp.Or(getenv("DOCSVC_S3_BUCKET"), "attachments"),
		PaymentWebhookURL: getenv("DOCSVC_PAYMENT_WEBHOOK_URL"),
		PublicBaseURL:     getenv("DOCSVC_PUBLIC_BASE_URL"),
		WebhookSecret:     getenv("DOCSVC_WEBHOOK_SECRET"),
		MaxUploadBytes:    10 << 20,
	}

	var errs []error
	required := []struct{ name, value string }{
		{"DOCSVC_DATABASE_URL", c.DatabaseURL},
		{"DOCSVC_S3_ENDPOINT", c.S3Endpoint},
		{"DOCSVC_S3_ACCESS_KEY", c.S3AccessKey},
		{"DOCSVC_S3_SECRET_KEY", c.S3SecretKey},
		{"DOCSVC_WEBHOOK_SECRET", c.WebhookSecret},
	}
	for _, r := range required {
		if r.value == "" {
			errs = append(errs, fmt.Errorf("%s is required", r.name))
		}
	}
	if c.PaymentWebhookURL != "" && c.PublicBaseURL == "" {
		errs = append(errs, errors.New("DOCSVC_PUBLIC_BASE_URL is required when DOCSVC_PAYMENT_WEBHOOK_URL is set"))
	}
	if v := getenv("DOCSVC_S3_USE_TLS"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("DOCSVC_S3_USE_TLS: %w", err))
		}
		c.S3UseTLS = b
	}
	if v := getenv("DOCSVC_MAX_UPLOAD_BYTES"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			errs = append(errs, fmt.Errorf("DOCSVC_MAX_UPLOAD_BYTES must be a positive integer, got %q", v))
		}
		c.MaxUploadBytes = n
	}
	return c, errors.Join(errs...)
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/docsvc/config/ -v`
Expected: PASS (3 tests, 5 subtests).

- [ ] **Step 6: Normalise line endings and verify**

```bash
git add --renormalize .
git ls-files --eol | grep -v 'i/lf' | grep -v 'i/-text' || true
```
Expected: no output (every tracked text file is `i/lf`). Then check the working-tree copies: `git ls-files --eol README.md docs/` must show `w/lf`. If any shows `w/crlf`, re-check them out with the new attributes (only these committed files; nothing else is touched):

```bash
rm README.md docs/superpowers/specs/*.md docs/superpowers/plans/*.md
git checkout HEAD -- README.md docs/superpowers
```

- [ ] **Step 7: Commit**

```bash
git add .gitattributes .gitignore .golangci.yml Makefile go.mod internal/docsvc/config
git commit -m "feat: scaffold repository and docsvc configuration"
```

---

### Task 2: PostgreSQL store with embedded migrations

**Files:**
- Create: `internal/docsvc/store/store.go`, `internal/docsvc/store/migrations/001_init.sql`
- Test: `internal/docsvc/store/store_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `store.ErrNotFound error`
  - `store.Document{ID, Title, AttachmentKey, PaymentStatus string; CreatedAt, UpdatedAt time.Time}` (JSON: `id, title, payment_status, created_at, updated_at`; `AttachmentKey` is not serialised)
  - `store.Open(ctx, databaseURL string) (*store.Store, error)`, `(*Store).Close()`, `Ping(ctx) error`, `Migrate(ctx) error`
  - `CreateDocument(ctx, id, title, attachmentKey string) (Document, error)`, `GetDocument(ctx, id string) (Document, error)`, `SetPaymentStatus(ctx, id, status string) error`
  - `InsertCanary(ctx, seq int64) error`, `CanarySeqs(ctx, from int64, limit int) ([]int64, error)` (never returns a nil slice on success)

- [ ] **Step 1: Add dependencies**

```bash
go get github.com/jackc/pgx/v5 github.com/testcontainers/testcontainers-go github.com/testcontainers/testcontainers-go/modules/postgres
```

- [ ] **Step 2: Write the failing integration tests**

`internal/docsvc/store/store_test.go`:
```go
package store_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/oplosy/disavery/internal/docsvc/store"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test: needs Docker")
	}
	ctx := context.Background()
	ctr, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("docsvc"),
		postgres.WithUsername("docsvc"),
		postgres.WithPassword("docsvc"),
		postgres.BasicWaitStrategies(),
	)
	testcontainers.CleanupContainer(t, ctr)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	url, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	s, err := store.Open(ctx, url)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(s.Close)
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return s
}

func TestMigrateIsIdempotent(t *testing.T) {
	s := newStore(t)
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
}

func TestDocumentLifecycle(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	id := uuid.NewString()

	created, err := s.CreateDocument(ctx, id, "invoice", "documents/"+id)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ID != id || created.PaymentStatus != "pending" || created.AttachmentKey != "documents/"+id {
		t.Fatalf("unexpected document: %+v", created)
	}

	if err := s.SetPaymentStatus(ctx, id, "paid"); err != nil {
		t.Fatalf("set status: %v", err)
	}
	got, err := s.GetDocument(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.PaymentStatus != "paid" || got.UpdatedAt.Before(got.CreatedAt) {
		t.Fatalf("status not updated: %+v", got)
	}
}

func TestUnknownDocument(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	missing := uuid.NewString()
	if _, err := s.GetDocument(ctx, missing); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("get: want ErrNotFound, got %v", err)
	}
	if err := s.SetPaymentStatus(ctx, missing, "paid"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("set status: want ErrNotFound, got %v", err)
	}
}

func TestCanary(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	for _, seq := range []int64{1, 2, 3, 2} { // 2 twice: client retries must be idempotent
		if err := s.InsertCanary(ctx, seq); err != nil {
			t.Fatalf("insert %d: %v", seq, err)
		}
	}
	tests := []struct {
		from  int64
		limit int
		want  []int64
	}{
		{2, 10, []int64{2, 3}},
		{2, 1, []int64{2}},
		{10, 10, []int64{}},
	}
	for _, tc := range tests {
		got, err := s.CanarySeqs(ctx, tc.from, tc.limit)
		if err != nil {
			t.Fatalf("seqs: %v", err)
		}
		if got == nil || !slices.Equal(got, tc.want) {
			t.Fatalf("CanarySeqs(%d,%d) = %#v, want %#v", tc.from, tc.limit, got, tc.want)
		}
	}
}
```

```bash
go get github.com/google/uuid
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/docsvc/store/`
Expected: FAIL — `store.Open` undefined.

- [ ] **Step 4: Write the migration**

`internal/docsvc/store/migrations/001_init.sql`:
```sql
CREATE TABLE documents (
    id             uuid PRIMARY KEY,
    title          text NOT NULL CHECK (length(title) BETWEEN 1 AND 200),
    attachment_key text NOT NULL UNIQUE,
    payment_status text NOT NULL DEFAULT 'pending'
                   CHECK (payment_status IN ('pending', 'paid', 'failed')),
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);

-- Drill canary: one row per acknowledged client write, used to measure real RPO.
CREATE TABLE canary (
    seq        bigint PRIMARY KEY,
    written_at timestamptz NOT NULL DEFAULT now()
);
```

- [ ] **Step 5: Implement the store**

`internal/docsvc/store/store.go`:
```go
// Package store persists documents and canary writes in PostgreSQL.
package store

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrations embed.FS

// migrationLockID serialises concurrent migrators, e.g. two app nodes starting together.
const migrationLockID = 727001

// ErrNotFound is returned when a document does not exist.
var ErrNotFound = errors.New("not found")

// Document is a stored document record. The attachment itself lives in object storage.
type Document struct {
	ID            string    `json:"id"`
	Title         string    `json:"title"`
	AttachmentKey string    `json:"-"`
	PaymentStatus string    `json:"payment_status"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// Store is a PostgreSQL-backed repository.
type Store struct {
	pool *pgxpool.Pool
}

// Open creates a connection pool. It does not connect until first use.
func Open(ctx context.Context, databaseURL string) (*Store, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	return &Store{pool: pool}, nil
}

// Close releases all connections.
func (s *Store) Close() { s.pool.Close() }

// Ping checks that the database answers.
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

// Migrate applies embedded migrations that have not been applied yet.
func (s *Store) Migrate(ctx context.Context) error {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire connection: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", migrationLockID); err != nil {
		return fmt.Errorf("take migration lock: %w", err)
	}
	defer conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", migrationLockID) //nolint:errcheck // also released when the session ends

	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    text PRIMARY KEY,
		applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	files, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(files)
	for _, file := range files {
		version := strings.TrimSuffix(path.Base(file), ".sql")
		var applied bool
		if err := conn.QueryRow(ctx,
			"SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)", version).Scan(&applied); err != nil {
			return fmt.Errorf("check migration %s: %w", version, err)
		}
		if applied {
			continue
		}
		body, err := migrations.ReadFile(file)
		if err != nil {
			return err
		}
		if err := pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
			// No arguments: pgx uses the simple protocol, so multi-statement files work.
			if _, err := tx.Exec(ctx, string(body)); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, "INSERT INTO schema_migrations (version) VALUES ($1)", version)
			return err
		}); err != nil {
			return fmt.Errorf("apply migration %s: %w", version, err)
		}
	}
	return nil
}

const documentColumns = "id::text, title, attachment_key, payment_status, created_at, updated_at"

func scanDocument(row pgx.Row) (Document, error) {
	var d Document
	err := row.Scan(&d.ID, &d.Title, &d.AttachmentKey, &d.PaymentStatus, &d.CreatedAt, &d.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Document{}, ErrNotFound
	}
	return d, err
}

// CreateDocument inserts a new document with payment status "pending".
func (s *Store) CreateDocument(ctx context.Context, id, title, attachmentKey string) (Document, error) {
	return scanDocument(s.pool.QueryRow(ctx,
		"INSERT INTO documents (id, title, attachment_key) VALUES ($1, $2, $3) RETURNING "+documentColumns,
		id, title, attachmentKey))
}

// GetDocument returns ErrNotFound for unknown ids. id must be a valid UUID.
func (s *Store) GetDocument(ctx context.Context, id string) (Document, error) {
	return scanDocument(s.pool.QueryRow(ctx, "SELECT "+documentColumns+" FROM documents WHERE id = $1", id))
}

// SetPaymentStatus updates the payment status; ErrNotFound for unknown ids.
func (s *Store) SetPaymentStatus(ctx context.Context, id, status string) error {
	tag, err := s.pool.Exec(ctx,
		"UPDATE documents SET payment_status = $2, updated_at = now() WHERE id = $1", id, status)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// InsertCanary records an acknowledged canary write. Duplicates are ignored.
func (s *Store) InsertCanary(ctx context.Context, seq int64) error {
	_, err := s.pool.Exec(ctx, "INSERT INTO canary (seq) VALUES ($1) ON CONFLICT (seq) DO NOTHING", seq)
	return err
}

// CanarySeqs returns surviving canary sequence numbers >= from, ascending.
func (s *Store) CanarySeqs(ctx context.Context, from int64, limit int) ([]int64, error) {
	rows, err := s.pool.Query(ctx, "SELECT seq FROM canary WHERE seq >= $1 ORDER BY seq LIMIT $2", from, limit)
	if err != nil {
		return nil, err
	}
	seqs, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return nil, err
	}
	if seqs == nil {
		seqs = []int64{}
	}
	return seqs, nil
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go mod tidy && go test ./internal/docsvc/store/ -v` (Docker Desktop must be running)
Expected: PASS — 4 tests. `go test -short ./internal/docsvc/store/` reports them as SKIP.

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum internal/docsvc/store
git commit -m "feat: add docsvc PostgreSQL store with migrations"
```

---

### Task 3: Attachment blob store

**Files:**
- Create: `internal/docsvc/blob/blob.go`
- Test: `internal/docsvc/blob/blob_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `blob.ErrNotFound error`; `blob.New(endpoint, accessKey, secretKey, bucket string, useTLS bool) (*blob.Store, error)`; `(*Store).Put(ctx, key string, r io.Reader, size int64, contentType string) error`; `Get(ctx, key string) (io.ReadCloser, error)`; `Ready(ctx) error`.

- [ ] **Step 1: Add dependencies**

```bash
go get github.com/minio/minio-go/v7 github.com/testcontainers/testcontainers-go/modules/minio
```

- [ ] **Step 2: Write the failing integration tests**

`internal/docsvc/blob/blob_test.go`:
```go
package blob_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/testcontainers/testcontainers-go"
	tcminio "github.com/testcontainers/testcontainers-go/modules/minio"

	"github.com/oplosy/disavery/internal/docsvc/blob"
)

type env struct {
	endpoint, user, password string
}

func startMinIO(t *testing.T) env {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test: needs Docker")
	}
	ctx := context.Background()
	ctr, err := tcminio.Run(ctx, "minio/minio:RELEASE.2024-10-13T13-34-11Z")
	testcontainers.CleanupContainer(t, ctr)
	if err != nil {
		t.Fatalf("start minio: %v", err)
	}
	endpoint, err := ctr.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("endpoint: %v", err)
	}
	admin, err := minio.New(endpoint, &minio.Options{Creds: credentials.NewStaticV4(ctr.Username, ctr.Password, "")})
	if err != nil {
		t.Fatalf("admin client: %v", err)
	}
	if err := admin.MakeBucket(ctx, "attachments", minio.MakeBucketOptions{}); err != nil {
		t.Fatalf("make bucket: %v", err)
	}
	return env{endpoint, ctr.Username, ctr.Password}
}

func TestPutGetRoundTrip(t *testing.T) {
	e := startMinIO(t)
	b, err := blob.New(e.endpoint, e.user, e.password, "attachments", false)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	data := []byte("hello attachment")
	if err := b.Put(ctx, "documents/x", bytes.NewReader(data), int64(len(data)), "text/plain"); err != nil {
		t.Fatalf("put: %v", err)
	}
	rc, err := b.Get(ctx, "documents/x")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if !bytes.Equal(got, data) {
		t.Fatalf("got %q, want %q", got, data)
	}
}

func TestGetMissing(t *testing.T) {
	e := startMinIO(t)
	b, _ := blob.New(e.endpoint, e.user, e.password, "attachments", false)
	if _, err := b.Get(context.Background(), "documents/missing"); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestReady(t *testing.T) {
	e := startMinIO(t)
	ok, _ := blob.New(e.endpoint, e.user, e.password, "attachments", false)
	if err := ok.Ready(context.Background()); err != nil {
		t.Fatalf("ready: %v", err)
	}
	missing, _ := blob.New(e.endpoint, e.user, e.password, "nope", false)
	if err := missing.Ready(context.Background()); err == nil {
		t.Fatal("want error for missing bucket")
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/docsvc/blob/`
Expected: FAIL — `blob.New` undefined.

- [ ] **Step 4: Implement the blob store**

`internal/docsvc/blob/blob.go`:
```go
// Package blob stores document attachments in S3-compatible object storage.
package blob

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// ErrNotFound is returned when an attachment object does not exist.
var ErrNotFound = errors.New("attachment not found")

// Store reads and writes objects in a single bucket.
type Store struct {
	client *minio.Client
	bucket string
}

// New creates a client. It does not contact the server.
func New(endpoint, accessKey, secretKey, bucket string, useTLS bool) (*Store, error) {
	c, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useTLS,
	})
	if err != nil {
		return nil, fmt.Errorf("create s3 client: %w", err)
	}
	return &Store{client: c, bucket: bucket}, nil
}

// Put uploads an object.
func (s *Store) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	_, err := s.client.PutObject(ctx, s.bucket, key, r, size, minio.PutObjectOptions{ContentType: contentType})
	return err
}

// Get opens an object for reading; ErrNotFound if it does not exist.
func (s *Store) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	if _, err := obj.Stat(); err != nil {
		obj.Close()
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return obj, nil
}

// Ready reports whether the bucket exists and is reachable.
func (s *Store) Ready(ctx context.Context) error {
	ok, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("bucket %q does not exist", s.bucket)
	}
	return nil
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go mod tidy && go test ./internal/docsvc/blob/ -v`
Expected: PASS — 3 tests.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/docsvc/blob
git commit -m "feat: add docsvc attachment blob store"
```

---

### Task 4: Webhook signing and payment notifier

**Files:**
- Create: `internal/docsvc/webhook/webhook.go`
- Test: `internal/docsvc/webhook/webhook_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `webhook.SignatureHeader = "X-Signature"`
  - `webhook.Sign(secret, body []byte) string` → `"sha256=<hex>"`; `webhook.Verify(secret, body []byte, header string) bool`
  - `webhook.Callback{DocumentID string \`json:"document_id"\`; Status string \`json:"status"\`}`
  - `webhook.PaymentRequest{DocumentID string \`json:"document_id"\`; CallbackURL string \`json:"callback_url"\`}`
  - `webhook.Notifier{URL, CallbackURL string; Client *http.Client}` with `DocumentCreated(ctx, id string) error` (no-op when `URL == ""`; error unless the provider answers 202).

- [ ] **Step 1: Write the failing tests**

`internal/docsvc/webhook/webhook_test.go`:
```go
package webhook_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/oplosy/disavery/internal/docsvc/webhook"
)

func TestSignVerify(t *testing.T) {
	secret, body := []byte("s3cret"), []byte(`{"document_id":"x","status":"paid"}`)
	sig := webhook.Sign(secret, body)
	if !webhook.Verify(secret, body, sig) {
		t.Fatal("valid signature rejected")
	}
	if webhook.Verify(secret, []byte(`{"document_id":"x","status":"failed"}`), sig) {
		t.Fatal("tampered body accepted")
	}
	if webhook.Verify([]byte("other"), body, sig) {
		t.Fatal("wrong secret accepted")
	}
	if webhook.Verify(secret, body, "") {
		t.Fatal("missing signature accepted")
	}
}

func TestNotifierPostsPaymentRequest(t *testing.T) {
	var got webhook.PaymentRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("unexpected request %s %s", r.Method, r.Header.Get("Content-Type"))
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	n := &webhook.Notifier{URL: srv.URL, CallbackURL: "https://docs.disavery.test/callbacks/payment", Client: srv.Client()}
	if err := n.DocumentCreated(context.Background(), "doc-1"); err != nil {
		t.Fatalf("notify: %v", err)
	}
	if got.DocumentID != "doc-1" || got.CallbackURL != "https://docs.disavery.test/callbacks/payment" {
		t.Fatalf("unexpected payload: %+v", got)
	}
}

func TestNotifierRejectsUnexpectedStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	n := &webhook.Notifier{URL: srv.URL, CallbackURL: "https://x/cb", Client: srv.Client()}
	if err := n.DocumentCreated(context.Background(), "doc-1"); err == nil {
		t.Fatal("want error for 403")
	}
}

func TestNotifierDisabledWithoutURL(t *testing.T) {
	n := &webhook.Notifier{}
	if err := n.DocumentCreated(context.Background(), "doc-1"); err != nil {
		t.Fatalf("disabled notifier returned %v", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/docsvc/webhook/`
Expected: FAIL — `webhook.Sign` undefined.

- [ ] **Step 3: Implement the package**

`internal/docsvc/webhook/webhook.go`:
```go
// Package webhook talks to the payment provider: outgoing payment requests and
// HMAC-signed incoming callbacks.
package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// SignatureHeader carries the HMAC of a callback body.
const SignatureHeader = "X-Signature"

// PaymentRequest is sent to the provider when a document is created.
type PaymentRequest struct {
	DocumentID  string `json:"document_id"`
	CallbackURL string `json:"callback_url"`
}

// Callback is posted back by the provider.
type Callback struct {
	DocumentID string `json:"document_id"`
	Status     string `json:"status"`
}

// Sign returns the signature header value for body.
func Sign(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// Verify checks a signature header value in constant time.
func Verify(secret, body []byte, header string) bool {
	return hmac.Equal([]byte(Sign(secret, body)), []byte(header))
}

// Notifier sends payment requests. A zero URL disables it.
type Notifier struct {
	URL         string
	CallbackURL string
	Client      *http.Client
}

// DocumentCreated asks the provider to start a payment for document id.
func (n *Notifier) DocumentCreated(ctx context.Context, id string) error {
	if n.URL == "" {
		return nil
	}
	body, err := json.Marshal(PaymentRequest{DocumentID: id, CallbackURL: n.CallbackURL})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := n.Client.Do(req)
	if err != nil {
		return fmt.Errorf("payment webhook: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("payment webhook: unexpected status %d", resp.StatusCode)
	}
	return nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/docsvc/webhook/ -v`
Expected: PASS — 4 tests.

- [ ] **Step 5: Commit**

```bash
git add internal/docsvc/webhook
git commit -m "feat: add payment webhook signing and notifier"
```

---

### Task 5: docsvc HTTP API

**Files:**
- Create: `internal/docsvc/api/api.go`
- Test: `internal/docsvc/api/api_test.go`

**Interfaces:**
- Consumes: `store.Document`, `store.ErrNotFound` (Task 2); `blob.ErrNotFound` (Task 3); `webhook.Verify`, `webhook.SignatureHeader`, `webhook.Callback` (Task 4).
- Produces: `api.Store`, `api.Blob`, `api.Notifier` interfaces (method sets exactly as implemented by `*store.Store`, `*blob.Store`, `*webhook.Notifier`); `api.Server{Store; Blob; Notifier; WebhookSecret []byte; MaxUploadBytes int64; Log *slog.Logger}` with `Handler() http.Handler`.
- HTTP contract (plan 2's canary/prober depend on it):
  - `GET /healthz` → 200 `{"status":"ok"}`
  - `GET /readyz` → 200 or 503 `{"status":..., "checks":{"database":"ok|<err>","attachments":"ok|<err>"}}`
  - `POST /documents` (multipart `title`, `file`) → 201 document JSON
  - `GET /documents/{id}` → 200 document JSON / 404
  - `GET /documents/{id}/attachment` → 200 bytes / 404
  - `POST /callbacks/payment` (signed JSON `Callback`) → 204 / 400 / 401 / 404
  - `POST /canary` `{"seq":n}` (n ≥ 1) → 204 / 400
  - `GET /canary?from=n` → 200 `{"seqs":[...]}`
  - Errors are JSON `{"error":"..."}`.

- [ ] **Step 1: Write the failing tests**

`internal/docsvc/api/api_test.go`:
```go
package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/oplosy/disavery/internal/docsvc/api"
	"github.com/oplosy/disavery/internal/docsvc/blob"
	"github.com/oplosy/disavery/internal/docsvc/store"
	"github.com/oplosy/disavery/internal/docsvc/webhook"
)

var errDown = errors.New("down")

type fakeStore struct {
	mu     sync.Mutex
	docs   map[string]store.Document
	canary map[int64]bool
	err    error
}

func (f *fakeStore) CreateDocument(_ context.Context, id, title, key string) (store.Document, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return store.Document{}, f.err
	}
	now := time.Now()
	d := store.Document{ID: id, Title: title, AttachmentKey: key, PaymentStatus: "pending", CreatedAt: now, UpdatedAt: now}
	f.docs[id] = d
	return d, nil
}

func (f *fakeStore) GetDocument(_ context.Context, id string) (store.Document, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return store.Document{}, f.err
	}
	d, ok := f.docs[id]
	if !ok {
		return store.Document{}, store.ErrNotFound
	}
	return d, nil
}

func (f *fakeStore) SetPaymentStatus(_ context.Context, id, status string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	d, ok := f.docs[id]
	if !ok {
		return store.ErrNotFound
	}
	d.PaymentStatus = status
	f.docs[id] = d
	return nil
}

func (f *fakeStore) InsertCanary(_ context.Context, seq int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.canary[seq] = true
	return nil
}

func (f *fakeStore) CanarySeqs(_ context.Context, from int64, limit int) ([]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []int64{}
	for seq := range f.canary {
		if seq >= from {
			out = append(out, seq)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, f.err
}

func (f *fakeStore) Ping(context.Context) error { return f.err }

type fakeBlob struct {
	mu      sync.Mutex
	objects map[string][]byte
	err     error
}

func (f *fakeBlob) Put(_ context.Context, key string, r io.Reader, _ int64, _ string) error {
	if f.err != nil {
		return f.err
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objects[key] = data
	return nil
}

func (f *fakeBlob) Get(_ context.Context, key string) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	data, ok := f.objects[key]
	if !ok {
		return nil, blob.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (f *fakeBlob) Ready(context.Context) error { return f.err }

type fakeNotifier struct {
	ids []string
	err error
}

func (f *fakeNotifier) DocumentCreated(_ context.Context, id string) error {
	f.ids = append(f.ids, id)
	return f.err
}

const secret = "s3cret"

type harness struct {
	h        http.Handler
	store    *fakeStore
	blob     *fakeBlob
	notifier *fakeNotifier
}

func newHarness() *harness {
	h := &harness{
		store:    &fakeStore{docs: map[string]store.Document{}, canary: map[int64]bool{}},
		blob:     &fakeBlob{objects: map[string][]byte{}},
		notifier: &fakeNotifier{},
	}
	srv := &api.Server{
		Store: h.store, Blob: h.blob, Notifier: h.notifier,
		WebhookSecret: []byte(secret), MaxUploadBytes: 1024,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	h.h = srv.Handler()
	return h
}

func (h *harness) do(req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.h.ServeHTTP(rec, req)
	return rec
}

func uploadRequest(t *testing.T, title string, file []byte) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if title != "" {
		_ = mw.WriteField("title", title)
	}
	if file != nil {
		fw, err := mw.CreateFormFile("file", "doc.txt")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = fw.Write(file)
	}
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/documents", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return v
}

func (h *harness) createDoc(t *testing.T) store.Document {
	t.Helper()
	rec := h.do(uploadRequest(t, "invoice", []byte("pdf-bytes")))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	return decode[store.Document](t, rec)
}

func TestCreateDocument(t *testing.T) {
	h := newHarness()
	doc := h.createDoc(t)
	if _, err := uuid.Parse(doc.ID); err != nil || doc.Title != "invoice" || doc.PaymentStatus != "pending" {
		t.Fatalf("unexpected document: %+v", doc)
	}
	if string(h.blob.objects["documents/"+doc.ID]) != "pdf-bytes" {
		t.Fatal("attachment not stored under documents/<id>")
	}
	if !slices.Equal(h.notifier.ids, []string{doc.ID}) {
		t.Fatalf("notifier called with %v", h.notifier.ids)
	}
}

func TestCreateDocumentValidation(t *testing.T) {
	tests := []struct {
		name  string
		title string
		file  []byte
		code  int
	}{
		{"missing title", "", []byte("x"), http.StatusBadRequest},
		{"blank title", "   ", []byte("x"), http.StatusBadRequest},
		{"title too long", strings.Repeat("é", 201), []byte("x"), http.StatusBadRequest},
		{"missing file", "invoice", nil, http.StatusBadRequest},
		{"file too large", "invoice", bytes.Repeat([]byte("x"), 4096), http.StatusRequestEntityTooLarge},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness()
			rec := h.do(uploadRequest(t, tc.title, tc.file))
			if rec.Code != tc.code {
				t.Fatalf("code %d, want %d (%s)", rec.Code, tc.code, rec.Body)
			}
			if decode[map[string]string](t, rec)["error"] == "" {
				t.Fatal("missing JSON error message")
			}
			if len(h.store.docs) != 0 || len(h.blob.objects) != 0 {
				t.Fatal("rejected upload left data behind")
			}
		})
	}
}

func TestCreateDocumentBlobDown(t *testing.T) {
	h := newHarness()
	h.blob.err = errDown
	rec := h.do(uploadRequest(t, "invoice", []byte("x")))
	if rec.Code != http.StatusServiceUnavailable || len(h.store.docs) != 0 {
		t.Fatalf("code %d, docs %d", rec.Code, len(h.store.docs))
	}
}

func TestCreateDocumentNotifierFailureKeepsDocument(t *testing.T) {
	h := newHarness()
	h.notifier.err = errDown
	doc := h.createDoc(t)
	if doc.PaymentStatus != "pending" {
		t.Fatalf("status %q", doc.PaymentStatus)
	}
}

func TestGetDocument(t *testing.T) {
	h := newHarness()
	doc := h.createDoc(t)
	if rec := h.do(httptest.NewRequest(http.MethodGet, "/documents/"+doc.ID, nil)); rec.Code != http.StatusOK || decode[store.Document](t, rec).ID != doc.ID {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}
	for _, id := range []string{uuid.NewString(), "not-a-uuid"} {
		if rec := h.do(httptest.NewRequest(http.MethodGet, "/documents/"+id, nil)); rec.Code != http.StatusNotFound {
			t.Fatalf("get %s: %d", id, rec.Code)
		}
	}
}

func TestGetAttachment(t *testing.T) {
	h := newHarness()
	doc := h.createDoc(t)
	rec := h.do(httptest.NewRequest(http.MethodGet, "/documents/"+doc.ID+"/attachment", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "pdf-bytes" {
		t.Fatalf("attachment: %d %q", rec.Code, rec.Body)
	}
	delete(h.blob.objects, "documents/"+doc.ID) // DB row without object: a DR consistency failure
	rec = h.do(httptest.NewRequest(http.MethodGet, "/documents/"+doc.ID+"/attachment", nil))
	if rec.Code != http.StatusNotFound || decode[map[string]string](t, rec)["error"] != "attachment missing" {
		t.Fatalf("missing attachment: %d %s", rec.Code, rec.Body)
	}
}

func callbackRequest(body, signature string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/callbacks/payment", strings.NewReader(body))
	req.Header.Set(webhook.SignatureHeader, signature)
	return req
}

func signed(body string) *http.Request {
	return callbackRequest(body, webhook.Sign([]byte(secret), []byte(body)))
}

func TestPaymentCallback(t *testing.T) {
	h := newHarness()
	doc := h.createDoc(t)
	paid := `{"document_id":"` + doc.ID + `","status":"paid"}`

	if rec := h.do(signed(paid)); rec.Code != http.StatusNoContent {
		t.Fatalf("valid callback: %d %s", rec.Code, rec.Body)
	}
	if h.store.docs[doc.ID].PaymentStatus != "paid" {
		t.Fatal("status not updated")
	}
	if rec := h.do(signed(paid)); rec.Code != http.StatusNoContent {
		t.Fatalf("replayed callback: %d", rec.Code)
	}

	tests := []struct {
		name string
		req  *http.Request
		code int
	}{
		{"missing signature", callbackRequest(paid, ""), http.StatusUnauthorized},
		{"wrong signature", callbackRequest(paid, webhook.Sign([]byte("other"), []byte(paid))), http.StatusUnauthorized},
		{"unknown document", signed(`{"document_id":"` + uuid.NewString() + `","status":"paid"}`), http.StatusNotFound},
		{"invalid status", signed(`{"document_id":"` + doc.ID + `","status":"refunded"}`), http.StatusBadRequest},
		{"invalid id", signed(`{"document_id":"x","status":"paid"}`), http.StatusBadRequest},
		{"invalid json", signed(`{`), http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if rec := h.do(tc.req); rec.Code != tc.code {
				t.Fatalf("code %d, want %d (%s)", rec.Code, tc.code, rec.Body)
			}
		})
	}
}

func TestCanary(t *testing.T) {
	h := newHarness()
	for _, body := range []string{`{"seq":1}`, `{"seq":2}`, `{"seq":2}`} {
		if rec := h.do(httptest.NewRequest(http.MethodPost, "/canary", strings.NewReader(body))); rec.Code != http.StatusNoContent {
			t.Fatalf("write %s: %d", body, rec.Code)
		}
	}
	for _, body := range []string{`{"seq":0}`, `{"seq":-3}`, `{}`, `nope`} {
		if rec := h.do(httptest.NewRequest(http.MethodPost, "/canary", strings.NewReader(body))); rec.Code != http.StatusBadRequest {
			t.Fatalf("write %s: %d, want 400", body, rec.Code)
		}
	}
	rec := h.do(httptest.NewRequest(http.MethodGet, "/canary?from=2", nil))
	if got := decode[map[string][]int64](t, rec)["seqs"]; !slices.Equal(got, []int64{2}) {
		t.Fatalf("seqs %v", got)
	}
	rec = h.do(httptest.NewRequest(http.MethodGet, "/canary?from=99", nil))
	if !strings.Contains(rec.Body.String(), `"seqs":[]`) {
		t.Fatalf("empty list must be [], got %s", rec.Body)
	}
	if rec := h.do(httptest.NewRequest(http.MethodGet, "/canary?from=0", nil)); rec.Code != http.StatusBadRequest {
		t.Fatalf("from=0: %d", rec.Code)
	}
}

func TestHealthAndReadiness(t *testing.T) {
	h := newHarness()
	if rec := h.do(httptest.NewRequest(http.MethodGet, "/healthz", nil)); rec.Code != http.StatusOK {
		t.Fatalf("healthz %d", rec.Code)
	}
	if rec := h.do(httptest.NewRequest(http.MethodGet, "/readyz", nil)); rec.Code != http.StatusOK {
		t.Fatalf("readyz %d", rec.Code)
	}
	h.blob.err = errDown
	rec := h.do(httptest.NewRequest(http.MethodGet, "/readyz", nil))
	body := decode[struct {
		Checks map[string]string `json:"checks"`
	}](t, rec)
	if rec.Code != http.StatusServiceUnavailable || body.Checks["attachments"] != "down" || body.Checks["database"] != "ok" {
		t.Fatalf("readyz with blob down: %d %s", rec.Code, rec.Body)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/docsvc/api/`
Expected: FAIL — `api.Server` undefined.

- [ ] **Step 3: Implement the API**

`internal/docsvc/api/api.go`:
```go
// Package api implements the docsvc HTTP interface.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/oplosy/disavery/internal/docsvc/blob"
	"github.com/oplosy/disavery/internal/docsvc/store"
	"github.com/oplosy/disavery/internal/docsvc/webhook"
)

const (
	maxTitleRunes   = 200
	canaryListLimit = 100_000
)

// Store is the persistence the API needs; implemented by *store.Store.
type Store interface {
	CreateDocument(ctx context.Context, id, title, attachmentKey string) (store.Document, error)
	GetDocument(ctx context.Context, id string) (store.Document, error)
	SetPaymentStatus(ctx context.Context, id, status string) error
	InsertCanary(ctx context.Context, seq int64) error
	CanarySeqs(ctx context.Context, from int64, limit int) ([]int64, error)
	Ping(ctx context.Context) error
}

// Blob is the attachment storage the API needs; implemented by *blob.Store.
type Blob interface {
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	Ready(ctx context.Context) error
}

// Notifier informs the payment provider; implemented by *webhook.Notifier.
type Notifier interface {
	DocumentCreated(ctx context.Context, id string) error
}

// Server wires handlers to their dependencies.
type Server struct {
	Store          Store
	Blob           Blob
	Notifier       Notifier
	WebhookSecret  []byte
	MaxUploadBytes int64
	Log            *slog.Logger
}

// Handler returns the HTTP routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /readyz", s.readyz)
	mux.HandleFunc("POST /documents", s.createDocument)
	mux.HandleFunc("GET /documents/{id}", s.getDocument)
	mux.HandleFunc("GET /documents/{id}/attachment", s.getAttachment)
	mux.HandleFunc("POST /callbacks/payment", s.paymentCallback)
	mux.HandleFunc("POST /canary", s.writeCanary)
	mux.HandleFunc("GET /canary", s.listCanary)
	return mux
}

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	checks := map[string]string{"database": "ok", "attachments": "ok"}
	status := http.StatusOK
	if err := s.Store.Ping(ctx); err != nil {
		checks["database"] = err.Error()
		status = http.StatusServiceUnavailable
	}
	if err := s.Blob.Ready(ctx); err != nil {
		checks["attachments"] = err.Error()
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, map[string]any{"status": http.StatusText(status), "checks": checks})
}

func (s *Server) createDocument(w http.ResponseWriter, r *http.Request) {
	// Allow 1 MiB on top of the file limit for multipart framing and fields.
	r.Body = http.MaxBytesReader(w, r.Body, s.MaxUploadBytes+1<<20)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, "upload too large")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid multipart form")
		return
	}
	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" || utf8.RuneCountInString(title) > maxTitleRunes {
		writeError(w, http.StatusBadRequest, "title must be 1-200 characters")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file is required")
		return
	}
	defer file.Close()
	if header.Size > s.MaxUploadBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "upload too large")
		return
	}

	id := uuid.NewString()
	key := "documents/" + id
	contentType := header.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	// Object first, row second: a crash in between leaves an orphan object
	// (harmless, found by the consistency check) rather than a row whose
	// attachment is missing (user-visible).
	if err := s.Blob.Put(r.Context(), key, file, header.Size, contentType); err != nil {
		s.Log.Error("store attachment", "id", id, "err", err)
		writeError(w, http.StatusServiceUnavailable, "attachment storage unavailable")
		return
	}
	doc, err := s.Store.CreateDocument(r.Context(), id, title, key)
	if err != nil {
		s.Log.Error("create document", "id", id, "err", err)
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	if err := s.Notifier.DocumentCreated(r.Context(), doc.ID); err != nil {
		// The document stays "pending"; the provider can be retried out of band.
		s.Log.Warn("payment request failed", "id", doc.ID, "err", err)
	}
	writeJSON(w, http.StatusCreated, doc)
}

func (s *Server) lookupDocument(w http.ResponseWriter, r *http.Request) (store.Document, bool) {
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		writeError(w, http.StatusNotFound, "document not found")
		return store.Document{}, false
	}
	doc, err := s.Store.GetDocument(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "document not found")
		return store.Document{}, false
	}
	if err != nil {
		s.Log.Error("get document", "id", id, "err", err)
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return store.Document{}, false
	}
	return doc, true
}

func (s *Server) getDocument(w http.ResponseWriter, r *http.Request) {
	if doc, ok := s.lookupDocument(w, r); ok {
		writeJSON(w, http.StatusOK, doc)
	}
}

func (s *Server) getAttachment(w http.ResponseWriter, r *http.Request) {
	doc, ok := s.lookupDocument(w, r)
	if !ok {
		return
	}
	body, err := s.Blob.Get(r.Context(), doc.AttachmentKey)
	if errors.Is(err, blob.ErrNotFound) {
		writeError(w, http.StatusNotFound, "attachment missing")
		return
	}
	if err != nil {
		s.Log.Error("get attachment", "id", doc.ID, "err", err)
		writeError(w, http.StatusServiceUnavailable, "attachment storage unavailable")
		return
	}
	defer body.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	if _, err := io.Copy(w, body); err != nil {
		s.Log.Warn("stream attachment", "id", doc.ID, "err", err)
	}
}

func (s *Server) paymentCallback(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		writeError(w, http.StatusBadRequest, "unreadable body")
		return
	}
	if !webhook.Verify(s.WebhookSecret, body, r.Header.Get(webhook.SignatureHeader)) {
		writeError(w, http.StatusUnauthorized, "invalid signature")
		return
	}
	var cb webhook.Callback
	if err := json.Unmarshal(body, &cb); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if _, err := uuid.Parse(cb.DocumentID); err != nil {
		writeError(w, http.StatusBadRequest, "invalid document_id")
		return
	}
	if cb.Status != "paid" && cb.Status != "failed" {
		writeError(w, http.StatusBadRequest, "status must be paid or failed")
		return
	}
	err = s.Store.SetPaymentStatus(r.Context(), cb.DocumentID, cb.Status)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "document not found")
		return
	}
	if err != nil {
		s.Log.Error("set payment status", "id", cb.DocumentID, "err", err)
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) writeCanary(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Seq *int64 `json:"seq"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&req); err != nil || req.Seq == nil || *req.Seq <= 0 {
		writeError(w, http.StatusBadRequest, "seq must be a positive integer")
		return
	}
	if err := s.Store.InsertCanary(r.Context(), *req.Seq); err != nil {
		s.Log.Error("insert canary", "seq", *req.Seq, "err", err)
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listCanary(w http.ResponseWriter, r *http.Request) {
	from := int64(1)
	if v := r.URL.Query().Get("from"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "from must be a positive integer")
			return
		}
		from = n
	}
	seqs, err := s.Store.CanarySeqs(r.Context(), from, canaryListLimit)
	if err != nil {
		s.Log.Error("list canary", "err", err)
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	if seqs == nil {
		seqs = []int64{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"seqs": seqs})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/docsvc/api/ -v -race`
Expected: PASS — 9 tests and their subtests.

- [ ] **Step 5: Commit**

```bash
git add internal/docsvc/api go.mod go.sum
git commit -m "feat: add docsvc HTTP API"
```

---

### Task 6: Webhook mock (fake payment provider)

**Files:**
- Create: `internal/webhookmock/webhookmock.go`, `cmd/webhookmock/main.go`
- Test: `internal/webhookmock/webhookmock_test.go`

**Interfaces:**
- Consumes: `webhook.Sign`, `webhook.SignatureHeader`, `webhook.Callback`, `webhook.PaymentRequest` (Task 4).
- Produces: `webhookmock.Config{AllowCIDRs []netip.Prefix; Secret []byte; Delay, RetryBackoff time.Duration; MaxAttempts int; Client *http.Client; Log *slog.Logger}`, `webhookmock.New(Config) *Server`, `(*Server).Handler() http.Handler`, `(*Server).Wait()`, `webhookmock.ParseCIDRs(string) ([]netip.Prefix, error)`. Binary env: `WEBHOOKMOCK_LISTEN` (default `:8081`), `WEBHOOKMOCK_ALLOW_CIDRS` (required, comma-separated), `WEBHOOKMOCK_SECRET` (required), `WEBHOOKMOCK_DELAY` (default `1s`).

- [ ] **Step 1: Write the failing tests**

`internal/webhookmock/webhookmock_test.go`:
```go
package webhookmock_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oplosy/disavery/internal/docsvc/webhook"
	"github.com/oplosy/disavery/internal/webhookmock"
)

var secret = []byte("s3cret")

func newServer(t *testing.T, allow string) *webhookmock.Server {
	t.Helper()
	cidrs, err := webhookmock.ParseCIDRs(allow)
	if err != nil {
		t.Fatal(err)
	}
	return webhookmock.New(webhookmock.Config{
		AllowCIDRs: cidrs, Secret: secret, RetryBackoff: 10 * time.Millisecond, MaxAttempts: 3,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

func payment(remote, callbackURL string) *http.Request {
	body := `{"document_id":"doc-1","callback_url":"` + callbackURL + `"}`
	req := httptest.NewRequest(http.MethodPost, "/payments", strings.NewReader(body))
	req.RemoteAddr = remote
	return req
}

func TestAllowedRequestGetsSignedCallback(t *testing.T) {
	var got webhook.Callback
	var sigOK atomic.Bool
	cb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		sigOK.Store(webhook.Verify(secret, body, r.Header.Get(webhook.SignatureHeader)))
		_ = json.Unmarshal(body, &got)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer cb.Close()

	srv := newServer(t, "172.31.0.21/32")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, payment("172.31.0.21:40000", cb.URL))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("code %d", rec.Code)
	}
	srv.Wait()
	if !sigOK.Load() || got.DocumentID != "doc-1" || got.Status != "paid" {
		t.Fatalf("callback %+v, signature ok %v", got, sigOK.Load())
	}
}

func TestDisallowedSourceIsRejected(t *testing.T) {
	srv := newServer(t, "172.31.0.21/32")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, payment("172.31.0.31:40000", "http://unused"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code %d, want 403", rec.Code)
	}
}

func TestInvalidRequests(t *testing.T) {
	srv := newServer(t, "0.0.0.0/0")
	for _, cbURL := range []string{"", "ftp://x/cb", "not a url"} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, payment("10.0.0.1:1", cbURL))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("callback_url %q: code %d, want 400", cbURL, rec.Code)
		}
	}
}

func TestCallbackIsRetried(t *testing.T) {
	var attempts atomic.Int32
	cb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer cb.Close()

	srv := newServer(t, "10.0.0.0/8")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, payment("10.1.2.3:1", cb.URL))
	srv.Wait()
	if attempts.Load() != 2 {
		t.Fatalf("attempts %d, want 2", attempts.Load())
	}
}

func TestParseCIDRs(t *testing.T) {
	got, err := webhookmock.ParseCIDRs(" 172.31.0.21/32, 10.0.0.0/8 ")
	if err != nil || len(got) != 2 || got[1] != netip.MustParsePrefix("10.0.0.0/8") {
		t.Fatalf("got %v, %v", got, err)
	}
	for _, bad := range []string{"", " , ", "300.1.1.1/32", "10.0.0.1"} {
		if _, err := webhookmock.ParseCIDRs(bad); err == nil {
			t.Fatalf("ParseCIDRs(%q): want error", bad)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/webhookmock/`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Implement the mock**

`internal/webhookmock/webhookmock.go`:
```go
// Package webhookmock stands in for a third-party payment provider. Like a real
// provider it only accepts requests from allow-listed source addresses, which
// makes "update the provider's allowlist" a mandatory step after failover.
package webhookmock

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/oplosy/disavery/internal/docsvc/webhook"
)

// Config controls the mock's behaviour.
type Config struct {
	AllowCIDRs   []netip.Prefix
	Secret       []byte
	Delay        time.Duration // before the callback is sent
	RetryBackoff time.Duration
	MaxAttempts  int
	Client       *http.Client
	Log          *slog.Logger
}

// Server is the mock provider.
type Server struct {
	cfg Config
	wg  sync.WaitGroup
}

// New applies defaults and returns a server.
func New(cfg Config) *Server {
	if cfg.MaxAttempts == 0 {
		cfg.MaxAttempts = 5
	}
	if cfg.RetryBackoff == 0 {
		cfg.RetryBackoff = 2 * time.Second
	}
	if cfg.Client == nil {
		cfg.Client = &http.Client{Timeout: 5 * time.Second}
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return &Server{cfg: cfg}
}

// Handler returns the HTTP routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("POST /payments", s.payments)
	return mux
}

// Wait blocks until all pending callbacks have finished.
func (s *Server) Wait() { s.wg.Wait() }

func (s *Server) payments(w http.ResponseWriter, r *http.Request) {
	addr, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil || !s.allowed(addr.Addr().Unmap()) {
		s.cfg.Log.Warn("rejected payment request", "remote", r.RemoteAddr)
		http.Error(w, "source address not allow-listed", http.StatusForbidden)
		return
	}
	var req webhook.PaymentRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req); err != nil || req.DocumentID == "" {
		http.Error(w, "invalid payment request", http.StatusBadRequest)
		return
	}
	u, err := url.Parse(req.CallbackURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		http.Error(w, "invalid callback_url", http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusAccepted)

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		time.Sleep(s.cfg.Delay)
		s.callback(req)
	}()
}

func (s *Server) allowed(a netip.Addr) bool {
	for _, p := range s.cfg.AllowCIDRs {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

func (s *Server) callback(req webhook.PaymentRequest) {
	body, _ := json.Marshal(webhook.Callback{DocumentID: req.DocumentID, Status: "paid"})
	sig := webhook.Sign(s.cfg.Secret, body)
	for attempt := 1; attempt <= s.cfg.MaxAttempts; attempt++ {
		status, err := s.post(req.CallbackURL, body, sig)
		if err == nil && status < 300 {
			s.cfg.Log.Info("callback delivered", "document_id", req.DocumentID, "attempt", attempt)
			return
		}
		s.cfg.Log.Warn("callback failed", "document_id", req.DocumentID, "attempt", attempt, "status", status, "err", err)
		if attempt < s.cfg.MaxAttempts {
			time.Sleep(s.cfg.RetryBackoff)
		}
	}
	s.cfg.Log.Error("callback abandoned", "document_id", req.DocumentID)
}

func (s *Server) post(target string, body []byte, sig string) (int, error) {
	req, err := http.NewRequest(http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(webhook.SignatureHeader, sig)
	resp, err := s.cfg.Client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}

// ParseCIDRs parses a comma-separated list of prefixes; at least one is required.
func ParseCIDRs(s string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		p, err := netip.ParsePrefix(part)
		if err != nil {
			return nil, fmt.Errorf("parse %q: %w", part, err)
		}
		out = append(out, p.Masked())
	}
	if len(out) == 0 {
		return nil, errors.New("at least one CIDR is required")
	}
	return out, nil
}
```

`cmd/webhookmock/main.go`:
```go
// Command webhookmock runs the fake payment provider.
package main

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/oplosy/disavery/internal/webhookmock"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("webhookmock exited", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cidrs, err := webhookmock.ParseCIDRs(os.Getenv("WEBHOOKMOCK_ALLOW_CIDRS"))
	if err != nil {
		return errors.Join(errors.New("WEBHOOKMOCK_ALLOW_CIDRS"), err)
	}
	secret := os.Getenv("WEBHOOKMOCK_SECRET")
	if secret == "" {
		return errors.New("WEBHOOKMOCK_SECRET is required")
	}
	delay, err := time.ParseDuration(cmp.Or(os.Getenv("WEBHOOKMOCK_DELAY"), "1s"))
	if err != nil {
		return errors.Join(errors.New("WEBHOOKMOCK_DELAY"), err)
	}
	listen := cmp.Or(os.Getenv("WEBHOOKMOCK_LISTEN"), ":8081")

	srv := webhookmock.New(webhookmock.Config{AllowCIDRs: cidrs, Secret: []byte(secret), Delay: delay, Log: log})
	httpSrv := &http.Server{Addr: listen, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errc := make(chan error, 1)
	go func() { errc <- httpSrv.ListenAndServe() }()
	log.Info("webhookmock listening", "addr", listen, "allow", cidrs)

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err = httpSrv.Shutdown(shutdownCtx)
	srv.Wait()
	return err
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/webhookmock/ -v -race && go build ./cmd/webhookmock`
Expected: PASS — 5 tests; build succeeds.

- [ ] **Step 5: Commit**

```bash
git add internal/webhookmock cmd/webhookmock
git commit -m "feat: add webhook mock payment provider"
```

---

### Task 7: docsvc binary

**Files:**
- Create: `cmd/docsvc/main.go`

**Interfaces:**
- Consumes: `config.FromEnv` (Task 1), `store.Open/Migrate` (Task 2), `blob.New` (Task 3), `webhook.Notifier` (Task 4), `api.Server` (Task 5).
- Produces: binary `docsvc` configured purely by `DOCSVC_*` environment variables (Task 1); exits non-zero on startup failure.

- [ ] **Step 1: Implement main**

`cmd/docsvc/main.go`:
```go
// Command docsvc runs the document service protected by disavery.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/oplosy/disavery/internal/docsvc/api"
	"github.com/oplosy/disavery/internal/docsvc/blob"
	"github.com/oplosy/disavery/internal/docsvc/config"
	"github.com/oplosy/disavery/internal/docsvc/store"
	"github.com/oplosy/disavery/internal/docsvc/webhook"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("docsvc exited", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.FromEnv(os.Getenv)
	if err != nil {
		return fmt.Errorf("configuration: %w", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer st.Close()
	// Bounded so an unreachable database fails fast and systemd restarts us.
	migrateCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := st.Migrate(migrateCtx); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}

	bl, err := blob.New(cfg.S3Endpoint, cfg.S3AccessKey, cfg.S3SecretKey, cfg.S3Bucket, cfg.S3UseTLS)
	if err != nil {
		return err
	}
	srv := &api.Server{
		Store: st,
		Blob:  bl,
		Notifier: &webhook.Notifier{
			URL:         cfg.PaymentWebhookURL,
			CallbackURL: strings.TrimRight(cfg.PublicBaseURL, "/") + "/callbacks/payment",
			Client:      &http.Client{Timeout: 5 * time.Second},
		},
		WebhookSecret:  []byte(cfg.WebhookSecret),
		MaxUploadBytes: cfg.MaxUploadBytes,
		Log:            log,
	}
	httpSrv := &http.Server{Addr: cfg.Listen, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}

	errc := make(chan error, 1)
	go func() { errc <- httpSrv.ListenAndServe() }()
	log.Info("docsvc listening", "addr", cfg.Listen)

	select {
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	return httpSrv.Shutdown(shutdownCtx)
}
```

- [ ] **Step 2: Verify it builds and rejects missing configuration**

Run (Git Bash):
```bash
go build -o build/docsvc.exe ./cmd/docsvc && ./build/docsvc.exe; echo "exit=$?"
```
Expected: one JSON log line `"msg":"docsvc exited"` whose `err` lists `DOCSVC_DATABASE_URL is required`, `DOCSVC_S3_ENDPOINT is required`, …, then `exit=1`.

- [ ] **Step 3: Verify an unreachable database fails fast (Review Focus 5)**

```bash
time (DOCSVC_DATABASE_URL='postgres://docsvc:x@127.0.0.1:1/docsvc?sslmode=disable' \
  DOCSVC_S3_ENDPOINT=127.0.0.1:2 DOCSVC_S3_ACCESS_KEY=a DOCSVC_S3_SECRET_KEY=b DOCSVC_WEBHOOK_SECRET=c \
  ./build/docsvc.exe; echo "exit=$?")
```
Expected: `apply migrations: acquire connection: ... connection refused`, `exit=1`, `real` well under 15 s.

- [ ] **Step 4: Run the whole Go suite**

Run: `go vet ./... && go test ./...`
Expected: all packages PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/docsvc
git commit -m "feat: add docsvc binary"
```

---

### Task 8: Node image, toolbox and compose

**Files:**
- Create: `images/node/Dockerfile`
- Create: `images/toolbox/Dockerfile`, `images/toolbox/requirements.txt`, `images/toolbox/requirements.yml`
- Create: `compose.yaml`
- Create: `docs/adr/0001-systemd-containers-as-nodes.md`
- Modify: `Makefile` (append targets)

**Interfaces:**
- Consumes: nothing.
- Produces: image `disavery/node:local` (Debian 12, systemd PID 1, sshd with root key login, python3); running container `disavery-toolbox` (image `disavery/toolbox:local`) at `172.31.0.2` on network `disavery-wan`, repo mounted at `/work`, volumes `/secrets`, `/escrow`, Docker socket mounted, `SOPS_AGE_KEY_FILE=/secrets/age/keys.txt`, `ANSIBLE_CONFIG=/work/infra/ansible/ansible.cfg`; tools: go, terraform, ansible-playbook, ansible-lint, sops, age, step, mc, docker CLI, psql, dig, jq, ssh. Make targets `toolbox`, `images`.

- [ ] **Step 1: Write the node image**

`images/node/Dockerfile`:
```dockerfile
# A "machine" for the lab: systemd as PID 1 plus sshd, so Ansible can manage it
# exactly like a VM. See docs/adr/0001-systemd-containers-as-nodes.md.
FROM debian:12

ENV container=docker DEBIAN_FRONTEND=noninteractive

RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      systemd systemd-sysv dbus openssh-server sudo python3 python3-apt \
      ca-certificates curl gnupg iproute2 procps less \
 && apt-get clean && rm -rf /var/lib/apt/lists/* \
 && rm -f /lib/systemd/system/multi-user.target.wants/getty* \
 && systemctl enable ssh \
 && mkdir -p /root/.ssh && chmod 700 /root/.ssh \
 && sed -ri 's/^#?PermitRootLogin .*/PermitRootLogin prohibit-password/; s/^#?PasswordAuthentication .*/PasswordAuthentication no/' /etc/ssh/sshd_config

# Host keys are baked into the image and therefore shared by all nodes; acceptable
# for a local lab where Ansible disables host key checking.

STOPSIGNAL SIGRTMIN+3
CMD ["/lib/systemd/systemd"]
```

- [ ] **Step 2: Write the toolbox image**

`images/toolbox/requirements.txt`:
```
ansible-core==2.17.5
ansible-lint==24.9.2
```

`images/toolbox/requirements.yml`:
```yaml
collections:
  - name: community.sops
    version: 1.9.1
  - name: community.postgresql
    version: 3.6.1
```

`images/toolbox/Dockerfile`:
```dockerfile
# Pinned operator tooling. Terraform, Ansible and the drill CLI all run here so
# every user and CI job gets identical versions (Ansible does not run on Windows).
FROM golang:1.26-bookworm

ARG TERRAFORM_VERSION=1.9.8
ARG SOPS_VERSION=3.9.1
ARG AGE_VERSION=1.2.0
ARG STEP_VERSION=0.27.4
ARG MC_RELEASE=RELEASE.2024-10-08T09-37-26Z
ARG DOCKER_VERSION=27.3.1

RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      python3 python3-venv openssh-client postgresql-client dnsutils jq unzip curl ca-certificates \
 && apt-get clean && rm -rf /var/lib/apt/lists/*

RUN curl -fsSLo /tmp/tf.zip "https://releases.hashicorp.com/terraform/${TERRAFORM_VERSION}/terraform_${TERRAFORM_VERSION}_linux_amd64.zip" \
 && unzip -q /tmp/tf.zip -d /usr/local/bin && rm /tmp/tf.zip \
 && curl -fsSLo /usr/local/bin/sops "https://github.com/getsops/sops/releases/download/v${SOPS_VERSION}/sops-v${SOPS_VERSION}.linux.amd64" \
 && chmod +x /usr/local/bin/sops \
 && curl -fsSL "https://github.com/FiloSottile/age/releases/download/v${AGE_VERSION}/age-v${AGE_VERSION}-linux-amd64.tar.gz" \
    | tar -xz -C /usr/local/bin --strip-components=1 age/age age/age-keygen \
 && curl -fsSL "https://github.com/smallstep/cli/releases/download/v${STEP_VERSION}/step_linux_${STEP_VERSION}_amd64.tar.gz" \
    | tar -xz -C /usr/local/bin --strip-components=2 "step_${STEP_VERSION}/bin/step" \
 && curl -fsSLo /usr/local/bin/mc "https://dl.min.io/client/mc/release/linux-amd64/archive/mc.${MC_RELEASE}" \
 && chmod +x /usr/local/bin/mc \
 && curl -fsSL "https://download.docker.com/linux/static/stable/x86_64/docker-${DOCKER_VERSION}.tgz" \
    | tar -xz -C /usr/local/bin --strip-components=1 docker/docker

COPY requirements.txt requirements.yml /tmp/
RUN python3 -m venv /opt/ansible \
 && /opt/ansible/bin/pip install --no-cache-dir -r /tmp/requirements.txt \
 && /opt/ansible/bin/ansible-galaxy collection install -r /tmp/requirements.yml -p /usr/share/ansible/collections \
 && mkdir -p /root/.terraform.d/plugin-cache

ENV PATH=/opt/ansible/bin:$PATH
```

- [ ] **Step 3: Write `compose.yaml`**

```yaml
name: disavery

services:
  toolbox:
    build: images/toolbox
    image: disavery/toolbox:local
    container_name: disavery-toolbox
    init: true
    command: ["sleep", "infinity"]
    working_dir: /work
    environment:
      SOPS_AGE_KEY_FILE: /secrets/age/keys.txt
      ANSIBLE_CONFIG: /work/infra/ansible/ansible.cfg
      TF_PLUGIN_CACHE_DIR: /root/.terraform.d/plugin-cache
    volumes:
      - .:/work
      - /var/run/docker.sock:/var/run/docker.sock
      - secrets:/secrets
      - escrow:/escrow
      - gocache:/root/.cache/go-build
      - gomod:/go/pkg/mod
      - tfcache:/root/.terraform.d/plugin-cache
    networks:
      wan:
        ipv4_address: 172.31.0.2

networks:
  wan:
    name: disavery-wan
    labels:
      disavery.env: drill
    ipam:
      config:
        - subnet: 172.31.0.0/24

volumes:
  secrets:
    name: disavery-secrets
  escrow:
    name: disavery-escrow
  gocache: {}
  gomod: {}
  tfcache: {}
```

- [ ] **Step 4: Append Make targets**

Append to `Makefile`:
```make

TB := docker compose exec -T toolbox

.PHONY: toolbox
toolbox: ## Build and start the toolbox container
	docker compose up -d --build toolbox

.PHONY: images
images: ## Build the node image
	docker build -t disavery/node:local images/node
```

- [ ] **Step 5: Write ADR 0001**

`docs/adr/0001-systemd-containers-as-nodes.md`:
```markdown
# ADR 0001: Systemd containers as lab "machines"

- Status: accepted
- Date: 2026-10-07

## Context
The lab needs machines that Terraform can create and destroy quickly and that
Ansible can configure over SSH, so the same roles later run on cloud VMs.
Real VMs (Hyper-V, Vagrant) take minutes per rebuild and are hard to run in CI.

## Decision
Each machine is a privileged Debian 12 container running systemd as PID 1 with
sshd, created by Terraform's Docker provider and configured by Ansible.

## Consequences
- A site rebuilds in under a minute and the whole lab runs on a GitHub runner.
- Ansible roles are VM-ready; the cloud profile only swaps the Terraform provider.
- Containers share the host kernel and use overlay storage, so fsync/disk
  latency, kernel tunables and failure modes differ from VMs. Measured RTOs are
  lab numbers, not production predictions.
- Containers run privileged; acceptable for a local lab only.
```

- [ ] **Step 6: Verify the images**

Start Docker Desktop first, then:
```bash
make images toolbox
docker run -d --name node-check --privileged --tmpfs /run --tmpfs /run/lock disavery/node:local
sleep 5
docker exec node-check systemctl is-system-running --wait; docker exec node-check systemctl is-active ssh
docker rm -f node-check
docker compose exec toolbox bash -c 'terraform version && ansible --version | head -1 && sops --version && age --version && step version && mc --version | head -1 && docker version --format "{{.Client.Version}}" && go version'
```
Expected: `running` (or `degraded`, acceptable), `active`; every tool prints its pinned version.

- [ ] **Step 7: Commit**

```bash
git add images compose.yaml Makefile docs/adr/0001-systemd-containers-as-nodes.md
git commit -m "feat: add node image, toolbox image and compose setup"
```

---

### Task 9: Secrets bootstrap

**Files:**
- Create: `scripts/init-secrets.sh`
- Modify: `Makefile` (append target)

**Interfaces:**
- Consumes: toolbox (Task 8).
- Produces: `/secrets/age/keys.txt`, `/escrow/age-keys.txt` (identical), `/secrets/ssh/id_ed25519{,.pub}`, `/secrets/local.sops.yaml` with keys:
  `postgres.{app_password,replication_password}`,
  `minio.{site_root_user,site_root_password,app_access_key,app_secret_key,vault_root_user,vault_root_password,vault_writer_access_key,vault_writer_secret_key}`,
  `pgbackrest.repo2_cipher_pass`, `webhook.hmac_secret`,
  `tls.{ca_crt,ca_key,docs_crt,docs_key,vault_crt,vault_key}`.
  Make target `secrets`.

- [ ] **Step 1: Write the script**

`scripts/init-secrets.sh`:
```bash
#!/usr/bin/env bash
# Generates every local secret once and stores it SOPS-encrypted in /secrets.
# The age key is copied to /escrow, a separate volume used for secret-loss drills (S5).
# Usage (inside the toolbox): bash scripts/init-secrets.sh [--force]
set -euo pipefail

SECRETS=/secrets
ESCROW=/escrow
OUT=$SECRETS/local.sops.yaml

if [[ -f $OUT && ${1:-} != --force ]]; then
  echo "secrets already exist at $OUT (use --force to regenerate)"
  exit 0
fi

umask 077
rm -rf "$SECRETS/age" "$SECRETS/ssh"
mkdir -p "$SECRETS/age" "$SECRETS/ssh" "$ESCROW"

age-keygen -o "$SECRETS/age/keys.txt" 2>/dev/null
cp "$SECRETS/age/keys.txt" "$ESCROW/age-keys.txt"
ssh-keygen -q -t ed25519 -N '' -C disavery -f "$SECRETS/ssh/id_ed25519"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

step certificate create "disavery local CA" "$tmp/ca.crt" "$tmp/ca.key" \
  --profile root-ca --no-password --insecure --not-after 87600h
step certificate create docs.disavery.test "$tmp/docs.crt" "$tmp/docs.key" \
  --profile leaf --ca "$tmp/ca.crt" --ca-key "$tmp/ca.key" --no-password --insecure \
  --not-after 8760h --san docs.disavery.test --san direct.docs.disavery.test
step certificate create vault "$tmp/vault.crt" "$tmp/vault.key" \
  --profile leaf --ca "$tmp/ca.crt" --ca-key "$tmp/ca.key" --no-password --insecure \
  --not-after 8760h --san vault

rand() { python3 -c 'import secrets; print(secrets.token_hex(24))'; }
block() { sed 's/^/    /' "$1"; }

cat > "$tmp/plain.yaml" <<EOF
postgres:
  app_password: $(rand)
  replication_password: $(rand)
minio:
  site_root_user: siteadmin
  site_root_password: $(rand)
  app_access_key: docsvc
  app_secret_key: $(rand)
  vault_root_user: vaultadmin
  vault_root_password: $(rand)
  vault_writer_access_key: prodwriter
  vault_writer_secret_key: $(rand)
pgbackrest:
  repo2_cipher_pass: $(rand)
webhook:
  hmac_secret: $(rand)
tls:
  ca_crt: |
$(block "$tmp/ca.crt")
  ca_key: |
$(block "$tmp/ca.key")
  docs_crt: |
$(block "$tmp/docs.crt")
  docs_key: |
$(block "$tmp/docs.key")
  vault_crt: |
$(block "$tmp/vault.crt")
  vault_key: |
$(block "$tmp/vault.key")
EOF

sops --encrypt --age "$(age-keygen -y "$SECRETS/age/keys.txt")" \
  --input-type yaml --output-type yaml "$tmp/plain.yaml" > "$OUT"

echo "wrote $OUT; age key escrowed to $ESCROW/age-keys.txt"
```

- [ ] **Step 2: Append the Make target**

```make

.PHONY: secrets
secrets: toolbox ## Generate local secrets once (SOPS + age, escrow copy)
	$(TB) bash scripts/init-secrets.sh
```

- [ ] **Step 3: Verify**

```bash
make secrets
make secrets   # second run must be a no-op
docker compose exec -T toolbox bash -c '
  sops -d --extract "[\"minio\"][\"vault_writer_access_key\"]" /secrets/local.sops.yaml; echo
  sops -d --extract "[\"tls\"][\"docs_crt\"]" /secrets/local.sops.yaml | step certificate inspect --short -
  cmp /secrets/age/keys.txt /escrow/age-keys.txt && echo escrow-ok
  grep -c "ENC\[" /secrets/local.sops.yaml'
```
Expected: second run prints `secrets already exist`; output shows `prodwriter`, a certificate with SANs `docs.disavery.test, direct.docs.disavery.test` issued by `disavery local CA`, `escrow-ok`, and a non-zero count of encrypted values.

- [ ] **Step 4: Commit**

```bash
git add scripts/init-secrets.sh Makefile
git commit -m "feat: add SOPS/age secrets bootstrap with key escrow"
```

---

### Task 10: Terraform — networks, nodes, global zone and inventory

**Files:**
- Create: `infra/terraform/modules/node/{main.tf,variables.tf,outputs.tf}`
- Create: `infra/terraform/modules/site/{main.tf,variables.tf}`
- Create: `infra/terraform/envs/local/{versions.tf,variables.tf,main.tf,global.tf,sites.tf,vault.tf,inventory.tf,outputs.tf}`
- Create: `infra/terraform/envs/local/templates/{Corefile.tftpl,Caddyfile.tftpl}`
- Create: `docs/adr/0002-global-zone-survives-site-loss.md`
- Modify: `Makefile` (append targets)

**Interfaces:**
- Consumes: `disavery/node:local`, network `disavery-wan` (Task 8); `/secrets/ssh/id_ed25519.pub`, `/secrets/local.sops.yaml` (Task 9).
- Produces: containers `dns`, `edge`, `webhook`, `vault`, `app-a`, `db-a`, `obj-a` (site-b nodes when `site_b_enabled=true`), all with labels `disavery.env=drill`, `disavery.node=<name>`; module address `module.site["a"]` (plan 3's site-loss drill targets it); variables `site_a_enabled`, `site_b_enabled`, `active_site`; generated `infra/ansible/inventory/hosts.yml` with groups `webhooks`, `vaults`, `app`, `db`, `obj`, `site_a`, `site_b`, host var `wan_ip` and `site`, and `all.vars.active_site`. Make targets `infra`, `down`.

- [ ] **Step 1: Write the node module**

`infra/terraform/modules/node/variables.tf`:
```hcl
variable "name" {
  description = "Container name and hostname."
  type        = string
}

variable "image" {
  description = "Node image name."
  type        = string
}

variable "networks" {
  description = "Networks to attach; the first one with an address is the WAN."
  type = list(object({
    name         = string
    ipv4_address = optional(string)
  }))
}

variable "dns" {
  description = "Upstream DNS servers for Docker's embedded resolver."
  type        = list(string)
  default     = []
}

variable "uploads" {
  description = "Files to place in the container at creation: path => content."
  type        = map(string)
  default     = {}
}

variable "labels" {
  description = "Extra container labels."
  type        = map(string)
  default     = {}
}
```

`infra/terraform/modules/node/main.tf`:
```hcl
# A lab machine: systemd + sshd container (see ADR 0001).
resource "docker_container" "this" {
  name       = var.name
  hostname   = var.name
  image      = var.image
  privileged = true
  must_run   = true
  restart    = "no"
  dns        = var.dns
  tmpfs = {
    "/run"      = "rw"
    "/run/lock" = "rw"
  }

  dynamic "networks_advanced" {
    for_each = var.networks
    content {
      name         = networks_advanced.value.name
      ipv4_address = networks_advanced.value.ipv4_address
      aliases      = [var.name]
    }
  }

  dynamic "upload" {
    for_each = var.uploads
    content {
      file    = upload.key
      content = upload.value
    }
  }

  dynamic "labels" {
    for_each = merge({ "disavery.env" = "drill", "disavery.node" = var.name }, var.labels)
    content {
      label = labels.key
      value = labels.value
    }
  }
}
```

`infra/terraform/modules/node/outputs.tf`:
```hcl
output "name" {
  value = docker_container.this.name
}
```

- [ ] **Step 2: Write the site module**

`infra/terraform/modules/site/variables.tf`:
```hcl
variable "site" {
  description = "Site key, e.g. \"a\"."
  type        = string
}

variable "subnet" {
  description = "Zone network CIDR."
  type        = string
}

variable "wan_network" {
  type = string
}

variable "wan_ips" {
  description = "Role => WAN address, e.g. { app = \"172.31.0.21\" }."
  type        = map(string)
}

variable "image" {
  type = string
}

variable "dns" {
  type = list(string)
}

variable "uploads" {
  type = map(string)
}
```

`infra/terraform/modules/site/main.tf`:
```hcl
# One site: a zone network plus app, db and obj nodes.
resource "docker_network" "zone" {
  name = "disavery-site-${var.site}"
  ipam_config {
    subnet = var.subnet
  }
  labels {
    label = "disavery.env"
    value = "drill"
  }
}

module "node" {
  source   = "../node"
  for_each = var.wan_ips

  name    = "${each.key}-${var.site}"
  image   = var.image
  dns     = var.dns
  uploads = var.uploads
  networks = [
    { name = var.wan_network, ipv4_address = each.value },
    { name = docker_network.zone.name },
  ]
  labels = {
    "disavery.site" = var.site
    "disavery.role" = each.key
  }
}
```

- [ ] **Step 3: Write the environment**

`infra/terraform/envs/local/versions.tf`:
```hcl
terraform {
  required_version = ">= 1.9.0"
  required_providers {
    docker = {
      source  = "kreuzwerker/docker"
      version = "~> 3.0.2"
    }
    sops = {
      source  = "carlpett/sops"
      version = "~> 1.1.1"
    }
    local = {
      source  = "hashicorp/local"
      version = "~> 2.5"
    }
  }
}

provider "docker" {
  host = "unix:///var/run/docker.sock"
}

provider "sops" {}
```

`infra/terraform/envs/local/variables.tf`:
```hcl
variable "site_a_enabled" {
  type    = bool
  default = true
}

variable "site_b_enabled" {
  type    = bool
  default = false
}

variable "active_site" {
  description = "Site that receives traffic (DNS direct record, edge upstream, webhook allowlist)."
  type        = string
  default     = "a"
  validation {
    condition     = contains(["a", "b"], var.active_site)
    error_message = "active_site must be \"a\" or \"b\"."
  }
}

variable "wan_subnet" {
  type    = string
  default = "172.31.0.0/24"
}

variable "edge_host_port" {
  description = "Host port published for the edge proxy's HTTPS listener."
  type        = number
  default     = 8443
}

variable "node_image" {
  type    = string
  default = "disavery/node:local"
}

variable "ssh_public_key_path" {
  type    = string
  default = "/secrets/ssh/id_ed25519.pub"
}

variable "secrets_file" {
  type    = string
  default = "/secrets/local.sops.yaml"
}
```

`infra/terraform/envs/local/main.tf`:
```hcl
data "docker_network" "wan" {
  name = "disavery-wan" # created by compose.yaml so the toolbox can join it
}

data "sops_file" "secrets" {
  source_file = var.secrets_file
}

locals {
  site_roles = { app = 1, db = 2, obj = 3 }
  site_cfg = {
    a = { enabled = var.site_a_enabled, base = 20, subnet = "172.31.1.0/24" }
    b = { enabled = var.site_b_enabled, base = 30, subnet = "172.31.2.0/24" }
  }
  enabled_sites = [for s, c in local.site_cfg : s if c.enabled]

  # Every address in the lab, including disabled sites, so DNS/inventory can refer to them.
  ip = merge(
    {
      dns     = cidrhost(var.wan_subnet, 10)
      edge    = cidrhost(var.wan_subnet, 11)
      webhook = cidrhost(var.wan_subnet, 12)
      vault   = cidrhost(var.wan_subnet, 40)
    },
    {
      for pair in setproduct(keys(local.site_cfg), keys(local.site_roles)) :
      "${pair[1]}-${pair[0]}" => cidrhost(var.wan_subnet, local.site_cfg[pair[0]].base + local.site_roles[pair[1]])
    },
  )

  node_uploads = {
    "/root/.ssh/authorized_keys" = file(var.ssh_public_key_path)
  }
}
```

`infra/terraform/envs/local/global.tf`:
```hcl
# Global zone: stands in for external DNS, CDN/edge and a third-party provider.
# Assumed to survive the loss of a site (ADR 0002).

resource "docker_image" "coredns" {
  name         = "coredns/coredns:1.11.3"
  keep_locally = true
}

resource "docker_image" "caddy" {
  name         = "caddy:2.8.4"
  keep_locally = true
}

resource "docker_container" "dns" {
  name    = "dns"
  image   = docker_image.coredns.image_id
  command = ["-conf", "/Corefile"]
  restart = "unless-stopped"

  networks_advanced {
    name         = data.docker_network.wan.name
    ipv4_address = local.ip.dns
  }

  upload {
    file = "/Corefile"
    content = templatefile("${path.module}/templates/Corefile.tftpl", {
      edge_ip   = local.ip.edge
      direct_ip = local.ip["app-${var.active_site}"]
    })
  }

  labels {
    label = "disavery.env"
    value = "drill"
  }
}

resource "docker_container" "edge" {
  name    = "edge"
  image   = docker_image.caddy.image_id
  restart = "unless-stopped"
  dns     = [local.ip.dns]

  ports {
    internal = 443
    external = var.edge_host_port
  }

  networks_advanced {
    name         = data.docker_network.wan.name
    ipv4_address = local.ip.edge
  }

  upload {
    file    = "/etc/caddy/Caddyfile"
    content = templatefile("${path.module}/templates/Caddyfile.tftpl", { upstream = "app-${var.active_site}:8080" })
  }
  upload {
    file    = "/etc/caddy/docs.crt"
    content = data.sops_file.secrets.data["tls.docs_crt"]
  }
  upload {
    file    = "/etc/caddy/docs.key"
    content = data.sops_file.secrets.data["tls.docs_key"]
  }

  labels {
    label = "disavery.env"
    value = "drill"
  }
}

module "webhook" {
  source  = "../../modules/node"
  name    = "webhook"
  image   = var.node_image
  dns     = [local.ip.dns]
  uploads = local.node_uploads
  networks = [
    { name = data.docker_network.wan.name, ipv4_address = local.ip.webhook },
  ]
  labels = { "disavery.role" = "webhook" }
}
```

`infra/terraform/envs/local/templates/Corefile.tftpl`:
```
disavery.test:53 {
    hosts {
        ${edge_ip} docs.disavery.test
        ${direct_ip} direct.docs.disavery.test
        ttl 30
    }
    errors
    log
}

.:53 {
    forward . /etc/resolv.conf
    cache 30
    errors
}
```

`infra/terraform/envs/local/templates/Caddyfile.tftpl`:
```
{
	admin off
	auto_https disable_redirects
}

docs.disavery.test:443 {
	tls /etc/caddy/docs.crt /etc/caddy/docs.key
	reverse_proxy ${upstream} {
		health_uri /readyz
		health_interval 2s
		fail_duration 5s
	}
}
```

`infra/terraform/envs/local/sites.tf`:
```hcl
module "site" {
  source   = "../../modules/site"
  for_each = toset(local.enabled_sites)

  site        = each.key
  subnet      = local.site_cfg[each.key].subnet
  wan_network = data.docker_network.wan.name
  wan_ips     = { for role in keys(local.site_roles) : role => local.ip["${role}-${each.key}"] }
  image       = var.node_image
  dns         = [local.ip.dns]
  uploads     = local.node_uploads
}
```

`infra/terraform/envs/local/vault.tf`:
```hcl
# The vault stands in for a separate cloud account holding immutable backups.
resource "docker_network" "vault" {
  name = "disavery-vault"
  ipam_config {
    subnet = "172.31.3.0/24"
  }
  labels {
    label = "disavery.env"
    value = "drill"
  }
}

module "vault" {
  source  = "../../modules/node"
  name    = "vault"
  image   = var.node_image
  dns     = [local.ip.dns]
  uploads = local.node_uploads
  networks = [
    { name = data.docker_network.wan.name, ipv4_address = local.ip.vault },
    { name = docker_network.vault.name },
  ]
  labels = { "disavery.role" = "vault" }
}
```

`infra/terraform/envs/local/inventory.tf`:
```hcl
# Terraform owns the address plan, so it also writes the Ansible inventory.
resource "local_file" "inventory" {
  filename        = "${path.module}/../../../ansible/inventory/hosts.yml"
  file_permission = "0644"
  content = yamlencode({
    all = {
      vars = { active_site = var.active_site }
      children = merge(
        {
          webhooks = { hosts = { webhook = { wan_ip = local.ip.webhook } } }
          vaults   = { hosts = { vault = { wan_ip = local.ip.vault } } }
        },
        {
          for role in keys(local.site_roles) : role => {
            hosts = { for s in local.enabled_sites : "${role}-${s}" => { wan_ip = local.ip["${role}-${s}"], site = s } }
          }
        },
        {
          for s in keys(local.site_cfg) : "site_${s}" => {
            hosts = { for role in keys(local.site_roles) : "${role}-${s}" => {} if local.site_cfg[s].enabled }
          }
        },
      )
    }
  })
}
```

`infra/terraform/envs/local/outputs.tf`:
```hcl
output "ip" {
  value = local.ip
}

output "active_site" {
  value = var.active_site
}
```

- [ ] **Step 4: Write ADR 0002**

`docs/adr/0002-global-zone-survives-site-loss.md`:
```markdown
# ADR 0002: The global zone survives site loss

- Status: accepted
- Date: 2026-10-07

## Context
In production, DNS, the CDN/edge and third-party providers are run by other
companies in other failure domains. The lab must still model them.

## Decision
CoreDNS, the Caddy edge and the webhook mock form a "global" zone that drills
never destroy. DNS records and the edge upstream are Terraform-managed (variable
`active_site`), the way Route 53 records would be.

## Consequences
- Site-loss drills measure recovery of the site, not of the internet.
- Repointing traffic is an explicit, timed runbook step (Terraform apply on the
  global zone), and DNS TTL (30 s) is part of measured RTO.
- Failure of the DNS/edge provider itself is out of scope for v1.
```

- [ ] **Step 5: Append Make targets**

```make

TF_DIR := infra/terraform/envs/local

.PHONY: infra
infra: toolbox ## Create networks and containers with Terraform
	$(TB) bash -c 'cd $(TF_DIR) && terraform init -input=false && terraform workspace select -or-create drill && terraform apply -input=false -auto-approve'

.PHONY: down
down: ## Destroy Terraform-managed containers (keeps secrets and toolbox)
	$(TB) bash -c 'cd $(TF_DIR) && terraform workspace select drill && terraform destroy -input=false -auto-approve'
```

- [ ] **Step 6: Verify**

```bash
docker compose exec -T toolbox bash -c 'terraform fmt -check -recursive infra/terraform && cd infra/terraform/envs/local && terraform init -input=false >/dev/null && terraform validate'
make infra
docker ps --filter label=disavery.env=drill --format '{{.Names}}' | sort
docker compose exec -T toolbox bash -c '
  ssh -i /secrets/ssh/id_ed25519 -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR root@db-a hostname
  dig +short @172.31.0.10 docs.disavery.test
  dig +short @172.31.0.10 direct.docs.disavery.test
  ssh -i /secrets/ssh/id_ed25519 -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR root@db-a getent hosts deb.debian.org >/dev/null && echo internet-dns-ok
  cat infra/ansible/inventory/hosts.yml'
make infra   # second apply
```
Expected: `Success! The configuration is valid.`; containers `app-a db-a dns edge obj-a vault webhook`; `db-a`; `172.31.0.11`; `172.31.0.21`; `internet-dns-ok`; inventory lists groups `app`, `db`, `obj` with `-a` hosts, `site_a` populated, `site_b` with empty `hosts`; the second `make infra` reports `No changes.`

- [ ] **Step 7: Commit**

```bash
git add infra/terraform docs/adr/0002-global-zone-survives-site-loss.md Makefile
git commit -m "feat: add Terraform lab environment and generated inventory"
```

(`.terraform.lock.hcl` under `envs/local` is committed with this change.)

---

### Task 11: Ansible skeleton and base role

**Files:**
- Create: `infra/ansible/ansible.cfg`, `infra/ansible/.ansible-lint`
- Create: `infra/ansible/inventory/group_vars/all.yml`
- Create: `infra/ansible/playbooks/site.yml`
- Create: `infra/ansible/roles/base/{tasks/main.yml,handlers/main.yml}`
- Modify: `Makefile` (append target)

**Interfaces:**
- Consumes: inventory from Task 10; secrets from Task 9.
- Produces: fact `secrets` (decrypted SOPS map) on every host for the rest of the play run; disavery CA trusted system-wide on every node at `/usr/local/share/ca-certificates/disavery-ca.crt`; group vars `pg_version: 16`, `pgbackrest_stanza: main`, `wan_subnet`, `secrets_file`, `minio_release`, `mc_release`, `vault_retention_days: 14`; play variable `target_site` (extra var, default `a`). Make target `configure`.

- [ ] **Step 1: Write configuration and group vars**

`infra/ansible/ansible.cfg`:
```ini
[defaults]
inventory = /work/infra/ansible/inventory/hosts.yml
roles_path = /work/infra/ansible/roles
host_key_checking = False
interpreter_python = /usr/bin/python3
callback_result_format = yaml
forks = 10

[ssh_connection]
pipelining = True
```

`infra/ansible/.ansible-lint`:
```yaml
profile: basic
exclude_paths:
  - inventory/hosts.yml
```

`infra/ansible/inventory/group_vars/all.yml`:
```yaml
ansible_user: root
ansible_ssh_private_key_file: /secrets/ssh/id_ed25519
ansible_ssh_common_args: "-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR"

secrets_file: /secrets/local.sops.yaml
wan_subnet: 172.31.0.0/24
disavery_ca_path: /usr/local/share/ca-certificates/disavery-ca.crt

pg_version: 16
pgbackrest_stanza: main

minio_release: RELEASE.2024-10-13T13-34-11Z
mc_release: RELEASE.2024-10-08T09-37-26Z
vault_retention_days: 14
```

- [ ] **Step 2: Write the base role**

`infra/ansible/roles/base/tasks/main.yml`:
```yaml
- name: Install base packages
  ansible.builtin.apt:
    name: [ca-certificates, curl, gnupg, jq]
    update_cache: true
    cache_valid_time: 3600

- name: Trust the disavery CA
  ansible.builtin.copy:
    content: "{{ secrets.tls.ca_crt }}"
    dest: "{{ disavery_ca_path }}"
    mode: "0644"
  notify: Update CA certificates

- name: Apply CA changes now
  ansible.builtin.meta: flush_handlers
```

`infra/ansible/roles/base/handlers/main.yml`:
```yaml
- name: Update CA certificates
  ansible.builtin.command: update-ca-certificates
  changed_when: true
```

- [ ] **Step 3: Write the first version of the site playbook**

`infra/ansible/playbooks/site.yml`:
```yaml
# Builds the lab. Target a site with -e target_site=a|b (default a).
- name: Prepare all nodes
  hosts: all
  gather_facts: false
  tasks:
    - name: Wait for SSH
      ansible.builtin.wait_for_connection:
        timeout: 60

    - name: Load SOPS secrets
      community.sops.load_vars:
        file: "{{ secrets_file }}"
        name: secrets

    - name: Gather facts
      ansible.builtin.setup:

- name: Base configuration
  hosts: all
  roles:
    - base
```

- [ ] **Step 4: Append the Make target**

```make

.PHONY: configure
configure: toolbox ## Configure all nodes with Ansible
	$(TB) ansible-playbook infra/ansible/playbooks/site.yml
```

- [ ] **Step 5: Verify**

```bash
make configure
docker exec db-a test -f /usr/local/share/ca-certificates/disavery-ca.crt && echo ca-installed
docker compose exec -T toolbox bash -c 'cd infra/ansible && ansible-lint playbooks roles'
```
Expected: PLAY RECAP shows `failed=0` for all 5 hosts (`app-a db-a obj-a vault webhook`); `ca-installed`; ansible-lint `Passed`.

- [ ] **Step 6: Commit**

```bash
git add infra/ansible Makefile
git commit -m "feat: add Ansible skeleton and base role"
```

---

### Task 12: MinIO role — site store and immutable vault

**Files:**
- Create: `infra/ansible/roles/minio/defaults/main.yml`
- Create: `infra/ansible/roles/minio/tasks/{main.yml,site.yml,vault.yml}`
- Create: `infra/ansible/roles/minio/handlers/main.yml`
- Create: `infra/ansible/roles/minio/templates/{minio.env.j2,minio.service.j2,prod-writer.json.j2}`
- Create: `infra/ansible/roles/minio/files/docsvc-policy.json`
- Create: `docs/adr/0003-minio-pinned-release.md`
- Modify: `infra/ansible/playbooks/site.yml`

**Interfaces:**
- Consumes: `secrets.minio.*`, `secrets.tls.vault_{crt,key}`, `secrets.tls.ca_crt`; group vars from Task 11.
- Produces:
  - `vault` node: HTTPS MinIO at `https://vault:9000`; buckets `pgbackrest` and `attachments` with Object Lock, default retention compliance `vault_retention_days`, noncurrent versions expire after `vault_retention_days + 1` days; user `prodwriter` with policy `prod-writer` (read/write/delete-marker only; no version deletion, no bypass, no bucket configuration changes).
  - `obj-<site>` node: HTTP MinIO at `http://obj-<site>:9000`; bucket `attachments` with versioning; user `docsvc` with policy `docsvc-rw`; bucket replication of new objects and delete markers to `vault/attachments`.
  - Role parameter `minio_profile: site | vault`.

- [ ] **Step 1: Write defaults, handlers and templates**

`infra/ansible/roles/minio/defaults/main.yml`:
```yaml
minio_profile: site # site | vault
minio_data_dir: /var/lib/minio
minio_certs_dir: /etc/minio/certs
minio_bin_url: "https://dl.min.io/server/minio/release/linux-amd64/archive/minio.{{ minio_release }}"
mc_bin_url: "https://dl.min.io/client/mc/release/linux-amd64/archive/mc.{{ mc_release }}"
minio_scheme: "{{ 'https' if minio_profile == 'vault' else 'http' }}"
minio_root_user: "{{ secrets.minio.vault_root_user if minio_profile == 'vault' else secrets.minio.site_root_user }}"
minio_root_password: "{{ secrets.minio.vault_root_password if minio_profile == 'vault' else secrets.minio.site_root_password }}"
```

`infra/ansible/roles/minio/handlers/main.yml`:
```yaml
- name: Restart MinIO
  ansible.builtin.systemd_service:
    name: minio
    state: restarted
    daemon_reload: true
```

`infra/ansible/roles/minio/templates/minio.env.j2`:
```
MINIO_ROOT_USER={{ minio_root_user }}
MINIO_ROOT_PASSWORD={{ minio_root_password }}
MINIO_VOLUMES={{ minio_data_dir }}
MINIO_OPTS="--address :9000 --console-address :9001 --certs-dir {{ minio_certs_dir }}"
```

`infra/ansible/roles/minio/templates/minio.service.j2`:
```ini
[Unit]
Description=MinIO ({{ minio_profile }})
Wants=network-online.target
After=network-online.target

[Service]
User=minio-user
Group=minio-user
EnvironmentFile=/etc/default/minio
ExecStart=/usr/local/bin/minio server $MINIO_OPTS $MINIO_VOLUMES
Restart=always
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
```

`infra/ansible/roles/minio/templates/prod-writer.json.j2`:
```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "s3:GetBucketLocation", "s3:ListBucket", "s3:ListBucketVersions",
        "s3:ListBucketMultipartUploads", "s3:GetBucketVersioning",
        "s3:GetBucketObjectLockConfiguration", "s3:GetReplicationConfiguration"
      ],
      "Resource": ["arn:aws:s3:::pgbackrest", "arn:aws:s3:::attachments"]
    },
    {
      "Effect": "Allow",
      "Action": [
        "s3:GetObject", "s3:GetObjectVersion", "s3:PutObject", "s3:DeleteObject",
        "s3:AbortMultipartUpload", "s3:ListMultipartUploadParts",
        "s3:GetObjectRetention", "s3:GetObjectLegalHold",
        "s3:ReplicateObject", "s3:ReplicateDelete", "s3:ReplicateTags",
        "s3:GetObjectVersionForReplication"
      ],
      "Resource": ["arn:aws:s3:::pgbackrest/*", "arn:aws:s3:::attachments/*"]
    },
    {
      "Effect": "Deny",
      "Action": [
        "s3:DeleteObjectVersion", "s3:BypassGovernanceRetention",
        "s3:PutObjectRetention", "s3:PutObjectLegalHold",
        "s3:PutBucketObjectLockConfiguration", "s3:PutBucketVersioning",
        "s3:PutLifecycleConfiguration", "s3:PutBucketPolicy", "s3:DeleteBucket"
      ],
      "Resource": ["arn:aws:s3:::*"]
    }
  ]
}
```

`infra/ansible/roles/minio/files/docsvc-policy.json`:
```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": ["s3:ListBucket", "s3:GetBucketLocation"],
      "Resource": ["arn:aws:s3:::attachments"]
    },
    {
      "Effect": "Allow",
      "Action": ["s3:GetObject", "s3:PutObject"],
      "Resource": ["arn:aws:s3:::attachments/*"]
    }
  ]
}
```

- [ ] **Step 2: Write the common tasks**

`infra/ansible/roles/minio/tasks/main.yml`:
```yaml
- name: Create minio-user
  ansible.builtin.user:
    name: minio-user
    system: true
    shell: /usr/sbin/nologin
    create_home: false

- name: Install MinIO server
  ansible.builtin.get_url:
    url: "{{ minio_bin_url }}"
    checksum: "sha256:{{ minio_bin_url }}.sha256sum"
    dest: /usr/local/bin/minio
    mode: "0755"
  notify: Restart MinIO

- name: Install MinIO client
  ansible.builtin.get_url:
    url: "{{ mc_bin_url }}"
    checksum: "sha256:{{ mc_bin_url }}.sha256sum"
    dest: /usr/local/bin/mc
    mode: "0755"

- name: Create MinIO directories
  ansible.builtin.file:
    path: "{{ item }}"
    state: directory
    owner: minio-user
    group: minio-user
    mode: "0750"
  loop:
    - "{{ minio_data_dir }}"
    - "{{ minio_certs_dir }}"
    - "{{ minio_certs_dir }}/CAs"

- name: Trust the disavery CA for outbound TLS (replication)
  ansible.builtin.copy:
    content: "{{ secrets.tls.ca_crt }}"
    dest: "{{ minio_certs_dir }}/CAs/disavery-ca.crt"
    owner: minio-user
    group: minio-user
    mode: "0644"
  notify: Restart MinIO

- name: Install the vault TLS certificate
  ansible.builtin.copy:
    content: "{{ item.content }}"
    dest: "{{ minio_certs_dir }}/{{ item.name }}"
    owner: minio-user
    group: minio-user
    mode: "{{ item.mode }}"
  loop:
    - { name: public.crt, content: "{{ secrets.tls.vault_crt }}", mode: "0644" }
    - { name: private.key, content: "{{ secrets.tls.vault_key }}", mode: "0600" }
  loop_control:
    label: "{{ item.name }}"
  when: minio_profile == 'vault'
  notify: Restart MinIO

- name: Write MinIO environment
  ansible.builtin.template:
    src: minio.env.j2
    dest: /etc/default/minio
    mode: "0600"
  notify: Restart MinIO

- name: Install MinIO unit
  ansible.builtin.template:
    src: minio.service.j2
    dest: /etc/systemd/system/minio.service
    mode: "0644"
  notify: Restart MinIO

- name: Start MinIO
  ansible.builtin.systemd_service:
    name: minio
    state: started
    enabled: true
    daemon_reload: true

- name: Apply MinIO restarts now
  ansible.builtin.meta: flush_handlers

- name: Wait for MinIO to be ready
  ansible.builtin.uri:
    url: "{{ minio_scheme }}://{{ inventory_hostname }}:9000/minio/health/ready"
    ca_path: "{{ disavery_ca_path }}"
  register: minio_ready
  until: minio_ready.status == 200
  retries: 30
  delay: 2

- name: Configure the local mc alias
  ansible.builtin.command: >-
    mc alias set local {{ minio_scheme }}://{{ inventory_hostname }}:9000
    {{ minio_root_user }} {{ minio_root_password }}
  changed_when: false
  no_log: true

- name: Apply profile-specific configuration
  ansible.builtin.include_tasks: "{{ minio_profile }}.yml"
```

- [ ] **Step 3: Write the vault tasks**

`infra/ansible/roles/minio/tasks/vault.yml`:
```yaml
- name: Create Object Lock buckets
  ansible.builtin.command: mc mb --ignore-existing --with-lock local/{{ item }}
  loop: [pgbackrest, attachments]
  register: vault_mb
  changed_when: "'created successfully' in vault_mb.stdout"

- name: Set default compliance retention
  ansible.builtin.command: mc retention set --default compliance {{ vault_retention_days }}d local/{{ item }}
  loop: [pgbackrest, attachments]
  changed_when: false

- name: Read lifecycle rules
  ansible.builtin.command: mc ilm rule ls --json local/{{ item }}
  loop: [pgbackrest, attachments]
  register: vault_ilm
  changed_when: false
  failed_when: false

- name: Expire noncurrent versions once the lock has lapsed
  ansible.builtin.command: mc ilm rule add --noncurrent-expire-days {{ vault_retention_days + 1 }} local/{{ item.item }}
  loop: "{{ vault_ilm.results }}"
  loop_control:
    label: "{{ item.item }}"
  when: "'NoncurrentVersionExpiration' not in item.stdout"
  changed_when: true

- name: Write the production writer policy
  ansible.builtin.template:
    src: prod-writer.json.j2
    dest: /etc/minio/prod-writer.json
    mode: "0644"

- name: Create the production writer policy
  ansible.builtin.command: mc admin policy create local prod-writer /etc/minio/prod-writer.json
  changed_when: false

- name: Create the production writer user
  ansible.builtin.command: >-
    mc admin user add local {{ secrets.minio.vault_writer_access_key }} {{ secrets.minio.vault_writer_secret_key }}
  changed_when: false
  no_log: true

- name: Attach the policy to the production writer
  ansible.builtin.command: mc admin policy attach local prod-writer --user {{ secrets.minio.vault_writer_access_key }}
  register: vault_attach
  changed_when: vault_attach.rc == 0
  failed_when: vault_attach.rc != 0 and 'already' not in (vault_attach.stderr + vault_attach.stdout)
```

- [ ] **Step 4: Write the site tasks**

`infra/ansible/roles/minio/tasks/site.yml`:
```yaml
- name: Create the attachments bucket
  ansible.builtin.command: mc mb --ignore-existing local/attachments
  register: site_mb
  changed_when: "'created successfully' in site_mb.stdout"

- name: Enable versioning
  ansible.builtin.command: mc version enable local/attachments
  changed_when: false

- name: Copy the docsvc policy
  ansible.builtin.copy:
    src: docsvc-policy.json
    dest: /etc/minio/docsvc-policy.json
    mode: "0644"

- name: Create the docsvc policy
  ansible.builtin.command: mc admin policy create local docsvc-rw /etc/minio/docsvc-policy.json
  changed_when: false

- name: Create the docsvc user
  ansible.builtin.command: >-
    mc admin user add local {{ secrets.minio.app_access_key }} {{ secrets.minio.app_secret_key }}
  changed_when: false
  no_log: true

- name: Attach the policy to docsvc
  ansible.builtin.command: mc admin policy attach local docsvc-rw --user {{ secrets.minio.app_access_key }}
  register: site_attach
  changed_when: site_attach.rc == 0
  failed_when: site_attach.rc != 0 and 'already' not in (site_attach.stderr + site_attach.stdout)

- name: Read replication rules
  ansible.builtin.command: mc replicate ls --json local/attachments
  register: site_replication
  changed_when: false
  failed_when: false

- name: Replicate attachments to the vault
  ansible.builtin.command: >-
    mc replicate add local/attachments
    --remote-bucket "https://{{ secrets.minio.vault_writer_access_key }}:{{ secrets.minio.vault_writer_secret_key }}@vault:9000/attachments"
    --replicate "delete-marker,existing-objects"
    --priority 1
  when: site_replication.rc != 0 or 'Enabled' not in site_replication.stdout
  changed_when: true
  no_log: true
```

- [ ] **Step 5: Extend the playbook**

Append to `infra/ansible/playbooks/site.yml`:
```yaml

- name: Vault object store
  hosts: vaults
  roles:
    - role: minio
      minio_profile: vault

- name: Site object store
  hosts: "obj:&site_{{ target_site | default('a') }}"
  roles:
    - role: minio
      minio_profile: site
```

- [ ] **Step 6: Write ADR 0003**

`docs/adr/0003-minio-pinned-release.md`:
```markdown
# ADR 0003: Pin an archived MinIO release

- Status: accepted
- Date: 2026-10-07

## Context
MinIO's community edition stopped publishing new pre-built binaries and images
in late 2025. The lab needs S3 Object Lock, versioning and bucket replication.

## Decision
Pin `minio` and `mc` to archived releases from `dl.min.io/.../archive/`
(`RELEASE.2024-10-13T13-34-11Z`, `RELEASE.2024-10-08T09-37-26Z`), verified by
their published SHA-256 sums. The same server tag is used by integration tests.

## Consequences
- Reproducible builds; no dependency on future MinIO distribution policy.
- No security fixes after the pinned date — acceptable for an isolated lab.
- If the archive disappears, alternatives with Object Lock support must be
  evaluated (e.g. Ceph RGW); the S3 API surface used here is standard.
```

- [ ] **Step 7: Verify, including the immutability guarantees**

```bash
make configure
docker compose exec -T toolbox bash -c '
  set -e
  mkdir -p ~/.mc/certs/CAs
  sops -d --extract "[\"tls\"][\"ca_crt\"]" /secrets/local.sops.yaml > ~/.mc/certs/CAs/disavery-ca.crt
  s() { sops -d --extract "$1" /secrets/local.sops.yaml; }
  mc alias set vw https://vault:9000 "$(s "[\"minio\"][\"vault_writer_access_key\"]")" "$(s "[\"minio\"][\"vault_writer_secret_key\"]")" >/dev/null
  mc alias set vr https://vault:9000 "$(s "[\"minio\"][\"vault_root_user\"]")" "$(s "[\"minio\"][\"vault_root_password\"]")" >/dev/null
  mc retention info --default vr/pgbackrest
  echo probe | mc pipe vw/attachments/probe.txt
  v=$(mc stat --json vw/attachments/probe.txt | jq -r .versionID)
  mc rm --version-id "$v" vw/attachments/probe.txt && echo "UNEXPECTED: writer deleted a version" || echo "writer denied: ok"
  mc rm --version-id "$v" vr/attachments/probe.txt && echo "UNEXPECTED: root deleted a locked version" || echo "root denied (WORM): ok"
  mc admin policy create vw x /dev/null 2>/dev/null && echo "UNEXPECTED: writer has admin" || echo "writer has no admin: ok"'
make configure   # second run
```
Expected: default retention `COMPLIANCE 14 days`; `writer denied: ok`; `root denied (WORM): ok`; `writer has no admin: ok`; the second run reports `failed=0` and `changed=0` for `vault` and `obj-a`. (The probe object stays in the vault for 14 days; that is the point.)

- [ ] **Step 8: Commit**

```bash
git add infra/ansible docs/adr/0003-minio-pinned-release.md
git commit -m "feat: add MinIO role with immutable vault and replication"
```

---

### Task 13: PostgreSQL and pgBackRest roles

**Files:**
- Create: `infra/ansible/roles/postgres/{defaults/main.yml,tasks/main.yml,handlers/main.yml,templates/disavery.conf.j2}`
- Create: `infra/ansible/roles/pgbackrest/{defaults/main.yml,tasks/main.yml,templates/pgbackrest.conf.j2,templates/pgbackrest-backup@.service.j2,templates/pgbackrest-full.timer.j2,templates/pgbackrest-incr.timer.j2}`
- Modify: `infra/ansible/playbooks/site.yml`

**Interfaces:**
- Consumes: `secrets.postgres.*`, `secrets.minio.vault_writer_*`, `secrets.pgbackrest.repo2_cipher_pass`; vault buckets from Task 12; group vars `pg_version`, `pgbackrest_stanza`, `wan_subnet`, `disavery_ca_path`.
- Produces: on `db-<site>`: PostgreSQL 16 cluster `16/main` with data checksums, listening on all interfaces; database `docsvc` owned by role `docsvc`; role `replicator` (REPLICATION); extension `amcheck` in `docsvc`; WAL archiving through pgBackRest stanza `main` to repo1 (`/var/lib/pgbackrest`, posix) and repo2 (`s3://pgbackrest/repo` on the vault, encrypted); at least one full backup in each repo; systemd timers `pgbackrest-full.timer` (daily 01:00) and `pgbackrest-incr.timer` (hourly), each backing up both repos. Service name `postgresql@16-main` (plan 3 stops/promotes it).

- [ ] **Step 1: Write the postgres role**

`infra/ansible/roles/postgres/defaults/main.yml`:
```yaml
pg_conf_dir: "/etc/postgresql/{{ pg_version }}/main"
pg_service: "postgresql@{{ pg_version }}-main"
pg_archive_timeout: 30
postgres_app_db: docsvc
postgres_app_user: docsvc
```

`infra/ansible/roles/postgres/handlers/main.yml`:
```yaml
- name: Restart PostgreSQL
  ansible.builtin.systemd_service:
    name: "{{ pg_service }}"
    state: restarted

- name: Reload PostgreSQL
  ansible.builtin.systemd_service:
    name: "{{ pg_service }}"
    state: reloaded
```

`infra/ansible/roles/postgres/templates/disavery.conf.j2`:
```
# Managed by Ansible (role postgres).
listen_addresses = '*'
wal_level = replica
max_wal_senders = 10
max_replication_slots = 10
wal_keep_size = '256MB'
hot_standby = on
archive_mode = on
archive_command = 'pgbackrest --stanza={{ pgbackrest_stanza }} archive-push %p'
archive_timeout = {{ pg_archive_timeout }}
```

`infra/ansible/roles/postgres/tasks/main.yml`:
```yaml
- name: Add the PGDG signing key
  ansible.builtin.get_url:
    url: https://www.postgresql.org/media/keys/ACCC4CF8.asc
    dest: /usr/share/keyrings/pgdg.asc
    mode: "0644"

- name: Add the PGDG repository
  ansible.builtin.copy:
    content: "deb [signed-by=/usr/share/keyrings/pgdg.asc] https://apt.postgresql.org/pub/repos/apt bookworm-pgdg main\n"
    dest: /etc/apt/sources.list.d/pgdg.list
    mode: "0644"
  register: pgdg_repo

- name: Install postgresql-common
  ansible.builtin.apt:
    name: postgresql-common
    update_cache: "{{ pgdg_repo.changed }}"

- name: Create new clusters with data checksums
  ansible.builtin.copy:
    content: "initdb_options = '--data-checksums'\n"
    dest: /etc/postgresql-common/createcluster.d/disavery.conf
    mode: "0644"

- name: Install PostgreSQL
  ansible.builtin.apt:
    name:
      - "postgresql-{{ pg_version }}"
      - "postgresql-client-{{ pg_version }}"
      - python3-psycopg2

- name: Configure PostgreSQL
  ansible.builtin.template:
    src: disavery.conf.j2
    dest: "{{ pg_conf_dir }}/conf.d/disavery.conf"
    owner: postgres
    group: postgres
    mode: "0644"
  notify: Restart PostgreSQL

- name: Allow application and replication connections
  ansible.builtin.blockinfile:
    path: "{{ pg_conf_dir }}/pg_hba.conf"
    marker: "# {mark} disavery"
    block: |
      host  {{ postgres_app_db }}  {{ postgres_app_user }}  {{ wan_subnet }}  scram-sha-256
      host  replication            replicator               {{ wan_subnet }}  scram-sha-256
  notify: Reload PostgreSQL

- name: Start PostgreSQL
  ansible.builtin.systemd_service:
    name: "{{ pg_service }}"
    state: started
    enabled: true

- name: Apply PostgreSQL changes now
  ansible.builtin.meta: flush_handlers

- name: Create the application role
  community.postgresql.postgresql_user:
    name: "{{ postgres_app_user }}"
    password: "{{ secrets.postgres.app_password }}"
  become: true
  become_user: postgres
  no_log: true

- name: Create the application database
  community.postgresql.postgresql_db:
    name: "{{ postgres_app_db }}"
    owner: "{{ postgres_app_user }}"
  become: true
  become_user: postgres

- name: Create the replication role
  community.postgresql.postgresql_user:
    name: replicator
    password: "{{ secrets.postgres.replication_password }}"
    role_attr_flags: LOGIN,REPLICATION
  become: true
  become_user: postgres
  no_log: true

- name: Enable amcheck for integrity verification
  community.postgresql.postgresql_ext:
    name: amcheck
    db: "{{ postgres_app_db }}"
  become: true
  become_user: postgres
```

- [ ] **Step 2: Write the pgbackrest role**

`infra/ansible/roles/pgbackrest/defaults/main.yml`:
```yaml
pgbackrest_repo1_path: /var/lib/pgbackrest
pgbackrest_repo1_retention_full: 7
pgbackrest_repo2_bucket: pgbackrest
pgbackrest_repo2_endpoint: vault
pgbackrest_repo2_retention_full: 14
pgbackrest_full_schedule: "*-*-* 01:00:00"
pgbackrest_incr_schedule: hourly
```

`infra/ansible/roles/pgbackrest/templates/pgbackrest.conf.j2`:
```ini
# Managed by Ansible (role pgbackrest).
[global]
repo1-path={{ pgbackrest_repo1_path }}
repo1-retention-full={{ pgbackrest_repo1_retention_full }}

repo2-type=s3
repo2-path=/repo
repo2-s3-bucket={{ pgbackrest_repo2_bucket }}
repo2-s3-endpoint={{ pgbackrest_repo2_endpoint }}
repo2-storage-port=9000
repo2-s3-region=us-east-1
repo2-s3-uri-style=path
repo2-s3-key={{ secrets.minio.vault_writer_access_key }}
repo2-s3-key-secret={{ secrets.minio.vault_writer_secret_key }}
repo2-storage-ca-file={{ disavery_ca_path }}
repo2-cipher-type=aes-256-cbc
repo2-cipher-pass={{ secrets.pgbackrest.repo2_cipher_pass }}
repo2-retention-full={{ pgbackrest_repo2_retention_full }}

archive-async=y
spool-path=/var/spool/pgbackrest
process-max=2
start-fast=y
compress-type=zst
log-level-console=info
log-level-file=detail

[{{ pgbackrest_stanza }}]
pg1-path=/var/lib/postgresql/{{ pg_version }}/main
```

`infra/ansible/roles/pgbackrest/templates/pgbackrest-backup@.service.j2`:
```ini
[Unit]
Description=pgBackRest %i backup to every repository
After=postgresql@{{ pg_version }}-main.service

[Service]
Type=oneshot
User=postgres
ExecStart=/usr/bin/pgbackrest --stanza={{ pgbackrest_stanza }} --repo=1 --type=%i backup
ExecStart=/usr/bin/pgbackrest --stanza={{ pgbackrest_stanza }} --repo=2 --type=%i backup
```

`infra/ansible/roles/pgbackrest/templates/pgbackrest-full.timer.j2`:
```ini
[Unit]
Description=Daily full pgBackRest backup

[Timer]
OnCalendar={{ pgbackrest_full_schedule }}
Persistent=true
Unit=pgbackrest-backup@full.service

[Install]
WantedBy=timers.target
```

`infra/ansible/roles/pgbackrest/templates/pgbackrest-incr.timer.j2`:
```ini
[Unit]
Description=Hourly incremental pgBackRest backup

[Timer]
OnCalendar={{ pgbackrest_incr_schedule }}
Persistent=true
Unit=pgbackrest-backup@incr.service

[Install]
WantedBy=timers.target
```

`infra/ansible/roles/pgbackrest/tasks/main.yml`:
```yaml
- name: Install pgBackRest
  ansible.builtin.apt:
    name: pgbackrest

- name: Remove the legacy configuration file
  ansible.builtin.file:
    path: /etc/pgbackrest.conf
    state: absent

- name: Create pgBackRest directories
  ansible.builtin.file:
    path: "{{ item }}"
    state: directory
    owner: postgres
    group: postgres
    mode: "0750"
  loop:
    - /etc/pgbackrest
    - "{{ pgbackrest_repo1_path }}"
    - /var/spool/pgbackrest
    - /var/log/pgbackrest

- name: Configure pgBackRest
  ansible.builtin.template:
    src: pgbackrest.conf.j2
    dest: /etc/pgbackrest/pgbackrest.conf
    owner: postgres
    group: postgres
    mode: "0600"

- name: Create the stanza (idempotent)
  ansible.builtin.command: pgbackrest --stanza={{ pgbackrest_stanza }} stanza-create
  become: true
  become_user: postgres
  changed_when: false

- name: Check archiving and both repositories
  ansible.builtin.command: pgbackrest --stanza={{ pgbackrest_stanza }} check
  become: true
  become_user: postgres
  changed_when: false

- name: Read the backup inventory
  ansible.builtin.command: pgbackrest --stanza={{ pgbackrest_stanza }} info --output=json
  become: true
  become_user: postgres
  register: pgbackrest_info
  changed_when: false

- name: Take an initial full backup in each repository
  ansible.builtin.command: pgbackrest --stanza={{ pgbackrest_stanza }} --repo={{ item }} --type=full backup
  become: true
  become_user: postgres
  loop: [1, 2]
  when: >-
    (pgbackrest_info.stdout | from_json)[0].backup
    | selectattr('database.repo-key', 'equalto', item) | list | length == 0
  changed_when: true

- name: Install backup units
  ansible.builtin.template:
    src: "{{ item }}.j2"
    dest: "/etc/systemd/system/{{ item }}"
    mode: "0644"
  loop:
    - pgbackrest-backup@.service
    - pgbackrest-full.timer
    - pgbackrest-incr.timer

- name: Enable backup timers
  ansible.builtin.systemd_service:
    name: "{{ item }}"
    state: started
    enabled: true
    daemon_reload: true
  loop:
    - pgbackrest-full.timer
    - pgbackrest-incr.timer
```

- [ ] **Step 3: Extend the playbook**

Append to `infra/ansible/playbooks/site.yml`:
```yaml

- name: Database
  hosts: "db:&site_{{ target_site | default('a') }}"
  roles:
    - postgres
    - pgbackrest
```

- [ ] **Step 4: Run and check Object Lock compatibility**

Run: `make configure`
Expected: `failed=0`; `db-a` shows the two "initial full backup" items as changed.

If the repo2 backup or `check` fails with an S3 error mentioning `Content-MD5` / `MissingContentMD5` / `InvalidRequest` (MinIO rejecting writes to a default-retention bucket without an MD5 header), apply this fallback and re-run `make configure`:

1. In `infra/ansible/roles/minio/tasks/vault.yml`, create `pgbackrest` with versioning but **without** `--with-lock`, and keep `attachments` locked:
```yaml
- name: Create Object Lock buckets
  ansible.builtin.command: mc mb --ignore-existing --with-lock local/attachments
  register: vault_mb
  changed_when: "'created successfully' in vault_mb.stdout"

- name: Create the versioned backup bucket (retention applied by the sweeper)
  ansible.builtin.command: mc mb --ignore-existing --with-versioning local/pgbackrest
  register: vault_mb_pg
  changed_when: "'created successfully' in vault_mb_pg.stdout"
```
   and limit the "Set default compliance retention" loop to `[attachments]`.
2. Add a sweeper that applies compliance retention to every version every 5 minutes (append to the same file):
```yaml
- name: Install the retention sweeper
  ansible.builtin.copy:
    dest: /etc/systemd/system/{{ item.name }}
    mode: "0644"
    content: "{{ item.content }}"
  loop:
    - name: vault-lock-sweeper.service
      content: |
        [Unit]
        Description=Apply compliance retention to new pgBackRest objects
        [Service]
        Type=oneshot
        ExecStart=/usr/local/bin/mc retention set --recursive --versions compliance {{ vault_retention_days }}d local/pgbackrest
    - name: vault-lock-sweeper.timer
      content: |
        [Unit]
        Description=Run the retention sweeper every 5 minutes
        [Timer]
        OnBootSec=1min
        OnUnitActiveSec=5min
        [Install]
        WantedBy=timers.target
  loop_control:
    label: "{{ item.name }}"

- name: Enable the retention sweeper
  ansible.builtin.systemd_service:
    name: vault-lock-sweeper.timer
    state: started
    enabled: true
    daemon_reload: true
```
   The sweeper runs as root on the vault, whose `mc` alias `local` uses the vault root credentials configured in `tasks/main.yml` (the alias lives in `/root/.mc`).
3. Add a line to ADR 0003's Consequences: "pgBackRest objects are unprotected for up to 5 minutes until the retention sweeper locks them."

Because the vault buckets are recreated, run `make down infra configure` after changing them (a non-locked bucket cannot be converted in place).

- [ ] **Step 5: Verify**

```bash
docker exec db-a runuser -u postgres -- pgbackrest --stanza=main info
docker exec db-a runuser -u postgres -- psql -tAc "SHOW data_checksums; SHOW archive_mode; SHOW archive_timeout;"
docker exec db-a systemctl list-timers 'pgbackrest-*' --no-pager
docker exec db-a runuser -u postgres -- psql -d docsvc -tAc "SELECT extname FROM pg_extension WHERE extname='amcheck'"
make configure   # second run
```
Expected: `info` shows `status: ok` and one `full backup` under `repo1` and one under `repo2` (`repo2` listed as encrypted `aes-256-cbc`); `on`, `on`, `30s`; two timers listed; `amcheck`; second run `failed=0 changed=0` on `db-a`.

- [ ] **Step 6: Commit**

```bash
git add infra/ansible docs/adr
git commit -m "feat: add PostgreSQL and pgBackRest roles with two repositories"
```

---

### Task 14: docsvc and webhook mock deployment

**Files:**
- Create: `infra/ansible/roles/docsvc/{tasks/main.yml,handlers/main.yml,templates/docsvc.env.j2,templates/docsvc.service.j2}`
- Create: `infra/ansible/roles/webhookmock/{tasks/main.yml,handlers/main.yml,templates/webhookmock.env.j2,templates/webhookmock.service.j2}`
- Modify: `infra/ansible/playbooks/site.yml`, `Makefile` (append `build` target)

**Interfaces:**
- Consumes: binaries `build/docsvc`, `build/webhookmock` (Tasks 6–7, built for linux/amd64 in the toolbox); DB, MinIO and inventory from Tasks 10–13.
- Produces: `docsvc.service` on `app-<site>` listening on `:8080`; `webhookmock.service` on `webhook` listening on `:8081` that only accepts the active site's app node (`hostvars['app-' + active_site].wan_ip/32`); `https://docs.disavery.test` served through the edge. Make target `build`.

- [ ] **Step 1: Append the build target**

```make

.PHONY: build
build: toolbox ## Build Linux binaries into build/
	$(TB) bash -c 'CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o build/ ./cmd/docsvc ./cmd/webhookmock'
```

- [ ] **Step 2: Write the docsvc role**

`infra/ansible/roles/docsvc/templates/docsvc.env.j2`:
```
DOCSVC_LISTEN=:8080
DOCSVC_DATABASE_URL=postgres://docsvc:{{ secrets.postgres.app_password | urlencode }}@db-{{ target_site | default('a') }}:5432/docsvc?sslmode=disable
DOCSVC_S3_ENDPOINT=obj-{{ target_site | default('a') }}:9000
DOCSVC_S3_ACCESS_KEY={{ secrets.minio.app_access_key }}
DOCSVC_S3_SECRET_KEY={{ secrets.minio.app_secret_key }}
DOCSVC_S3_BUCKET=attachments
DOCSVC_PAYMENT_WEBHOOK_URL=http://webhook:8081/payments
DOCSVC_PUBLIC_BASE_URL=https://docs.disavery.test
DOCSVC_WEBHOOK_SECRET={{ secrets.webhook.hmac_secret }}
```

`infra/ansible/roles/docsvc/templates/docsvc.service.j2`:
```ini
[Unit]
Description=disavery document service
Wants=network-online.target
After=network-online.target

[Service]
User=docsvc
EnvironmentFile=/etc/docsvc.env
ExecStart=/usr/local/bin/docsvc
Restart=always
RestartSec=2

[Install]
WantedBy=multi-user.target
```

`infra/ansible/roles/docsvc/handlers/main.yml`:
```yaml
- name: Restart docsvc
  ansible.builtin.systemd_service:
    name: docsvc
    state: restarted
    daemon_reload: true
```

`infra/ansible/roles/docsvc/tasks/main.yml`:
```yaml
- name: Create the docsvc user
  ansible.builtin.user:
    name: docsvc
    system: true
    shell: /usr/sbin/nologin
    create_home: false

- name: Install the docsvc binary
  ansible.builtin.copy:
    src: /work/build/docsvc
    dest: /usr/local/bin/docsvc
    mode: "0755"
  notify: Restart docsvc

- name: Write the docsvc environment
  ansible.builtin.template:
    src: docsvc.env.j2
    dest: /etc/docsvc.env
    mode: "0600"
  notify: Restart docsvc

- name: Install the docsvc unit
  ansible.builtin.template:
    src: docsvc.service.j2
    dest: /etc/systemd/system/docsvc.service
    mode: "0644"
  notify: Restart docsvc

- name: Start docsvc
  ansible.builtin.systemd_service:
    name: docsvc
    state: started
    enabled: true
    daemon_reload: true

- name: Apply docsvc restarts now
  ansible.builtin.meta: flush_handlers

- name: Wait for docsvc readiness
  ansible.builtin.uri:
    url: http://127.0.0.1:8080/readyz
  register: docsvc_ready
  until: docsvc_ready.status == 200
  retries: 30
  delay: 2
```

- [ ] **Step 3: Write the webhookmock role**

`infra/ansible/roles/webhookmock/templates/webhookmock.env.j2`:
```
WEBHOOKMOCK_LISTEN=:8081
WEBHOOKMOCK_ALLOW_CIDRS={{ hostvars['app-' + active_site].wan_ip }}/32
WEBHOOKMOCK_SECRET={{ secrets.webhook.hmac_secret }}
WEBHOOKMOCK_DELAY=1s
```

`infra/ansible/roles/webhookmock/templates/webhookmock.service.j2`:
```ini
[Unit]
Description=Fake payment provider
Wants=network-online.target
After=network-online.target

[Service]
DynamicUser=yes
EnvironmentFile=/etc/webhookmock.env
ExecStart=/usr/local/bin/webhookmock
Restart=always
RestartSec=2

[Install]
WantedBy=multi-user.target
```

`infra/ansible/roles/webhookmock/handlers/main.yml`:
```yaml
- name: Restart webhookmock
  ansible.builtin.systemd_service:
    name: webhookmock
    state: restarted
    daemon_reload: true
```

`infra/ansible/roles/webhookmock/tasks/main.yml`:
```yaml
- name: Install the webhookmock binary
  ansible.builtin.copy:
    src: /work/build/webhookmock
    dest: /usr/local/bin/webhookmock
    mode: "0755"
  notify: Restart webhookmock

- name: Write the webhookmock environment
  ansible.builtin.template:
    src: webhookmock.env.j2
    dest: /etc/webhookmock.env
    mode: "0600"
  notify: Restart webhookmock

- name: Install the webhookmock unit
  ansible.builtin.template:
    src: webhookmock.service.j2
    dest: /etc/systemd/system/webhookmock.service
    mode: "0644"
  notify: Restart webhookmock

- name: Start webhookmock
  ansible.builtin.systemd_service:
    name: webhookmock
    state: started
    enabled: true
    daemon_reload: true

- name: Apply webhookmock restarts now
  ansible.builtin.meta: flush_handlers

- name: Wait for webhookmock
  ansible.builtin.uri:
    url: http://127.0.0.1:8081/healthz
  register: webhookmock_ready
  until: webhookmock_ready.status == 200
  retries: 15
  delay: 1
```

- [ ] **Step 4: Extend the playbook**

Append to `infra/ansible/playbooks/site.yml`:
```yaml

- name: Payment provider mock
  hosts: webhooks
  roles:
    - webhookmock

- name: Application
  hosts: "app:&site_{{ target_site | default('a') }}"
  roles:
    - docsvc
```

- [ ] **Step 5: Verify through the edge**

```bash
make build configure
docker compose exec -T toolbox bash -c '
  sops -d --extract "[\"tls\"][\"ca_crt\"]" /secrets/local.sops.yaml > /tmp/ca.crt
  curl -fsS --cacert /tmp/ca.crt --resolve docs.disavery.test:443:172.31.0.11 https://docs.disavery.test/readyz'
```
Expected: `{"checks":{"attachments":"ok","database":"ok"},"status":"OK"}`.

- [ ] **Step 6: Commit**

```bash
git add infra/ansible Makefile
git commit -m "feat: deploy docsvc and the webhook mock"
```

---

### Task 15: Smoke test, `make up`, README and PR CI

**Files:**
- Create: `scripts/smoke.sh`
- Create: `.github/workflows/ci.yml`
- Modify: `Makefile` (append targets), `README.md`

**Interfaces:**
- Consumes: everything above.
- Produces: `make up` (images → secrets → build → infra → configure), `make smoke`, `make destroy`; CI workflow `ci` on pull requests and pushes to `main`.

- [ ] **Step 1: Write the smoke test**

`scripts/smoke.sh`:
```bash
#!/usr/bin/env bash
# End-to-end smoke test of the local lab. Runs inside the toolbox.
set -euo pipefail

SECRETS=/secrets/local.sops.yaml
DNS_SERVER=172.31.0.10
HOST=docs.disavery.test
BASE="https://$HOST"
SSH=(ssh -i /secrets/ssh/id_ed25519 -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR)

step() { printf '\n==> %s\n' "$*"; }
fail() { printf 'SMOKE FAIL: %s\n' "$*" >&2; exit 1; }
secret() { sops -d --extract "$1" "$SECRETS"; }

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
secret '["tls"]["ca_crt"]' > "$work/ca.crt"
mkdir -p ~/.mc/certs/CAs && cp "$work/ca.crt" ~/.mc/certs/CAs/disavery-ca.crt

step "DNS resolves $HOST through CoreDNS"
edge_ip=$(dig +short @"$DNS_SERVER" "$HOST")
[[ -n $edge_ip ]] || fail "$HOST did not resolve"
CURL=(curl -fsS --cacert "$work/ca.crt" --resolve "$HOST:443:$edge_ip")

step "application is ready behind the edge (TLS verified)"
"${CURL[@]}" "$BASE/readyz" >/dev/null || fail "readyz failed"

step "upload and download a document"
echo "smoke $(date -u +%FT%TZ)" > "$work/doc.txt"
id=$("${CURL[@]}" -F title=smoke -F "file=@$work/doc.txt" "$BASE/documents" | jq -r .id)
[[ $id =~ ^[0-9a-f-]{36}$ ]] || fail "unexpected document id: $id"
"${CURL[@]}" "$BASE/documents/$id/attachment" | cmp - "$work/doc.txt" || fail "attachment mismatch"

step "payment webhook round trip (allowlist, DNS, TLS, HMAC)"
status=pending
for _ in $(seq 1 20); do
  status=$("${CURL[@]}" "$BASE/documents/$id" | jq -r .payment_status)
  [[ $status == paid ]] && break
  sleep 1
done
[[ $status == paid ]] || fail "payment status stayed '$status'"

step "WAL archiving reaches both repositories"
"${SSH[@]}" root@db-a runuser -u postgres -- pgbackrest --stanza=main check >/dev/null || fail "pgbackrest check failed"

step "a backup exists in both repositories"
repos=$("${SSH[@]}" root@db-a runuser -u postgres -- pgbackrest --stanza=main info --output=json \
  | jq '[.[0].backup[].database["repo-key"]] | unique | length')
[[ $repos == 2 ]] || fail "expected backups in 2 repositories, found $repos"

step "attachment replicated to the vault"
mc alias set vaultw https://vault:9000 "$(secret '["minio"]["vault_writer_access_key"]')" "$(secret '["minio"]["vault_writer_secret_key"]')" >/dev/null
mc alias set vaultroot https://vault:9000 "$(secret '["minio"]["vault_root_user"]')" "$(secret '["minio"]["vault_root_password"]')" >/dev/null
for _ in $(seq 1 30); do
  mc stat "vaultw/attachments/documents/$id" >/dev/null 2>&1 && break
  sleep 1
done
version=$(mc stat --json "vaultw/attachments/documents/$id" | jq -r .versionID)
[[ -n $version && $version != null ]] || fail "attachment was not replicated to the vault"

step "production credentials cannot destroy vault versions"
if mc rm --version-id "$version" "vaultw/attachments/documents/$id" >/dev/null 2>&1; then
  fail "production writer deleted a vault version"
fi

step "even the vault root cannot delete a locked version"
if mc rm --version-id "$version" "vaultroot/attachments/documents/$id" >/dev/null 2>&1; then
  fail "a locked version was deleted"
fi

printf '\nSMOKE PASS\n'
```

- [ ] **Step 2: Append Make targets**

```make

.PHONY: up
up: images secrets build infra configure ## Bring the whole lab up (idempotent)

.PHONY: smoke
smoke: ## Run the end-to-end smoke test
	$(TB) bash scripts/smoke.sh

.PHONY: destroy
destroy: down ## Remove everything, including secrets, escrow and the toolbox
	docker compose down -v
```

- [ ] **Step 3: Run from scratch**

```bash
make destroy || true
time make up
make smoke
```
Expected: `make up` completes with `failed=0` for all hosts; `make smoke` ends with `SMOKE PASS`. Record the `make up` wall time for the README.

- [ ] **Step 4: Verify idempotency (Review Focus 4)**

```bash
make up 2>&1 | tee /tmp/up2.log
grep -E 'No changes\.' /tmp/up2.log
grep -E '^[a-z0-9-]+ +: ok=' /tmp/up2.log
```
Expected: Terraform prints `No changes.`; every PLAY RECAP line shows `changed=0 unreachable=0 failed=0`. Any task that reports `changed` on the second run is a bug in that role: fix its `changed_when`/idempotency before continuing.

- [ ] **Step 5: Write the PR CI workflow**

`.github/workflows/ci.yml`:
```yaml
name: ci

on:
  pull_request:
  push:
    branches: [main]

jobs:
  go:
    runs-on: ubuntu-24.04
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - name: Line endings are LF
        run: test -z "$(git ls-files --eol | grep -v 'i/lf' | grep -v 'i/-text')"
      - run: go vet ./...
      - run: go test -race ./...
      - uses: golangci/golangci-lint-action@v8
        with:
          version: latest

  infra:
    runs-on: ubuntu-24.04
    steps:
      - uses: actions/checkout@v4
      - uses: hashicorp/setup-terraform@v3
        with:
          terraform_version: 1.9.8
      - run: terraform fmt -check -recursive infra/terraform
      - run: terraform -chdir=infra/terraform/envs/local init -backend=false -input=false
      - run: terraform -chdir=infra/terraform/envs/local validate
      - name: ansible-lint
        working-directory: infra/ansible
        env:
          ANSIBLE_ROLES_PATH: ${{ github.workspace }}/infra/ansible/roles
        run: |
          pip install -r ../../images/toolbox/requirements.txt
          ansible-galaxy collection install -r ../../images/toolbox/requirements.yml
          ansible-lint playbooks roles
```

- [ ] **Step 6: Write the README**

`README.md`:
````markdown
# disavery

Disaster recovery architecture for PostgreSQL that proves recovery instead of
assuming it: automated backups to two repositories (one immutable), measured
RPO/RTO drills, pilot light vs. warm standby, failover and failback.

> Status: milestone 1 of 5 — the lab environment, the protected application and
> backups. Drills arrive in milestone 2. Design: [spec](docs/superpowers/specs/2026-10-07-disavery-dr-design.md).

## What runs

| Zone | Nodes | Purpose |
|---|---|---|
| global | `dns` (CoreDNS), `edge` (Caddy), `webhook` | External DNS, edge/CDN and a fake payment provider |
| site-a | `app-a` (docsvc), `db-a` (PostgreSQL 16 + pgBackRest), `obj-a` (MinIO) | Production |
| vault | `vault` (MinIO, Object Lock) | Immutable backup copy in a "separate account" |

Every node is a systemd container created by Terraform and configured by Ansible
([ADR 0001](docs/adr/0001-systemd-containers-as-nodes.md)).

## Quick start

Requirements: Docker (Docker Desktop with WSL2 on Windows), GNU make, Go 1.26 for local tests.

```bash
make up      # build images, generate secrets, create and configure the lab
make smoke   # end-to-end check: TLS, upload, webhook, WAL archive, backups, immutability
make down    # remove the lab containers (keeps secrets)
make destroy # remove everything, including secrets and the escrow volume
```

`make up` is idempotent; running it again changes nothing.

Secrets are generated once into the `disavery-secrets` volume, encrypted with
SOPS/age; the age key is escrowed to the separate `disavery-escrow` volume.

## Development

```bash
make test-short  # unit tests
make test        # unit + integration tests (needs Docker)
make lint
```
````

- [ ] **Step 7: Final verification and commit**

```bash
go test ./... && make smoke
git add scripts/smoke.sh .github Makefile README.md
git commit -m "feat: add smoke test, make up, README and PR CI"
git push -u origin feat/m1-environment
```
Expected: tests PASS, `SMOKE PASS`. Open a PR from `feat/m1-environment` to `main`; the `ci` workflow must be green before merging.
