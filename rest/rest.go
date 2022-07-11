package rest

import (
	"net/http"

	"github.com/lxc/lxd/lxd/response"

	"github.com/canonical/microcluster/state"
)

// EndpointAlias represents an alias URL of and Endpoint in our API.
type EndpointAlias struct {
	Name string // Name for this alias.
	Path string // Path pattern for this alias.
}

// EndpointAction represents an action on an API endpoint.
type EndpointAction struct {
	Handler        func(state *state.State, r *http.Request) response.Response
	AccessHandler  func(state *state.State, r *http.Request) response.Response
	AllowUntrusted bool
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
}
