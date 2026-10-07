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
