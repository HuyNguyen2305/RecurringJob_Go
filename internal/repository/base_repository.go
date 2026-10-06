// Package repository is the data-access layer.
package repository

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"

	"recurringjob/internal/common/apperror"
	"recurringjob/internal/common/auth"
	"recurringjob/internal/common/civil"
)

type txKey struct{}

var schemaName = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// BaseRepository gives repositories tenant-scoped, transactional DB access.
type BaseRepository struct {
	db *gorm.DB
}

func NewBaseRepository(db *gorm.DB) BaseRepository {
	return BaseRepository{db: db}
}

// Transaction runs fn in a transaction whose search_path is the request's
// tenant schema. A transaction already in ctx is reused, so repository calls
// made inside fn share it (and roll back together).
func (b BaseRepository) Transaction(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := ctx.Value(txKey{}).(*gorm.DB); ok {
		return fn(ctx)
	}
	return b.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if schema := auth.TenantSchemaFromContext(ctx); schema != "" {
			if !schemaName.MatchString(schema) {
				return apperror.Validation("invalid tenant schema")
			}
			if err := tx.Exec(`SELECT set_config('search_path', ?, true)`, `"`+schema+`", public`).Error; err != nil {
				return err
			}
		}
		return fn(context.WithValue(ctx, txKey{}, tx))
	})
}

// WithSchema runs fn with a *gorm.DB scoped to the request's tenant schema.
func (b BaseRepository) WithSchema(ctx context.Context, fn func(tx *gorm.DB) error) error {
	return b.Transaction(ctx, func(ctx context.Context) error {
		return fn(ctx.Value(txKey{}).(*gorm.DB))
	})
}

// LockOccurrence takes a transaction-scoped advisory lock for one occurrence
// (job + date) of the request's tenant, waiting while another transaction
// holds it; it is released when the surrounding transaction ends. Locks are
// database-wide, so the schema is part of the key. Call it inside Transaction.
func (b BaseRepository) LockOccurrence(ctx context.Context, jobID string, date time.Time) error {
	return b.WithSchema(ctx, func(tx *gorm.DB) error {
		return tx.Exec(`SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || ':' || ? || ':' || ?, 0))`,
			jobID, civil.Format(date)).Error
	})
}

// isUniqueViolation reports a Postgres unique_violation (23505).
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// isForeignKeyViolationOn reports a foreign-key violation (23503, or 23001
// for a restrict) raised by a constraint whose name contains part, such as
// "job_id".
func isForeignKeyViolationOn(err error, part string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && (pgErr.Code == "23503" || pgErr.Code == "23001") && strings.Contains(pgErr.ConstraintName, part)
}
