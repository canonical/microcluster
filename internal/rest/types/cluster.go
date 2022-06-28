package types

import "time"

// ClusterMember represents information about a dqlite cluster member.
type ClusterMember struct {
	ClusterMemberLocal
	Role          string       `json:"role" yaml:"role"`
	SchemaVersion int          `json:"schema_version" yaml:"schema_version"`
	LastHeartbeat time.Time    `json:"last_heartbeat" yaml:"last_heartbeat"`
	Status        MemberStatus `json:"status" yaml:"status"`
}

// ClusterMemberLocal represents local information about a new cluster member.
type ClusterMemberLocal struct {
	Name        string          `json:"name" yaml:"name"`
	Address     AddrPort        `json:"address" yaml:"address"`
	Certificate X509Certificate `json:"certificate" yaml:"certificate"`
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
