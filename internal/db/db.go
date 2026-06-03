package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"os"
	"path"
	"time"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/revert"

	"github.com/canonical/microcluster/v4/internal/db/query"
	"github.com/canonical/microcluster/v4/internal/db/update"
	"github.com/canonical/microcluster/v4/internal/sys"
	clusterDB "github.com/canonical/microcluster/v4/microcluster/db"
	"github.com/canonical/microcluster/v4/microcluster/types"
)

// Open opens the dqlite database and loads the schema.
// Returns true if we need to wait for other nodes to catch up to our version.
func (db *DqliteDB) Open(ext types.Extensions, bootstrap bool) error {
	// Allow dqlite up to 2 minutes to become ready when starting up.
	// This is to allow for unready/dead nodes to time out. The timeout can be
	// raised via the DQLITE_READY_TIMEOUT environment variable, which helps when
	// a joining node must sync a large dqlite database over a slow link or disk
	// and would otherwise exceed the default.
	readyTimeout := 120 * time.Second
	if v := os.Getenv(sys.DqliteReadyTimeout); v != "" {
		parsed, err := time.ParseDuration(v)
		switch {
		case err != nil:
			db.log().Warn("Ignoring invalid DQLITE_READY_TIMEOUT", slog.String("value", v), slog.String("error", err.Error()))
		case parsed <= 0:
			db.log().Warn("Ignoring non-positive DQLITE_READY_TIMEOUT", slog.String("value", v))
		default:
			readyTimeout = parsed
		}
	}

	ctx, cancel := context.WithTimeout(db.ctx, readyTimeout)
	defer cancel()

	db.statusLock.Lock()
	db.status = types.DatabaseStarting
	db.statusLock.Unlock()

	reverter := revert.New()
	defer reverter.Fail()

	err := db.dqlite.Ready(ctx)
	if err != nil {
		return fmt.Errorf("Ready dqlite: %w", err)
	}

	if db.db == nil {
		db.db, err = db.dqlite.Open(db.ctx, db.dbName)
		if err != nil {
			return fmt.Errorf("Open dqlite: %w", err)
		}

		// Add reverter only after successfully opening db.db
		reverter.Add(func() {
			db.statusLock.Lock()
			db.status = types.DatabaseOffline
			db.statusLock.Unlock()
			closeErr := db.db.Close()
			if closeErr != nil {
				db.log().Error("Failed to close database", slog.String("address", db.listenAddr.String()), slog.String("error", closeErr.Error()))
			}

			db.db = nil
		})
	}

	err = db.waitUpgrade(bootstrap, ext)
	if err != nil {
		return err
	}

	db.log().Info("Preparing statements")

	err = clusterDB.PrepareStmts(db.db, false)
	if err != nil {
		return err
	}

	db.statusLock.Lock()
	db.status = types.DatabaseReady
	db.statusLock.Unlock()

	reverter.Success()

	return nil
}

