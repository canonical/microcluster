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
func (db *DB) Open(ext extensions.Extensions, bootstrap bool, project string) error {
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

	err = db.waitUpgrade(bootstrap, ext)
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

// waitUpgradeAPI compares the version information of all cluster members in the database to the local version.
// If this node's version is ahead of others, then it will block on the `db.apiUpgradeCh` or up to 30s.
// If this node's version is behind others, then it returns an error.
func (db *DB) waitUpgradeAPI(ext extensions.Extensions) error {

	checkAPIExtensions := func(currentAPIExtensions extensions.Extensions, clusterMemberAPIExtensions []extensions.Extensions) (otherNodesBehind bool, err error) {
		logger.Warnf("Local API extensions: %v, cluster members API extensions: %v", currentAPIExtensions, clusterMemberAPIExtensions)

		nodeIsBehind := false
		for _, extensions := range clusterMemberAPIExtensions {
			if currentAPIExtensions.IsSameVersion(extensions) == nil {
				// API extensions are equal, there's hope for the
				// update. Let's check the next node.
				continue
			} else if extensions == nil || currentAPIExtensions.Version() > extensions.Version() {
				// Our version is bigger, we should stop here
				// and wait for other nodes to be upgraded and
				// restarted.
				nodeIsBehind = true
				continue
			} else {
				// Another node has a version greater than ours
				// and presumeably is waiting for other nodes
				// to upgrade. Let's error out and shutdown
				// since we need a greater version.
				return false, fmt.Errorf("This node's API extensions are behind, please upgrade")
			}
		}

		return nodeIsBehind, nil
	}

	otherNodesBehind := false
	err := db.retry(context.TODO(), func(_ context.Context) error {
		otherNodesBehindAPI := false
		// Perform the API extensions check.
		err := query.Transaction(context.TODO(), db.db, func(ctx context.Context, tx *sql.Tx) error {
			err := cluster.UpdateClusterMemberAPIExtensions(tx, ext, db.listenAddr.URL.Host)
			if err != nil {
				return fmt.Errorf("Failed to update API extensions when joining cluster: %w", err)
			}

			clusterMembersAPIExtensions, err := cluster.GetClusterMemberAPIExtensions(ctx, tx)
			if err != nil {
				return fmt.Errorf("Failed to get other members' API extensions: %w", err)
			}

			otherNodesBehindAPI, err = checkAPIExtensions(ext, clusterMembersAPIExtensions)
			if err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			return err
		}

		if otherNodesBehindAPI {
			otherNodesBehind = true
			return schema.ErrGracefulAbort
		}

		return nil
	})

	// If we are not bootstrapping, wait for an upgrade notification, or wait a minute before checking again.
	if otherNodesBehind {
		logger.Warn("Waiting for other cluster members to upgrade their versions", logger.Ctx{"address": db.listenAddr.String()})
		select {
		case <-db.apiUpgradeCh:
		case <-time.After(30 * time.Second):
		}
	}

	return err
}

// waitUpgradeSchema compares the version information of all cluster members in the database to the local version.
// If this node's version is ahead of others, then it will block on the `db.schemaUpgradeCh` or up to 30s.
// If this node's version is behind others, then it returns an error.
func (db *DB) waitUpgradeSchema(bootstrap bool) error {
	checkSchemaVersion := func(schemaVersion uint64, clusterMemberVersions []uint64) (otherNodesBehind bool, err error) {
		nodeIsBehind := false
		for _, version := range clusterMemberVersions {
			if schemaVersion == version {
				// Versions are equal, there's hope for the
				// update. Let's check the next node.
				continue
			}

			if schemaVersion > version {
				// Our version is bigger, we should stop here
				// and wait for other nodes to be upgraded and
				// restarted.
				nodeIsBehind = true
				continue
			}

			// Another node has a version greater than ours
			// and presumeably is waiting for other nodes
			// to upgrade. Let's error out and shutdown
			// since we need a greater version.
			return false, fmt.Errorf("This node's version is behind, please upgrade")
		}

		return nodeIsBehind, nil
	}

	otherNodesBehind := false
	newSchema := db.Schema()
	if !bootstrap {
		checkVersions := func(ctx context.Context, current int, tx *sql.Tx) error {
			schemaVersionInternal, schemaVersionExternal := newSchema.Version()
			err := cluster.UpdateClusterMemberSchemaVersion(tx, schemaVersionInternal, schemaVersionExternal, db.listenAddr.URL.Host)
			if err != nil {
				return fmt.Errorf("Failed to update schema version when joining cluster: %w", err)
			}

			versionsInternal, versionsExternal, err := cluster.GetClusterMemberSchemaVersions(ctx, tx)
			if err != nil {
				return fmt.Errorf("Failed to get other members' schema versions: %w", err)
			}

			otherNodesBehindInternal, err := checkSchemaVersion(schemaVersionInternal, versionsInternal)
			if err != nil {
				return err
			}

			otherNodesBehindExternal, err := checkSchemaVersion(schemaVersionExternal, versionsExternal)
			if err != nil {
				return err
			}

			// Wait until after considering both internal and external schema versions to determine if we should wait for other nodes.
			// This is to prevent nodes accidentally waiting for each other in case of an awkward upgrade.
			if otherNodesBehindInternal || otherNodesBehindExternal {
				otherNodesBehind = true

				return schema.ErrGracefulAbort
			}

			return nil
		}

		newSchema.Check(checkVersions)
	}

	err := db.retry(context.TODO(), func(_ context.Context) error {
		_, err := newSchema.Ensure(db.db)

		return err
	})

	// If we are not bootstrapping, wait for an upgrade notification, or wait a minute before checking again.
	if otherNodesBehind && !bootstrap {
		logger.Warn("Waiting for other cluster members to upgrade their versions", logger.Ctx{"address": db.listenAddr.String()})
		select {
		case <-db.schemaUpgradeCh:
		case <-time.After(30 * time.Second):
		}
	}

	return err
}

// Transaction handles performing a transaction on the dqlite database.
func (db *DB) Transaction(outerCtx context.Context, f func(context.Context, *sql.Tx) error) error {
	return db.retry(outerCtx, func(ctx context.Context) error {
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

func (db *DB) retry(ctx context.Context, f func(context.Context) error) error {
	if db.ctx.Err() != nil {
		return f(ctx)
	}

	return query.Retry(ctx, f)
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
