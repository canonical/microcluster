// Package query transaction functions that match LXD's transaction handling patterns.
package query

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/canonical/microcluster/v3/internal/log"
)

// Transaction executes the given function within a database transaction with a 10s context timeout.
// This implementation matches LXD's Transaction function.
func Transaction(ctx context.Context, db *sql.DB, f func(context.Context, *sql.Tx) error) error {
	ctx, cancel := context.WithTimeout(ctx, time.Second*10)
	defer cancel()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		// If there is a leftover transaction let's try to rollback,
		// we'll then retry again.
		if strings.Contains(err.Error(), "cannot start a transaction within a transaction") {
			_, _ = db.Exec("ROLLBACK")
		}

		return fmt.Errorf("Failed to begin transaction: %w", err)
	}

	err = f(ctx, tx)
	if err != nil {
		return rollback(ctx, tx, err)
	}

	err = tx.Commit()
	if err == sql.ErrTxDone {
		err = nil // Ignore duplicate commits/rollbacks
	}

	return err
}

// Rollback a transaction after the given error occurred. If the rollback
// succeeds the given error is returned, otherwise a new error that wraps it
// gets generated and returned.
// This implementation matches LXD's rollback function.
func rollback(ctx context.Context, tx *sql.Tx, reason error) error {
	err := Retry(ctx, func(_ context.Context) error { return tx.Rollback() })
	if err != nil {
		logger, logErr := log.LoggerFromContext(ctx)
		if logErr != nil {
			return logErr
		}

		logger.Warn("Failed to rollback transaction after error", slog.String("reason", reason.Error()), slog.String("error", err.Error()))
	}

	return reason
}
