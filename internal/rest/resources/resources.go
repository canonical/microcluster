package resources

import (
	"github.com/canonical/microcluster/internal/rest/client"
	"github.com/canonical/microcluster/rest"
)

// Resources represents all the resources served over the same path.
type Resources struct {
	Path      client.EndpointType
	Endpoints []rest.Endpoint
}

// ControlEndpoints are the endpoints available over the unix socket.
var ControlEndpoints = &Resources{
	Path: client.ControlEndpoint,
	Endpoints: []rest.Endpoint{
		controlCmd,
		sqlCmd,
		readyCmd,
		tokensCmd,
		tokenCmd,
		clusterCmd,
		heartbeatCmd,
    shutdownCmd,
	},
}

// PublicEndpoints are the /cluster/1.0 API endpoints available without authentication.
var PublicEndpoints = &Resources{
	Path: client.PublicEndpoint,
	Endpoints: []rest.Endpoint{
		clusterCmd,
		tokensCmd,
		readyCmd,
	},
}

// InternalEndpoints are the /cluster/internal API endpoints available at the listen address.
var InternalEndpoints = &Resources{
	Path: client.InternalEndpoint,
	Endpoints: []rest.Endpoint{
		databaseCmd,
		sqlCmd,
		tokenCmd,
		heartbeatCmd,
	},
}

// ExtendedEndpoints holds all the endpoints added by external usage of MicroCluster.
var ExtendedEndpoints = &Resources{
	Path:      client.ExtendedEndpoint,
	Endpoints: []rest.Endpoint{},
}
