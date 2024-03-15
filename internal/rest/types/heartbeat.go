package types

// HeartbeatInfo represents information about the cluster sent out by the leader of the cluster to other members.
// If BeginRound is set, a new heartbeat will initiate.
type HeartbeatInfo struct {
	BeginRound        bool                     `json:"begin_round" yaml:"begin_round"`
	MaxSchemaInternal uint64                   `json:"max_schema_internal" yaml:"max_schema_internal"`
	MaxSchemaExternal uint64                   `json:"max_schema_external" yaml:"max_schema_external"`
	ClusterMembers    map[string]ClusterMember `json:"cluster_members" yaml:"cluster_members"`
}