// waitUpgrade compares the version information of all cluster members in the database to the local version.
// If this node's version is ahead of others, then it will block on the `db.upgradeCh` or up to a minute.
// If this node's version is behind others, then it returns an error.
func (db *DqliteDB) waitUpgrade(bootstrap bool, ext types.Extensions) error {
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

	checkAPIExtensions := func(currentAPIExtensions types.Extensions, clusterMemberAPIExtensions []types.Extensions) (otherNodesBehind bool, err error) {
		db.log().Debug(fmt.Sprintf("Local API extensions: %v, cluster members API extensions: %v", currentAPIExtensions, clusterMemberAPIExtensions))

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
	newSchema := db.Schema()
	newSchema.File(path.Join(db.os.StateDir(), "patch.global.sql"))

	if !bootstrap {
		checkVersions := func(ctx context.Context, current int, tx *sql.Tx) error {
			schemaVersionInternal, schemaVersionExternal, _ := newSchema.Version()
			err := update.UpdateClusterMemberSchemaVersion(ctx, tx, schemaVersionInternal, schemaVersionExternal, db.memberName())
			if err != nil {
				return fmt.Errorf("Failed to update schema version when joining cluster: %w", err)
			}

			// Attempt to update the API extensions right away in case the daemon already supports it.
			// This means we won't need to wait longer after the final member commits all schema updates.
			err = update.UpdateClusterMemberAPIExtensions(ctx, tx, ext, db.memberName())
			if err != nil {
				return fmt.Errorf("Failed to update API extensions when joining cluster: %w", err)
			}

			versionsInternal, versionsExternal, err := update.GetClusterMemberSchemaVersions(ctx, tx)
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

				return update.ErrGracefulAbort
			}

			return nil
		}

		newSchema.Check(checkVersions)
	}

	err := db.retry(db.ctx, func(ctx context.Context) error {
		_, err := newSchema.Ensure(ctx, db.db)
		if err != nil {
			return err
		}

		if !bootstrap {
			otherNodesBehindAPI := false
			// Perform the API extensions check.
			err = query.Transaction(ctx, db.db, func(ctx context.Context, tx *sql.Tx) error {
				err := update.UpdateClusterMemberAPIExtensions(ctx, tx, ext, db.memberName())
				if err != nil {
					return fmt.Errorf("Failed to update API extensions when joining cluster: %w", err)
				}

				clusterMembersAPIExtensions, err := update.GetClusterMemberAPIExtensions(ctx, tx)
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
				return update.ErrGracefulAbort
			}
		}

		return nil
	})

	// If we are not bootstrapping, wait for an upgrade notification, or wait a minute before checking again.
	if otherNodesBehind && !bootstrap {
		db.statusLock.Lock()
		db.status = types.DatabaseWaiting
		db.statusLock.Unlock()

		db.log().Warn("Waiting for other cluster members to upgrade their versions", slog.String("address", db.listenAddr.String()))
		select {
		case <-db.upgradeCh:
		case <-time.After(30 * time.Second):
		}
	}

	return err
}

// Transaction handles performing a transaction on the dqlite database.
func (db *DqliteDB) Transaction(outerCtx context.Context, f func(context.Context, *sql.Tx) error) error {
	status := db.Status()
	if status != types.DatabaseWaiting && status != types.DatabaseReady {
		return api.StatusErrorf(http.StatusServiceUnavailable, "Database is not ready yet: %v", status)
	}

	return db.retry(outerCtx, func(ctx context.Context) error {
		err := query.Transaction(ctx, db.db, f)
		if errors.Is(err, context.DeadlineExceeded) {
			// If the query timed out it likely means that the leader has abruptly become unreachable.
			// Now that this query has been cancelled, a leader election should have taken place by now.
			// So let's retry the transaction once more in case the global database is now available again.
			db.log().Warn("Transaction timed out. Retrying once", slog.String("error", err.Error()))
			return query.Transaction(ctx, db.db, f)
		}

		return err
	})
}

func (db *DqliteDB) retry(ctx context.Context, f func(context.Context) error) error {
	if db.ctx.Err() != nil {
		return f(ctx)
	}

	return query.Retry(ctx, f)
}

// Update attempts to update the database with the executable at the path specified by the SCHEMA_UPDATE variable.
func (db *DqliteDB) Update() error {
	err := db.IsOpen(context.Background())
	if err != nil {
		return fmt.Errorf("Failed to update, database is not yet open: %w", err)
	}

	updateExec := os.Getenv(sys.SchemaUpdate)
	if updateExec == "" {
		db.log().Warn("No SCHEMA_UPDATE variable set, skipping auto-update")
		return nil
	}

	// Wait a random amount of seconds (up to 30) to space out the update.
	wait := time.Duration(rand.Intn(30)) * time.Second
	db.log().Info("Triggering cluster auto-update soon", slog.String("wait", wait.String()), slog.String("updateExecutable", updateExec))
	time.Sleep(wait)

	db.log().Info("Triggering cluster auto-update now")
	_, err = shared.RunCommand(context.TODO(), updateExec)
	if err != nil {
		db.log().Error("Triggering cluster update failed", slog.String("error", err.Error()))
		return err
	}

	db.log().Info("Triggering cluster auto-update succeeded")

	return nil
}
