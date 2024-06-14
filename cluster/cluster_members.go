package cluster

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/shared/logger"

	"github.com/canonical/microcluster/internal/extensions"
	internalTypes "github.com/canonical/microcluster/internal/rest/types"
	"github.com/canonical/microcluster/rest/types"
)

//go:generate -command mapper lxd-generate db mapper -t cluster_members.mapper.go
//go:generate mapper reset
//
//go:generate mapper stmt -e internal_cluster_member objects table=internal_cluster_members
//go:generate mapper stmt -e internal_cluster_member objects-by-Address table=internal_cluster_members
//go:generate mapper stmt -e internal_cluster_member objects-by-Name table=internal_cluster_members
//go:generate mapper stmt -e internal_cluster_member id table=internal_cluster_members
//go:generate mapper stmt -e internal_cluster_member create table=internal_cluster_members
//go:generate mapper stmt -e internal_cluster_member delete-by-Address table=internal_cluster_members
//go:generate mapper stmt -e internal_cluster_member update table=internal_cluster_members
//
//go:generate mapper method -i -e internal_cluster_member GetMany table=internal_cluster_members
//go:generate mapper method -i -e internal_cluster_member GetOne table=internal_cluster_members
//go:generate mapper method -i -e internal_cluster_member ID table=internal_cluster_members
//go:generate mapper method -i -e internal_cluster_member Exists table=internal_cluster_members
//go:generate mapper method -i -e internal_cluster_member Create table=internal_cluster_members
//go:generate mapper method -i -e internal_cluster_member DeleteOne-by-Address table=internal_cluster_members
//go:generate mapper method -i -e internal_cluster_member Update table=internal_cluster_members

// Role is the role of the dqlite cluster member.
type Role string

// Pending indicates that a node is about to be added or removed.
const Pending Role = "PENDING"

// InternalClusterMember represents the global database entry for a dqlite cluster member.
type InternalClusterMember struct {
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

// InternalClusterMemberFilter is used for filtering queries using generated methods.
type InternalClusterMemberFilter struct {
	Address *string
	Name    *string
}

// ToAPI returns the api struct for a ClusterMember database entity.
// The cluster member's status will be reported as unreachable by default.
func (c InternalClusterMember) ToAPI() (*internalTypes.ClusterMember, error) {
	address, err := types.ParseAddrPort(c.Address)
	if err != nil {
		return nil, fmt.Errorf("Failed to parse address %q of database cluster member: %w", c.Address, err)
	}

	certificate, err := types.ParseX509Certificate(c.Certificate)
	if err != nil {
		return nil, fmt.Errorf("Failed to parse certificate of database cluster member with address %q: %w", c.Address, err)
	}

	return &internalTypes.ClusterMember{
		ClusterMemberLocal: internalTypes.ClusterMemberLocal{
			Name:        c.Name,
			Address:     address,
			Certificate: *certificate,
		},
		Role:                  string(c.Role),
		SchemaInternalVersion: c.SchemaInternal,
		SchemaExternalVersion: c.SchemaExternal,
		LastHeartbeat:         c.Heartbeat,
		Status:                internalTypes.MemberUnreachable,
		Extensions:            c.APIExtensions,
	}, nil
}

// prepareUpdateV1 creates the temporary table `internal_cluster_members_new` if we have not yet run `updateFromV1`.
// To keep this table in sync with `internal_cluster_members`, a create & update trigger is created as well.
// This table (and its triggers) will be deleted by `updateFromV1`.
func prepareUpdateV1(tx *sql.Tx) (tableName string, err error) {
	// Check if we need a temporary table, if no `type` field exists in the schemas table.
	stmt := "SELECT count(name) FROM pragma_table_info('schemas') WHERE name IN ('type');"
	var count int
	err = tx.QueryRow(stmt).Scan(&count)
	if err != nil {
		return "", err
	}

	// If we have a `type` field, then the default database is valid, so we can just use that.
	// TODO: Dynamically rename to "core_"
	if count == 1 {
		return "internal_cluster_members", nil
	}

	// Check if another cluster member has already created the temporary table.
	stmt = "SELECT name FROM sqlite_master WHERE name = 'internal_cluster_members_new';"
	err = tx.QueryRow(stmt).Scan(&tableName)
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
`

	_, err = tx.Exec(stmt)
	if err != nil {
		return "", err
	}

	return "internal_cluster_members_new", nil
}

// UpdateClusterMemberSchemaVersion sets the schema version for the cluster member with the given address.
// This helper is non-generated to work before generated statements are loaded, as we update the schema.
func UpdateClusterMemberSchemaVersion(tx *sql.Tx, internalVersion uint64, externalVersion uint64, address string) error {
	tableName, err := prepareUpdateV1(tx)
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
	tableName, err := prepareUpdateV1(tx)
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
func UpdateClusterMemberAPIExtensions(tx *sql.Tx, apiExtensions extensions.Extensions, address string) error {
	stmt := "UPDATE internal_cluster_members SET api_extensions=? WHERE address=?"
	result, err := tx.Exec(stmt, apiExtensions, address)
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
	query := "SELECT api_extensions FROM internal_cluster_members WHERE NOT role='pending'"
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
