package cluster

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/lxc/lxd/lxd/db/query"

	internalTypes "github.com/canonical/microcluster/internal/rest/types"
	"github.com/canonical/microcluster/rest/types"
)

//go:generate -command mapper lxd-generate db mapper -t cluster_members.mapper.go
//go:generate mapper reset
//
//go:generate mapper stmt -e internal_cluster_member objects table=internal_cluster_members
//go:generate mapper stmt -e internal_cluster_member objects-by-Address table=internal_cluster_members
//go:generate mapper stmt -e internal_cluster_member id table=internal_cluster_members
//go:generate mapper stmt -e internal_cluster_member create table=internal_cluster_members
//go:generate mapper stmt -e internal_cluster_member delete-by-Address table=internal_cluster_members
//go:generate mapper stmt -e internal_cluster_member update table=internal_cluster_members
//
//go:generate mapper method -i -e internal_cluster_member GetMany
//go:generate mapper method -i -e internal_cluster_member GetOne
//go:generate mapper method -i -e internal_cluster_member ID
//go:generate mapper method -i -e internal_cluster_member Exists
//go:generate mapper method -i -e internal_cluster_member Create
//go:generate mapper method -i -e internal_cluster_member DeleteOne-by-Address
//go:generate mapper method -i -e internal_cluster_member Update

// Role is the role of the dqlite cluster member, with the addition of "pending" for nodes about to be added or
// removed.
type Role string

const Pending Role = "PENDING"

// InternalClusterMember represents the global database entry for a dqlite cluster member.
type InternalClusterMember struct {
	ID          int
	Name        string `db:"primary=yes"`
	Address     string
	Certificate string
	Schema      int
	Heartbeat   time.Time
	Role        Role
}

// InternalClusterMemberFilter is used for filtering queries using generated methods.
type InternalClusterMemberFilter struct {
	Address *string
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
		Role:          string(c.Role),
		SchemaVersion: c.Schema,
		LastHeartbeat: c.Heartbeat,
		Status:        internalTypes.MemberUnreachable,
	}, nil
}

// UpdateClusterMemberSchemaVersion sets the schema version for the cluster member with the given address.
// This helper is non-generated to work before generated statements are loaded, as we update the schema.
func UpdateClusterMemberSchemaVersion(tx *sql.Tx, version int, address string) error {
	stmt := "UPDATE internal_cluster_members SET schema=? WHERE address=?"
	result, err := tx.Exec(stmt, version, address)
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
func GetClusterMemberSchemaVersions(tx *sql.Tx) ([]int, error) {
	sql := "SELECT schema FROM internal_cluster_members WHERE NOT role='pending'"
	versions, err := query.SelectIntegers(tx, sql)
	if err != nil {
		return nil, err
	}

	return versions, nil
}
