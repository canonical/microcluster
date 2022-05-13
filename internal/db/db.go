package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/lxc/lxd/lxd/db/query"

	"github.com/canonical/microcluster/internal/db/cluster"
	"github.com/canonical/microcluster/internal/logger"
)

// Tx is a convenience so we don't have to import sql.Tx everywhere.
type Tx = sql.Tx

// Open opens the dqlite database and loads the schema.
func (db *DB) Open(bootstrap bool) error {
	ctx, cancel := context.WithTimeout(db.ctx, 10*time.Second)
	defer cancel()

	err := db.dqlite.Ready(ctx)
	if err != nil {
		return err
	}

	db.db, err = db.dqlite.Open(db.ctx, db.dbName)
	if err != nil {
		return err
	}

	if bootstrap {
		_, err = db.db.Exec(cluster.CreateSchema)
		if err != nil {
			return fmt.Errorf("Failed to bootstrap schema: %w", err)
		}
	}

	err = cluster.PrepareStmts(db.db, false)
	if err != nil {
		return err
	}

	db.openCanceller.Cancel()

	return nil
}

// Transaction handles performing a transaction on the dqlite database.
func (db *DB) Transaction(ctx context.Context, f func(context.Context, *Tx) error) error {
	return db.retry(func() error {
		err := query.Transaction(ctx, db.db, f)
		if errors.Is(err, context.DeadlineExceeded) {
			// If the query timed out it likely means that the leader has abruptly become unreachable.
			// Now that this query has been cancelled, a leader election should have taken place by now.
			// So let's retry the transaction once more in case the global database is now available again.
			logger.Warn("Transaction timed out. Retrying once", logger.Ctx{"err": err})
			return query.Transaction(ctx, db.db, f)
		}

		return err
	})
}

func (db *DB) retry(f func() error) error {
	if db.ctx.Err() != nil {
		return f()
	}

	return query.Retry(f)
}
