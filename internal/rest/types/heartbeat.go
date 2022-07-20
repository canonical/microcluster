package types

// HeartbeatInfo represents information about the cluster sent out by the leader of the cluster to other members.
// If BeginRound is set, a new heartbeat will initiate.
type HeartbeatInfo struct {
	BeginRound     bool                     `json:"begin_round" yaml:"begin_round"`
	MaxSchema      int                      `json:"max_schema" yaml:"max_schema"`
	ClusterMembers map[string]ClusterMember `json:"cluster_members" yaml:"cluster_members"`
}
