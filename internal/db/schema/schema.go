package schema

import (
	"context"
	"database/sql"
)

// Update represents a database schema update function.
// It takes a context and transaction and applies the schema changes.
type Update func(ctx context.Context, tx *sql.Tx) error

// Hook is a callback that gets fired when a update gets applied.
type Hook func(ctx context.Context, version int, tx *sql.Tx) error

// Check is a function that gets executed before applying schema updates.
// It can be used to validate preconditions or abort the update process.
// The current parameter indicates the current schema version.
type Check func(ctx context.Context, current int, tx *sql.Tx) error
