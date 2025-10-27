// Package query object functions that match LXD's object handling patterns.
package query

import (
	"context"
	"database/sql"
)

// Dest is a function that is expected to return the objects to pass to the
// 'dest' argument of sql.Rows.Scan(). It is invoked by SelectObjects once per
// yielded row, and it will be passed the index of the row being scanned.
type Dest func(scan func(dest ...any) error) error

// SelectObjects executes a statement which must yield rows with a specific
// columns schema. It invokes the given Dest hook for each yielded row.
// This implementation matches LXD's SelectObjects function.
func SelectObjects(ctx context.Context, stmt *sql.Stmt, rowFunc Dest, args ...any) error {
	rows, err := stmt.QueryContext(ctx, args...)
	if err != nil {
		return err
	}

	defer func() { _ = rows.Close() }()

	for rows.Next() {
		err = rowFunc(rows.Scan)
		if err != nil {
			return err
		}
	}

	return rows.Err()
}

// Scan runs a query with inArgs and provides the rowFunc with the scan function for each row.
// It handles closing the rows and errors from the result set.
// This implementation matches LXD's Scan function.
func Scan(ctx context.Context, tx *sql.Tx, sql string, rowFunc Dest, inArgs ...any) error {
	rows, err := tx.QueryContext(ctx, sql, inArgs...)
	if err != nil {
		return err
	}

	defer func() { _ = rows.Close() }()

	for rows.Next() {
		err = rowFunc(rows.Scan)
		if err != nil {
			return err
		}
	}

	return rows.Err()
}
