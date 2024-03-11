package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"time"

	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/lxd/db/schema"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/logger"

	"github.com/canonical/microcluster/cluster"
	"github.com/canonical/microcluster/internal/extensions"
	"github.com/canonical/microcluster/internal/sys"
)

// Open opens the dqlite database and loads the schema.
// Returns true if we need to wait for other nodes to catch up to our version.
func (db *DB) Open(ext *extensions.Extensions, bootstrap bool, project string) error {
	ctx, cancel := context.WithTimeout(db.ctx, 30*time.Second)
	defer cancel()

	err := db.dqlite.Ready(ctx)
	if err != nil {
		return err
	}

	db.db, err = db.dqlite.Open(db.ctx, db.dbName)
	if err != nil {
		return err
	}

	otherNodesBehind := false
	newSchema := db.Schema()
	if !bootstrap {
		checkVersions := func(ctx context.Context, current int, tx *sql.Tx) error {
			// Override the second update to ensure that the API extensions are available.
			err := newSchema.OverrideUpdate(ctx, tx, 2)
			if err != nil {
				return err
			}

			schemaVersion := newSchema.Version()
			info := cluster.InternalClusterMemberVersioningInfo{
				SchemaVersion: schemaVersion,
				Extensions:    ext,
			}

			err = cluster.UpdateClusterMemberSchemaVersionAndAPIExtensions(tx, info, db.listenAddr.URL.Host)
			if err != nil {
				return fmt.Errorf("Failed to update schema version when joining/upgrading cluster: %w", err)
			}

			versioningInfo, err := cluster.GetClusterMemberSchemaVersionsAndAPIExtensions(ctx, tx)
			if err != nil {
				return fmt.Errorf("Failed to get other members' schema versions: %w", err)
			}

			for _, versionInfo := range versioningInfo {
				isSameVersion := ext.IsSameVersion(versionInfo.Extensions) == nil
				if isSameVersion {
					// All the API extensions are equal, now check the schema version
					if schemaVersion == versionInfo.SchemaVersion {
						// Versions and API extensions are equal, there's hope for the
						// update. Let's check the next node.
						continue
					}

					if schemaVersion > versionInfo.SchemaVersion {
						// Our version is bigger, we should stop here
						// and wait for other nodes to be upgraded and
						// restarted.
						otherNodesBehind = true
						return schema.ErrGracefulAbort
					}

					// Another node has a version greater than ours
					// and presumably is waiting for other nodes
					// to upgrade. Let's error out and shutdown
					// since we need a greater version.
					return fmt.Errorf("Cluster expects Schema version (%d) but this system has (%d), please upgrade", versionInfo.SchemaVersion, schemaVersion)
				}

				if versionInfo.Extensions != nil {
					if ext.Version() <= versionInfo.Extensions.Version() {
						// Another node has an API extension set bigger than ours
						// and presumably is waiting for other nodes
						// to upgrade. Let's error out and shutdown
						// since we need a greater version.
						return fmt.Errorf("Cluster expects API version (%d) but this system has (%d), please upgrade", versionInfo.Extensions.Version(), ext.Version())
					}
				}

				// Our version is bigger or the API extension system does not exist on the other node, we should stop here
				// and wait for other nodes to be upgraded and
				// restarted.
				otherNodesBehind = true
				return schema.ErrGracefulAbort
			}

			return nil
		}

		newSchema.Check(checkVersions)
	}

	err = db.retry(func() error {
		_, err = newSchema.Ensure(db.db)
		return err
	})

	// Check if other nodes are behind before checking the error.
	if otherNodesBehind {
		// If we are not bootstrapping, wait for an upgrade notification, or wait a minute before checking again.
		if !bootstrap {
			logger.Warn("Waiting for other cluster members to upgrade their versions", logger.Ctx{"address": db.listenAddr.String()})
			select {
			case <-db.upgradeCh:
			case <-time.After(time.Minute):
			}
		}

		return err
	}

	if err != nil {
		return err
	}

	err = cluster.PrepareStmts(db.db, project, false)
	if err != nil {
		return err
	}

	db.openCanceller.Cancel()

	return nil
}

// Transaction handles performing a transaction on the dqlite database.
func (db *DB) Transaction(ctx context.Context, f func(context.Context, *sql.Tx) error) error {
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

// Update attempts to update the database with the executable at the path specified by the SCHEMA_UPDATE variable.
func (db *DB) Update() error {
	if !db.IsOpen() {
		return fmt.Errorf("Failed to update, database is not yet open")
	}

	updateExec := os.Getenv(sys.SchemaUpdate)
	if updateExec == "" {
		logger.Warn("No SCHEMA_UPDATE variable set, skipping auto-update")
		return nil
	}

	// Wait a random amount of seconds (up to 30) to space out the update.
	wait := time.Duration(rand.Intn(30)) * time.Second
	logger.Info("Triggering cluster auto-update soon", logger.Ctx{"wait": wait, "updateExecutable": updateExec})
	time.Sleep(wait)

	logger.Info("Triggering cluster auto-update now")
	_, err := shared.RunCommand(updateExec)
	if err != nil {
		logger.Error("Triggering cluster update failed", logger.Ctx{"err": err})
		return err
	}

	logger.Info("Triggering cluster auto-update succeeded")

	return nil
}
