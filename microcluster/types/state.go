package types

import (
	"net/url"

	"github.com/canonical/lxd/shared"
)

// State exposes the internal daemon state for use with extended API handlers.
type State interface {
	// Listen Address.
	Address() *url.URL

	// Name of the cluster member.
	Name() string

	// Version is provided by the MicroCluster consumer.
	Version() string

	// Server certificate is used for server-to-server connection.
	ServerCert() *shared.CertInfo

	// Cluster certificate is used for downstream connections within a cluster.
	ClusterCert() *shared.CertInfo

	// HasExtension returns whether the given API extension is supported.
	HasExtension(ext string) bool

	// ExtensionServers returns an immutable list of the daemon's additional listeners.
	ExtensionServers() []string

	// FileSystem structure.
	FileSystem() OS

	// Database.
	Database() DB

	// Returns a connector for interconnection with the cluster.
	Connect() Connector
}
