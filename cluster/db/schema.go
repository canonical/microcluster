package db

import (
	"context"
	"database/sql"
)

// Update represents a database schema update function.
// It takes a context and transaction and applies the schema changes.
type Update func(ctx context.Context, tx *sql.Tx) error
