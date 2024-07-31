package types

import (
	"github.com/canonical/microcluster/v2/rest/types"
)

// HeartbeatInfo represents information about the cluster sent out by the leader of the cluster to other members.
// If BeginRound is set, a new heartbeat will initiate.
type HeartbeatInfo struct {
	BeginRound        bool                           `json:"begin_round"         yaml:"begin_round"`
	MaxSchemaInternal uint64                         `json:"max_schema_internal" yaml:"max_schema_internal"`
	MaxSchemaExternal uint64                         `json:"max_schema_external" yaml:"max_schema_external"`
	ClusterMembers    map[string]types.ClusterMember `json:"cluster_members"     yaml:"cluster_members"`
	LeaderAddress     string                         `json:"leader_address"      yaml:"leader_address"`
	DqliteRoles       map[string]string              `json:"dqlite_roles"        yaml:"dqlite_roles"`
}
