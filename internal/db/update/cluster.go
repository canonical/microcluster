package update

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/shared/logger"

	"github.com/canonical/microcluster/internal/extensions"
)

// PrepareUpdateV1 creates the temporary table `internal_cluster_members_new` if we have not yet run `updateFromV1`.
// To keep this table in sync with `internal_cluster_members`, a create & update trigger is created as well.
// This table (and its triggers) will be deleted by `updateFromV1`.
func PrepareUpdateV1(ctx context.Context, tx *sql.Tx) (tableName string, err error) {
	// Check if we need a temporary table, if no `type` field exists in the schemas table.
	stmt := "SELECT count(name) FROM pragma_table_info('schemas') WHERE name IN ('type');"
	var count int
	err = tx.QueryRowContext(ctx, stmt).Scan(&count)
	if err != nil {
		return "", err
	}

	// If we have a `type` field, then the default database is valid, so we can just use that.
	// TODO: Dynamically rename to "core_"
	if count == 1 {
		return getClusterTableName(ctx, tx)
	}

	// Check if another cluster member has already created the temporary table.
	stmt = "SELECT name FROM sqlite_master WHERE name = 'internal_cluster_members_new';"
	err = tx.QueryRowContext(ctx, stmt).Scan(&tableName)
	if err != nil && err != sql.ErrNoRows {
		return "", err
	}

	if tableName != "" {
		return tableName, nil
	}

	// If no cluster member has created the temporary table, create it and return its name.
	// Also create some triggers so that updates to the `internal_cluster_members` table are propagated to the new table.
	stmt = `
CREATE TABLE internal_cluster_members_new (
  id                   INTEGER   PRIMARY  KEY    AUTOINCREMENT  NOT  NULL,
  name                 TEXT      NOT      NULL,
  address              TEXT      NOT      NULL,
  certificate          TEXT      NOT      NULL,
  schema_internal      INTEGER   NOT      NULL,
  schema_external      INTEGER   NOT      NULL,
  heartbeat            DATETIME  NOT      NULL,
  role                 TEXT      NOT      NULL,
  UNIQUE(name),
  UNIQUE(certificate)
);

INSERT INTO internal_cluster_members_new SELECT id,name,address,certificate,1,(schema-1),heartbeat,role FROM internal_cluster_members;

CREATE TRIGGER internal_cluster_member_added
	AFTER INSERT ON internal_cluster_members
FOR EACH ROW
BEGIN
	INSERT INTO internal_cluster_members_new (id,name,address,certificate,schema_internal,schema_external,heartbeat,role)
	VALUES (NEW.id,NEW.name,NEW.address,NEW.certificate,1,(NEW.schema-1),heartbeat,role);
END;

	CREATE TRIGGER internal_cluster_member_removed
	AFTER DELETE ON internal_cluster_members
FOR EACH ROW
BEGIN
	DELETE FROM internal_cluster_members_new
	WHERE id = OLD.id;
END;
`

	_, err = tx.ExecContext(ctx, stmt)
	if err != nil {
		return "", err
	}

	return "internal_cluster_members_new", nil
}

// UpdateClusterMemberSchemaVersion sets the schema version for the cluster member with the given address.
// This helper is non-generated to work before generated statements are loaded, as we update the schema.
func UpdateClusterMemberSchemaVersion(ctx context.Context, tx *sql.Tx, internalVersion uint64, externalVersion uint64, memberName string) error {
	tableName, err := PrepareUpdateV1(ctx, tx)
	if err != nil {
		return err
	}

	stmt := fmt.Sprintf("UPDATE %s SET schema_internal=?,schema_external=? WHERE name=?", tableName)
	result, err := tx.Exec(stmt, internalVersion, externalVersion, memberName)
	if err != nil {
		return err
	}

	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("Updated %d rows instead of 1", n)
	}

	return nil
}

