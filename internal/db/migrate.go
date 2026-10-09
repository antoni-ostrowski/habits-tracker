package schema

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// schemaSQL is the DDL source of truth, shared with sqlc codegen and
// sqldef planning. Embedded so the binary carries it anywhere.

//go:embed schema.sql
var schemaSQL string

// Migrate creates missing tables/indexes on boot. Create-if-missing
// only: it never alters or drops, so existing data is untouched and
// the image can boot against an empty database with no repo files
// around. Real schema changes go through `mise run db-apply` (sqldef),
// which diffs this same file declaratively.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, schemaSQL); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	return nil
}
