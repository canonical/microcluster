package cluster

import (
	"fmt"

	internalTypes "github.com/canonical/microcluster/internal/rest/types"
	"github.com/canonical/microcluster/rest/types"
)

//go:generate -command mapper lxd-generate db mapper -t non_cluster_members.mapper.go
//go:generate mapper reset
//
//go:generate mapper stmt -e internal_non_cluster_member objects table=internal_non_cluster_members
//go:generate mapper stmt -e internal_non_cluster_member objects-by-Address table=internal_non_cluster_members
//go:generate mapper stmt -e internal_non_cluster_member objects-by-Name table=internal_non_cluster_members
//go:generate mapper stmt -e internal_non_cluster_member id table=internal_non_cluster_members
//go:generate mapper stmt -e internal_non_cluster_member create table=internal_non_cluster_members
//go:generate mapper stmt -e internal_non_cluster_member delete-by-Address table=internal_non_cluster_members
//go:generate mapper stmt -e internal_non_cluster_member update table=internal_non_cluster_members
//
//go:generate mapper method -i -e internal_non_cluster_member GetMany table=internal_non_cluster_members
//go:generate mapper method -i -e internal_non_cluster_member GetOne table=internal_non_cluster_members
//go:generate mapper method -i -e internal_non_cluster_member ID table=internal_non_cluster_members
//go:generate mapper method -i -e internal_non_cluster_member Exists table=internal_non_cluster_members
//go:generate mapper method -i -e internal_non_cluster_member Create table=internal_non_cluster_members
//go:generate mapper method -i -e internal_non_cluster_member DeleteOne-by-Address table=internal_non_cluster_members
//go:generate mapper method -i -e internal_non_cluster_member Update table=internal_non_cluster_members

// InternalNonClusterMember represents the global database entry for a non-cluster member.
type InternalNonClusterMember struct {
	ID          int
	Name        string `db:"primary=yes"`
	Address     string
	Certificate string
}

// InternalNonClusterMemberFilter is used for filtering queries using generated methods.
type InternalNonClusterMemberFilter struct {
	Address *string
	Name    *string
}

// ToAPI returns the api struct for a NonClusterMember database entity.
func (c InternalNonClusterMember) ToAPI() (*internalTypes.ClusterMemberLocal, error) {
	address, err := types.ParseAddrPort(c.Address)
	if err != nil {
		return nil, fmt.Errorf("Failed to parse address %q of database cluster member: %w", c.Address, err)
	}

	certificate, err := types.ParseX509Certificate(c.Certificate)
	if err != nil {
		return nil, fmt.Errorf("Failed to parse certificate of database cluster member with address %q: %w", c.Address, err)
	}

	return &internalTypes.ClusterMemberLocal{
		Name:        c.Name,
		Address:     address,
		Certificate: *certificate,
	}, nil
}