// GetClusterMemberSchemaVersions returns the schema versions from all cluster members that are not pending.
// This helper is non-generated to work before generated statements are loaded, as we update the schema.
func GetClusterMemberSchemaVersions(ctx context.Context, tx *sql.Tx) (internalSchema []uint64, externalSchema []uint64, err error) {
	tableName, err := PrepareUpdateV1(ctx, tx)
	if err != nil {
		return nil, nil, err
	}

	sql := fmt.Sprintf("SELECT schema_internal,schema_external FROM %s WHERE NOT role='pending'", tableName)

	internalSchema = []uint64{}
	externalSchema = []uint64{}
	dest := func(scan func(dest ...any) error) error {
		var internal, external uint64
		err := scan(&internal, &external)
		if err != nil {
			return err
		}

		internalSchema = append(internalSchema, internal)
		externalSchema = append(externalSchema, external)

		return nil
	}

	err = query.Scan(ctx, tx, sql, dest)
	if err != nil {
		return nil, nil, err
	}

	return internalSchema, externalSchema, nil
}

// UpdateClusterMemberAPIExtensions sets the API extensions for the cluster member with the given address.
// This helper is non-generated to work before generated statements are loaded, as we update the API extensions.
func UpdateClusterMemberAPIExtensions(ctx context.Context, tx *sql.Tx, apiExtensions extensions.Extensions, memberName string) error {
	table, err := getClusterTableName(ctx, tx)
	if err != nil {
		return err
	}

	// Check for the `api_extensions` column, which may not exist if we haven't actually run the update yet.
	stmt := fmt.Sprintf(`
SELECT count(name)
FROM pragma_table_info('%s')
WHERE name IN ('api_extensions');
`, table)

	var count int
	err = tx.QueryRow(stmt).Scan(&count)
	if err != nil {
		return err
	}

	if count == 0 {
		logger.Warn("Skipping API extension update, schema does not yet support it", logger.Ctx{"memberName": memberName})
		return nil
	}

	stmt = fmt.Sprintf("UPDATE %s SET api_extensions=? WHERE name=?", table)
	result, err := tx.ExecContext(ctx, stmt, apiExtensions, memberName)
	if err != nil {
		return err
	}

	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("Updated %d rows instead of 1", n)
	}

	return nil
}

// GetClusterMemberAPIExtensions returns the API extensions from all cluster members that are not pending.
// This helper is non-generated to work before generated statements are loaded, as we update the API extensions.
func GetClusterMemberAPIExtensions(ctx context.Context, tx *sql.Tx) ([]extensions.Extensions, error) {
	table, err := getClusterTableName(ctx, tx)
	if err != nil {
		return nil, err
	}

	query := fmt.Sprintf("SELECT api_extensions FROM %s WHERE NOT role='pending'", table)
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}

	defer func() {
		err := rows.Close()
		if err != nil {
			logger.Error("Failed to close rows after reading API extensions", logger.Ctx{"error": err})
		}
	}()

	var results []extensions.Extensions
	for rows.Next() {
		var ext extensions.Extensions
		err := rows.Scan(&ext)
		if err != nil {
			return nil, err
		}

		results = append(results, ext)
	}

	err = rows.Err()
	if err != nil {
		return nil, err
	}

	return results, nil
}

// getClusterTableName returns the name of the table that holds the record of cluster members from sqlite_master.
// Prior to updateFromV4, this table was called `internal_cluster_members`, but now it is `core_cluster_members`.
// Since we need to check this table to perform the update that renames it, we can use this function to dynamically determine its name.
func getClusterTableName(ctx context.Context, tx *sql.Tx) (string, error) {
	stmt := "SELECT name FROM sqlite_master WHERE name = 'internal_cluster_members' OR name = 'core_cluster_members'"
	tables, err := query.SelectStrings(ctx, tx, stmt)
	if err != nil {
		return "", err
	}

	if len(tables) != 1 || tables[0] == "" {
		return "", fmt.Errorf("No cluster members table found")
	}

	return tables[0], nil
}
