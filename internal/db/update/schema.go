package update

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/lxd/db/schema"
	"github.com/canonical/lxd/shared"
)

// updateType represents whether the update is an internal or external schema update.
type updateType uint

const (
	// updateInternal represents an internal schema update from microcluster.
	updateInternal updateType = 0

	// updateExternal represents a schema update external to microcluster, applied after all internal updates.
	updateExternal updateType = 1
)

// SchemaUpdate holds the configuration for executing schema updates.
type SchemaUpdate struct {
	updates map[updateType][]schema.Update // Ordered series of internal and external updates making up the schema
	hook    schema.Hook                    // Optional hook to execute whenever a update gets applied
	fresh   string                         // Optional SQL statement used to create schema from scratch
	check   schema.Check                   // Optional callback invoked before doing any update
	path    string                         // Optional path to a file containing extra queries to run
}

// Fresh sets a statement that will be used to create the schema from scratch
// when bootstraping an empty database. It should be a "flattening" of the
// available updates, generated using the Dump() method. If not given, all
// patches will be applied in order.
func (s *SchemaUpdate) Fresh(statement string) {
	s.fresh = statement
}

// Check instructs the schema to invoke the given function whenever Ensure is
// invoked, before applying any due update. It can be used for aborting the
// operation.
func (s *SchemaUpdate) Check(check schema.Check) {
	s.check = check
}

// Version returns the internal and external schema update versions, corresponding to the number of updates that have occurred.
func (s *SchemaUpdate) Version() (internalVersion uint64, externalVersion uint64) {
	return uint64(len(s.updates[updateInternal])), uint64(len(s.updates[updateExternal]))
}

// Ensure makes sure that the actual schema in the given database matches the
// one defined by our updates.
//
// All updates are applied transactionally. In case any error occurs the
// transaction will be rolled back and the database will remain unchanged.
//
// A update will be applied only if it hasn't been before (currently applied
// updates are tracked in the a 'shema' table, which gets automatically
// created).
//
// If no error occurs, the integer returned by this method is the
// initial version that the schema has been upgraded from.
func (s *SchemaUpdate) Ensure(db *sql.DB) (int, error) {
	var current int
	aborted := false
	err := query.Transaction(context.TODO(), db, func(ctx context.Context, tx *sql.Tx) error {
		err := execFromFile(ctx, tx, s.path, s.hook)
		if err != nil {
			return fmt.Errorf("failed to execute queries from %s: %w", s.path, err)
		}

		exists, err := doesSchemaTableExist(tx)
		if err != nil {
			return fmt.Errorf("failed to check if schema table is there: %w", err)
		}

		var versions []int
		if exists {
			// updateFromV1 changes the schema table and needs to be run before we calculate the schema version.
			err := updateFromV1(ctx, tx)
			if err != nil {
				return err
			}

			// maxVersionsStmt grabs the highest schema `version` column for each `type` (updateInternal/0) (updateExternal/1).
			// The result is list of size 2, with index 0 corresponding to the max internal version and index 1 to the max external version, thanks to UNION ALL.
			// The selected column must default to zero, otherwise query.SelectIntegers will fail to parse a null value as an integer.
			maxVersionsStmt := "SELECT COALESCE(MAX(version), 0) FROM schemas WHERE type = 0 UNION ALL SELECT COALESCE(MAX(version), 0) FROM schemas WHERE type = 1"
			versions, err = query.SelectIntegers(ctx, tx, maxVersionsStmt)
			if err != nil {
				return err
			}
		}

		if s.check != nil {
			err := s.check(ctx, current, tx)
			if err == schema.ErrGracefulAbort {
				// Abort the update gracefully, committing what
				// we've done so far.
				aborted = true
				return nil
			}
			if err != nil {
				return err
			}
		}

		// When creating the schema from scratch, use the fresh dump if
		// available. Otherwise just apply all relevant updates.
		if versions == nil && s.fresh != "" {
			_, err = tx.ExecContext(ctx, s.fresh)
			if err != nil {
				return fmt.Errorf("cannot apply fresh schema: %w", err)
			}
		} else {
			err = ensureUpdatesAreApplied(ctx, tx, versions, s.updates, s.hook)
			if err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return -1, err
	}
	if aborted {
		return current, schema.ErrGracefulAbort
	}
	return current, nil
}

// Apply any pending update that was not yet applied.
func ensureUpdatesAreApplied(ctx context.Context, tx *sql.Tx, versions []int, schemaUpdates map[updateType][]schema.Update, hook schema.Hook) error {
	if versions == nil {
		versions = []int{0, 0}
	}

	// Internal updates should be run before external ones.
	updateOrder := []updateType{updateInternal, updateExternal}
	for _, updateType := range updateOrder {
		updates := schemaUpdates[updateType]
		version := versions[updateType]
		if version > len(updates) {
			return fmt.Errorf("Schema version '%d' is more recent than expected '%d'", version, len(updates))
		}

		// If there are no updates, there's nothing to do.
		if len(updates) == 0 {
			return nil
		}

		// Apply missing updates.
		for _, update := range updates[version:] {
			if hook != nil {
				err := hook(ctx, version, tx)
				if err != nil {
					return fmt.Errorf("Failed to execute hook (version %d): %v", version, err)
				}
			}

			err := update(ctx, tx)
			if err != nil {
				return fmt.Errorf("Failed to apply update %d: %w", version, err)
			}

			if updateType == updateInternal && version == 0 {
				err = updateFromV1(ctx, tx)
				if err != nil {
					return fmt.Errorf("Failed to apply special update 1: %w", err)
				}
			}

			version++

			statement := `INSERT INTO schemas (version, type, updated_at) VALUES (?, ?, strftime("%s"))`
			_, err = tx.ExecContext(ctx, statement, version, updateType)
			if err != nil {
				return fmt.Errorf("failed to insert version %d: %w", version, err)
			}
		}
	}

	return nil
}

// Read the given file (if it exists) and executes all queries it contains.
func execFromFile(ctx context.Context, tx *sql.Tx, path string, hook schema.Hook) error {
	if !shared.PathExists(path) {
		return nil
	}

	bytes, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}

	if hook != nil {
		err := hook(ctx, -1, tx)
		if err != nil {
			return fmt.Errorf("failed to execute hook: %w", err)
		}
	}

	_, err = tx.ExecContext(ctx, string(bytes))
	if err != nil {
		return err
	}

	err = os.Remove(path)
	if err != nil {
		return fmt.Errorf("failed to remove file: %w", err)
	}

	return nil
}

// doesSchemaTableExist return whether the schema table is present in the
// database.
func doesSchemaTableExist(tx *sql.Tx) (bool, error) {
	statement := `
SELECT COUNT(name) FROM sqlite_master WHERE type = 'table' AND name = 'schemas'
`
	rows, err := tx.Query(statement)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()

	if !rows.Next() {
		return false, fmt.Errorf("schema table query returned no rows")
	}

	var count int

	err = rows.Scan(&count)
	if err != nil {
		return false, err
	}

	return count == 1, nil
}
