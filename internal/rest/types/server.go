package types

import (
	"github.com/canonical/microcluster/rest/types"
)

// Server represents server status information.
type Server struct {
	Name    string         `json:"name"    yaml:"name"`
	Address types.AddrPort `json:"address" yaml:"address"`
	Ready   bool           `json:"ready"   yaml:"ready"`
}

const (
	// ExtendedEndpoint - All endpoints added managed by external usage of MicroCluster.
	ExtendedEndpoint types.EndpointPrefix = "1.0"

	// PublicEndpoint - Internally managed APIs available without authentication.
	PublicEndpoint types.EndpointPrefix = "cluster/1.0"

	// InternalEndpoint - all endpoints restricted to trusted servers.
	InternalEndpoint types.EndpointPrefix = "cluster/internal"

	// ControlEndpoint - all endpoints available on the local unix socket.
	ControlEndpoint types.EndpointPrefix = "cluster/control"
)
