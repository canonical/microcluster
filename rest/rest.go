package rest

import (
	"net/http"

	"github.com/canonical/lxd/lxd/response"

	"github.com/canonical/microcluster/v2/rest/types"
	"github.com/canonical/microcluster/v2/state"
)

// EndpointAlias represents an alias URL of and Endpoint in our API.
type EndpointAlias struct {
	Name string // Name for this alias.
	Path string // Path pattern for this alias.
}

// EndpointAction represents an action on an API endpoint.
type EndpointAction struct {
	Handler        func(state state.State, r *http.Request) response.Response
	AccessHandler  func(state state.State, r *http.Request) (trusted bool, resp response.Response)
	AllowUntrusted bool
	ProxyTarget    bool // Allow forwarding of the request to a target if ?target=name is specified.
}

// Endpoint represents a URL in our API.
type Endpoint struct {
	Name    string          // Name for this endpoint.
	Path    string          // Path pattern for this endpoint.
	Aliases []EndpointAlias // Any aliases for this endpoint.
	Get     EndpointAction
	Put     EndpointAction
	Post    EndpointAction
	Delete  EndpointAction
	Patch   EndpointAction

	AllowedDuringShutdown bool // Whether we should return Unavailable Error (503) if daemon is shutting down.
	AllowedBeforeInit     bool // Whether we should return Unavailabel Error (503) if the daemon has not been initialized (is not yet part of a cluster).
}

// Resources represents all the resources served over the same path.
type Resources struct {
	PathPrefix types.EndpointPrefix
	Endpoints  []Endpoint
}

// Server contains configuration and handlers for additional listeners to be instantiated after app startup.
type Server struct {
	types.ServerConfig

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
}
