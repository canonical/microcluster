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

// UpdateClusterMemberSchemaVersion sets the schema version for the cluster member with the given address.
// This helper is non-generated to work before generated statements are loaded, as we update the schema.
func UpdateClusterMemberSchemaVersion(ctx context.Context, tx *sql.Tx, internalVersion uint64, externalVersion uint64, address string) error {
	table, err := getClusterTableName(ctx, tx)
	if err != nil {
		return err
	}

	stmt := fmt.Sprintf("UPDATE %s SET schema_internal=?,schema_external=? WHERE address=?", table)
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
	table, err := getClusterTableName(ctx, tx)
	if err != nil {
		return nil, nil, err
	}

	sql := fmt.Sprintf("SELECT schema_internal,schema_external FROM %s WHERE NOT role='pending'", table)

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
