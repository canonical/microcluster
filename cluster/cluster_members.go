package cluster

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/canonical/microcluster/v2/internal/db/update"
	"github.com/canonical/microcluster/v2/internal/extensions"
	"github.com/canonical/microcluster/v2/rest/types"
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

// GetUpgradingClusterMembers returns the list of all cluster members during an upgrade, as well as a map of members who we consider to be in a waiting state.
// This function can be used immediately after dqlite is ready, before we have loaded any prepared statements.
// A cluster member will be in a waiting state if a different cluster member still exists with a smaller API extension count or schema version.
func GetUpgradingClusterMembers(ctx context.Context, tx *sql.Tx, schemaInternal uint64, schemaExternal uint64, apiExtensions extensions.Extensions) (allMembers []CoreClusterMember, awaitingMembers map[string]bool, err error) {
	tableName, err := update.PrepareUpdateV1(ctx, tx)
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
