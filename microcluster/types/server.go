package types

import "time"

// ServerConfig represents the mutable fields of an additional network listener.
type ServerConfig struct {
	// Address is the server listen address.
	// Example: 127.0.0.1:9000
	Address AddrPort `json:"address" yaml:"address"`
}

// Server contains configuration and handlers for additional listeners to be instantiated after app startup.
type Server struct {
	ServerConfig

	// CoreAPI determines whether the the resources of the server should be served over the default cluster API.
	CoreAPI bool

	// PreInit determines whether the Server should be available prior to initializing the daemon.
	PreInit bool

	// ServeUnix sets whether the resources of this endpoint should also be served over the unix socket.
	ServeUnix bool

	// DedicatedCertificate sets whether the additional listener should use its own self signed certificate.
	// If false it tries to use a custom certificate from the daemon's state `/certificates` directory
	// based on the name provided when creating the server.
	// In case there isn't any custom certificate it falls back to the cluster certificate of the core API.
	DedicatedCertificate bool

	// Resources is the list of resources offered by this server.
	Resources []Resources

	// DrainConnectionsTimeout is the amount of time to allow for all connections to drain when shutting down.
	// If it's 0, the connections are not drained when shutting down.
	DrainConnectionsTimeout time.Duration
}
