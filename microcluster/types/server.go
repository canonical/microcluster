package types

import "github.com/canonical/microcluster/v3/internal/extensions"

const (
	// PublicEndpoint - Internally managed APIs.
	PublicEndpoint EndpointPrefix = "core/1.0"

	// InternalEndpoint - All internal endpoints restricted to trusted servers.
	InternalEndpoint EndpointPrefix = "core/internal"

	// ControlEndpoint - All internal endpoints available on the local unix socket.
	ControlEndpoint EndpointPrefix = "core/control"
)

// ServerConfig represents the mutable fields of an additional network listener.
type ServerConfig struct {
	// Address is the server listen address.
	// Example: 127.0.0.1:9000
	Address AddrPort `json:"address" yaml:"address"`
}

// Server represents server status information.
type Server struct {
	Name       string                `json:"name"    yaml:"name"`
	Address    AddrPort              `json:"address" yaml:"address"`
	Version    string                `json:"version" yaml:"version"`
	Ready      bool                  `json:"ready"   yaml:"ready"`
	Extensions extensions.Extensions `json:"extensions" yaml:"extensions"`
}
