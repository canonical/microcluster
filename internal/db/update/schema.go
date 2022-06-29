package update

import (
	"context"
	"database/sql"
	"fmt"
	"io/ioutil"
	"os"
	"sort"
	"strings"

	"github.com/lxc/lxd/lxd/db/query"
	"github.com/lxc/lxd/lxd/db/schema"
	"github.com/lxc/lxd/shared"
)

type SchemaUpdate struct {
	updates []schema.Update // Ordered series of updates making up the schema
	hook    schema.Hook     // Optional hook to execute whenever a update gets applied
	fresh   string          // Optional SQL statement used to create schema from scratch
	check   schema.Check    // Optional callback invoked before doing any update
	path    string          // Optional path to a file containing extra queries to run
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

func (s *SchemaUpdate) Version() int {
	return len(s.updates)
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
		err := execFromFile(tx, s.path, s.hook)
		if err != nil {
			return fmt.Errorf("failed to execute queries from %s: %w", s.path, err)
		}

		exists, err := doesSchemaTableExist(tx)
		if err != nil {
			return fmt.Errorf("failed to check if schema table is there: %w", err)
		}

		current := 0
		if exists {
			versions, err := query.SelectIntegers(tx, "SELECT version FROM schemas ORDER BY version")
			if err != nil {
				return err
			}

			if len(versions) > 0 {
				current = versions[len(versions)-1]
			}
		}

		if s.check != nil {
			err := s.check(current, tx)
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
		if current == 0 && s.fresh != "" {
			_, err = tx.Exec(s.fresh)
			if err != nil {
				return fmt.Errorf("cannot apply fresh schema: %w", err)
			}
		} else {
			err = ensureUpdatesAreApplied(ctx, tx, current, s.updates, s.hook)
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

// Dump returns a text of SQL commands that can be used to create this schema
// from scratch in one go, without going thorugh individual patches
// (essentially flattening them).
//
// It requires that all patches in this schema have been applied, otherwise an
// error will be returned.
func (s *SchemaUpdate) Dump(db *sql.DB) (string, error) {
	var statements []string
	err := query.Transaction(context.TODO(), db, func(ctx context.Context, tx *sql.Tx) error {
		versions, err := query.SelectIntegers(tx, "SELECT version FROM schemas ORDER BY version")
		if err != nil {
			return err
		}

		if len(versions) == 0 {
			return fmt.Errorf("expected schema table to contain at least one row")
		}

		err = checkSchemaVersionsHaveNoHoles(versions)
		if err != nil {
			return err
		}

		current := versions[len(versions)-1]
		if current != len(s.updates) {
			return fmt.Errorf("update level is %d, expected %d", current, len(s.updates))
		}

		statements, err = selectTablesSQL(tx)
		return err
	})
	if err != nil {
		return "", err
	}
	for i, statement := range statements {
		statements[i] = formatSQL(statement)
	}

	// Add a statement for inserting the current schema version row.
	statements = append(
		statements,
		fmt.Sprintf(`
INSERT INTO schemas (version, updated_at) VALUES (%d, strftime("%%s"))
`, len(s.updates)))
	return strings.Join(statements, ";\n"), nil
}

// Check that the given list of update version numbers doesn't have "holes",
// that is each version equal the preceding version plus 1.
func checkSchemaVersionsHaveNoHoles(versions []int) error {
	// Ensure that there are no "holes" in the recorded versions.
	for i := range versions[:len(versions)-1] {
		if versions[i+1] != versions[i]+1 {
			return fmt.Errorf("Missing updates: %d to %d", versions[i], versions[i+1])
		}
	}
	return nil
}

// Return a list of SQL statements that can be used to create all tables in the
// database.
func selectTablesSQL(tx *sql.Tx) ([]string, error) {
	statement := `
SELECT sql FROM sqlite_master WHERE
  type IN ('table', 'index', 'view', 'trigger') AND
  name != 'schemas' AND
  name NOT LIKE 'sqlite_%'
ORDER BY name
`
	return query.SelectStrings(tx, statement)
}

// Apply any pending update that was not yet applied.
func ensureUpdatesAreApplied(ctx context.Context, tx *sql.Tx, current int, updates []schema.Update, hook schema.Hook) error {
	if current > len(updates) {
		return fmt.Errorf(
			"schema version '%d' is more recent than expected '%d'",
			current, len(updates))
	}

	// If there are no updates, there's nothing to do.
	if len(updates) == 0 {
		return nil
	}

	// Apply missing updates.
	for _, update := range updates[current:] {
		if hook != nil {
			err := hook(current, tx)
			if err != nil {
				return fmt.Errorf(
					"failed to execute hook (version %d): %v", current, err)
			}
		}
		err := update(tx)
		if err != nil {
			return fmt.Errorf("failed to apply update %d: %w", current, err)
		}
		current++

		statement := `INSERT INTO schemas (version, updated_at) VALUES (?, strftime("%s"))`
		_, err = tx.Exec(statement, current)
		if err != nil {
			return fmt.Errorf("failed to insert version %d: %w", current, err)
		}
	}

	return nil
}

// Read the given file (if it exists) and executes all queries it contains.
func execFromFile(tx *sql.Tx, path string, hook schema.Hook) error {
	if !shared.PathExists(path) {
		return nil
	}

	bytes, err := ioutil.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}

	if hook != nil {
		err := hook(-1, tx)
		if err != nil {
			return fmt.Errorf("failed to execute hook: %w", err)
		}
	}

	_, err = tx.Exec(string(bytes))
	if err != nil {
		return err
	}

	err = os.Remove(path)
	if err != nil {
		return fmt.Errorf("failed to remove file: %w", err)
	}

	return nil
}

// Format the given SQL statement in a human-readable way.
//
// In particular make sure that each column definition in a CREATE TABLE clause
// is in its own row, since SQLite dumps occasionally stuff more than one
// column in the same line.
func formatSQL(statement string) string {
	lines := strings.Split(statement, "\n")
	for i, line := range lines {
		if strings.Contains(line, "UNIQUE") {
			// Let UNIQUE(x, y) constraints alone.
			continue
		}
		lines[i] = strings.Replace(line, ", ", ",\n    ", -1)
	}
	return strings.Join(lines, "\n")
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

// NewFromMap creates a new schema Schema with the updates specified in the
// given map. The keys of the map are schema versions that when upgraded will
// trigger the associated Update value. It's required that the minimum key in
// the map is 1, and if key N is present then N-1 is present too, with N>1
// (i.e. there are no missing versions).
func NewFromMap(versionsToUpdates map[int]schema.Update) *SchemaUpdate {
	// Collect all version keys.
	versions := []int{}
	for version := range versionsToUpdates {
		versions = append(versions, version)
	}

	// Sort the versions,
	sort.Ints(versions)

	// Build the updates slice.
	updates := []schema.Update{}
	for i, version := range versions {
		// Assert that we start from 1 and there are no gaps.
		if version != i+1 {
			panic(fmt.Sprintf("updates map misses version %d", i+1))
		}

		updates = append(updates, versionsToUpdates[version])
	}

	return &SchemaUpdate{
		updates: updates,
	}
}
