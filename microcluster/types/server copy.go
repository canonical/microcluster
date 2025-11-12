package types

// Server represents server status information.
type Server struct {
	Name       string     `json:"name"    yaml:"name"`
	Address    AddrPort   `json:"address" yaml:"address"`
	Version    string     `json:"version" yaml:"version"`
	Ready      bool       `json:"ready"   yaml:"ready"`
	Extensions Extensions `json:"extensions" yaml:"extensions"`
}

const (
	// PublicEndpoint - Internally managed APIs.
	PublicEndpoint EndpointPrefix = "core/1.0"

	// InternalEndpoint - All internal endpoints restricted to trusted servers.
	InternalEndpoint EndpointPrefix = "core/internal"

	// ControlEndpoint - All internal endpoints available on the local unix socket.
	ControlEndpoint EndpointPrefix = "core/control"
)
