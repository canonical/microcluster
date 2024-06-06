package cluster

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/shared/logger"

	"github.com/canonical/microcluster/internal/extensions"
	"github.com/canonical/microcluster/rest/types"
)

//go:generate -command mapper lxd-generate db mapper -t cluster_members.mapper.go
//go:generate mapper reset
//
//go:generate mapper stmt -e core_cluster_member objects table=core_cluster_members
//go:generate mapper stmt -e core_cluster_member objects-by-Address table=core_cluster_members
//go:generate mapper stmt -e core_cluster_member objects-by-Name table=core_cluster_members
//go:generate mapper stmt -e core_cluster_member id table=core_cluster_members
//go:generate mapper stmt -e core_cluster_member create table=core_cluster_members
//go:generate mapper stmt -e core_cluster_member delete-by-Address table=core_cluster_members
//go:generate mapper stmt -e core_cluster_member update table=core_cluster_members
//
//go:generate mapper method -i -e core_cluster_member GetMany table=core_cluster_members
//go:generate mapper method -i -e core_cluster_member GetOne table=core_cluster_members
//go:generate mapper method -i -e core_cluster_member ID table=core_cluster_members
//go:generate mapper method -i -e core_cluster_member Exists table=core_cluster_members
//go:generate mapper method -i -e core_cluster_member Create table=core_cluster_members
//go:generate mapper method -i -e core_cluster_member DeleteOne-by-Address table=core_cluster_members
//go:generate mapper method -i -e core_cluster_member Update table=core_cluster_members

// Role is the role of the dqlite cluster member.
type Role string

// Pending indicates that a node is about to be added or removed.
const Pending Role = "PENDING"

// CoreClusterMember represents the global database entry for a dqlite cluster member.
type CoreClusterMember struct {
	ID             int
	Name           string `db:"primary=yes"`
	Address        string
	Certificate    string
	SchemaInternal uint64
	SchemaExternal uint64
	APIExtensions  extensions.Extensions
	Heartbeat      time.Time
	Role           Role
}

// CoreClusterMemberFilter is used for filtering queries using generated methods.
type CoreClusterMemberFilter struct {
	Address *string
	Name    *string
}

// ToAPI returns the api struct for a ClusterMember database entity.
// The cluster member's status will be reported as unreachable by default.
func (c CoreClusterMember) ToAPI() (*types.ClusterMember, error) {
	address, err := types.ParseAddrPort(c.Address)
	if err != nil {
		return nil, fmt.Errorf("Failed to parse address %q of database cluster member: %w", c.Address, err)
	}

	certificate, err := types.ParseX509Certificate(c.Certificate)
	if err != nil {
		return nil, fmt.Errorf("Failed to parse certificate of database cluster member with address %q: %w", c.Address, err)
	}

	return &types.ClusterMember{
		ClusterMemberLocal: types.ClusterMemberLocal{
			Name:        c.Name,
			Address:     address,
			Certificate: *certificate,
		},
		Role:                  string(c.Role),
		SchemaInternalVersion: c.SchemaInternal,
		SchemaExternalVersion: c.SchemaExternal,
		LastHeartbeat:         c.Heartbeat,
		Status:                types.MemberUnreachable,
		Extensions:            c.APIExtensions,
	}, nil
}

// prepareUpdateV1 creates the temporary table `internal_cluster_members_new` if we have not yet run `updateFromV1`.
// To keep this table in sync with `internal_cluster_members`, a create & update trigger is created as well.
// This table (and its triggers) will be deleted by `updateFromV1`.
func prepareUpdateV1(ctx context.Context, tx *sql.Tx) (tableName string, err error) {
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
func UpdateClusterMemberSchemaVersion(ctx context.Context, tx *sql.Tx, internalVersion uint64, externalVersion uint64, address string) error {
	tableName, err := prepareUpdateV1(ctx, tx)
	if err != nil {
		return err
	}

	stmt := fmt.Sprintf("UPDATE %s SET schema_internal=?,schema_external=? WHERE address=?", tableName)
	result, err := tx.Exec(stmt, internalVersion, externalVersion, address)
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
	tableName, err := prepareUpdateV1(ctx, tx)
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
func UpdateClusterMemberAPIExtensions(ctx context.Context, tx *sql.Tx, apiExtensions extensions.Extensions, address string) error {
	table, err := getClusterTableName(ctx, tx)
	if err != nil {
		return err
	}

	stmt := fmt.Sprintf("UPDATE %s SET api_extensions=? WHERE address=?", table)
	result, err := tx.ExecContext(ctx, stmt, apiExtensions, address)
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

// GetUpgradingClusterMembers returns the list of all cluster members during an upgrade, as well as a map of members who we consider to be in a waiting state.
// This function can be used immediately after dqlite is ready, before we have loaded any prepared statements.
// A cluster member will be in a waiting state if a different cluster member still exists with a smaller API extension count or schema version.
func GetUpgradingClusterMembers(ctx context.Context, tx *sql.Tx, schemaInternal uint64, schemaExternal uint64, apiExtensions extensions.Extensions) (allMembers []CoreClusterMember, awaitingMembers map[string]bool, err error) {
	tableName, err := prepareUpdateV1(ctx, tx)
	if err != nil {
		return nil, nil, err
	}

	// Check for the `api_extensions` column, which may not exist if we haven't actually run the update yet.
	stmt := fmt.Sprintf(`
SELECT count(name)
FROM pragma_table_info('%s')
WHERE name IN ('api_extensions');
`, tableName)

	var count int
	err = tx.QueryRow(stmt).Scan(&count)
	if err != nil {
		return nil, nil, err
	}

	// Fetch all cluster members with a smaller schema version than we expect.
	stmt = `SELECT id, name, address, certificate, schema_internal, schema_external, %s, heartbeat, role
  FROM %s
  ORDER BY name
	`

	// If API extensions are supported, ensure the list for each cluster member also matches what we expect,
	// and only return cluster members for whom it does not.
	apiField := "'[]' as api_extensions"
	if count == 1 {
		apiField = "api_extensions"
	}

	stmt = fmt.Sprintf(stmt, apiField, tableName)
	allMembers, err = getCoreClusterMembersRaw(ctx, tx, stmt)
	if err != nil {
		return nil, nil, err
	}

	awaitingMembers = make(map[string]bool, len(allMembers))
	for _, member := range allMembers {
		awaitingMembers[member.Name] = member.SchemaInternal < schemaInternal || member.SchemaExternal < schemaExternal

		// If we have API extension support, also compare against the database API extensions.
		if count == 1 {
			awaitingMembers[member.Name] = member.APIExtensions.IsSameVersion(apiExtensions) != nil || awaitingMembers[member.Name]
		}
	}

	return allMembers, awaitingMembers, nil
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
