package types

import (
	"time"

	"github.com/canonical/microcluster/rest/types"
)

// ClusterMember represents information about a dqlite cluster member.
//
// swagger:model
type ClusterMember struct {
	ClusterMemberLocal

	// Role of the cluster in dqlite.
	// Example: voter
	Role string `json:"role" yaml:"role"`

	// Schema version of the cluster member's database.
	// Example: 4
	SchemaVersion int `json:"schema_version" yaml:"schema_version"`

	// Time of the last heartbeat.
	// Example: 2024-04-11T00:00:00-00:00
	LastHeartbeat time.Time `json:"last_heartbeat" yaml:"last_heartbeat"`

	// Connected status of the cluster member.
	// Example: ONLINE
	Status MemberStatus `json:"status" yaml:"status"`

	// Secret used for joining the cluster.
	// Example: string
	Secret string `json:"secret" yaml:"secret"`
}

// ClusterMemberLocal represents local information about a new cluster member.
type ClusterMemberLocal struct {
	// Name of the cluster member.
	// Example: server01
	Name string `json:"name" yaml:"name"`

	// Address of the cluster member.
	// Example: 127.0.0.1:9000
	Address types.AddrPort `json:"address" yaml:"address"`

	// Certificate of the cluster member.
	// Example: X509 PEM certificate
	Certificate types.X509Certificate `json:"certificate" yaml:"certificate"`
}

// MemberStatus represents the online status of a cluster member.
type MemberStatus string

const (
	// MemberOnline should be the MemberStatus when the node is online and reachable.
	MemberOnline MemberStatus = "ONLINE"

	// MemberUnreachable should be the MemberStatus when we were not able to connect to the node.
	MemberUnreachable MemberStatus = "UNREACHABLE"

	// MemberNotTrusted should be the MemberStatus when there is no local yaml entry for this node.
	MemberNotTrusted MemberStatus = "NOT TRUSTED"

	// MemberNotFound should be the MemberStatus when the node was not found in dqlite.
	MemberNotFound MemberStatus = "NOT FOUND"
)
