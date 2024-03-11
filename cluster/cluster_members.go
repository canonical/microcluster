package cluster

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

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

// Role is the role of the dqlite cluster member, with the addition of "pending" for nodes about to be added or
// removed.
type Role string

const Pending Role = "PENDING"

// InternalClusterMember represents the global database entry for a dqlite cluster member.
type InternalClusterMember struct {
	ID                    int
	Name                  string `db:"primary=yes"`
	Address               string
	Certificate           string
	Schema                int
	InternalAPIExtensions sql.NullString // Some nodes might not have an extension system yet so we allow for null value.
	ExternalAPIExtensions sql.NullString // Some nodes might not have an extension system yet so we allow for null value.
	Heartbeat             time.Time
	Role                  Role
}

// InternalClusterMemberFilter is used for filtering queries using generated methods.
type InternalClusterMemberFilter struct {
	Address *string
	Name    *string
}

// InternalClusterMemberVersioningInfo represents the schema version and API extensions of a cluster member.
type InternalClusterMemberVersioningInfo struct {
	SchemaVersion int
	Extensions    *extensions.Extensions
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

	var internalExtensions []string
	var externalExtensions []string

	if c.InternalAPIExtensions.Valid {
		internalExtensions = strings.Split(c.InternalAPIExtensions.String, ",")
	} else {
		internalExtensions = []string{}
	}

	if c.ExternalAPIExtensions.Valid {
		externalExtensions = strings.Split(c.ExternalAPIExtensions.String, ",")
	} else {
		externalExtensions = []string{}
	}

	extensions := append(internalExtensions, externalExtensions...)

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
		Extensions:    extensions,
	}, nil
}

// UpdateClusterMemberSchemaVersionAndAPIExtensions sets the schema version, and the API extensions for the cluster member with the given address.
// This helper is non-generated to work before generated statements are loaded, as we update the schema and the API extensions.
func UpdateClusterMemberSchemaVersionAndAPIExtensions(tx *sql.Tx, info InternalClusterMemberVersioningInfo, address string) error {
	stmt := "UPDATE internal_cluster_members SET schema=?, internal_api_extensions = ?, external_api_extensions = ? WHERE address=?"
	internalExtensions, externalExtensions := info.Extensions.SerializeForDB()
	result, err := tx.Exec(stmt, info.SchemaVersion, internalExtensions, externalExtensions, address)
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

// GetClusterMemberSchemaVersionsAndAPIExtensions returns the schema versions, internal and external API extensions from all cluster members that are not pending.
// This helper is non-generated to work before generated statements are loaded, as we update the schema and the API extensions.
func GetClusterMemberSchemaVersionsAndAPIExtensions(ctx context.Context, tx *sql.Tx) ([]InternalClusterMemberVersioningInfo, error) {
	query := "SELECT schema, internal_api_extensions, external_api_extensions FROM internal_cluster_members WHERE NOT role='pending'"
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}

	defer rows.Close()

	var results []InternalClusterMemberVersioningInfo
	for rows.Next() {
		var info InternalClusterMemberVersioningInfo
		var InternalAPIExtensions sql.NullString
		var ExternalAPIExtensions sql.NullString
		err := rows.Scan(&info.SchemaVersion, &InternalAPIExtensions, &ExternalAPIExtensions)
		if err != nil {
			return nil, err
		}

		var ext *extensions.Extensions
		// In the case of an upgrade, some nodes might have no extensions system yet.
		if InternalAPIExtensions.Valid && ExternalAPIExtensions.Valid {
			ext, err = extensions.NewExtensionRegistryFromList(strings.Split(InternalAPIExtensions.String+","+ExternalAPIExtensions.String, ","))
			if err != nil {
				return nil, err
			}
		}

		info.Extensions = ext
		results = append(results, info)
	}

	err = rows.Err()
	if err != nil {
		return nil, err
	}

	return results, nil
}
