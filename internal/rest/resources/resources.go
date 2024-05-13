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

// UnixEndpoints are the endpoints available over the unix socket.
var UnixEndpoints = &Resources{
	Path: client.ControlEndpoint,
	Endpoints: []rest.Endpoint{
		controlCmd,
		shutdownCmd,
	},
}

// PublicEndpoints are the /cluster/1.0 API endpoints available without authentication.
var PublicEndpoints = &Resources{
	Path: client.PublicEndpoint,
	Endpoints: []rest.Endpoint{
		api10Cmd,
		clusterCmd,
		clusterMemberCmd,
		tokensCmd,
		readyCmd,
	},
}

// InternalEndpoints are the /cluster/internal API endpoints available at the listen address.
var InternalEndpoints = &Resources{
	Path: client.InternalEndpoint,
	Endpoints: []rest.Endpoint{
		databaseCmd,
		clusterCertificatesCmd,
		sqlCmd,
		tokenCmd,
		heartbeatCmd,
		trustCmd,
		trustEntryCmd,
		hooksCmd,
	},
}

// ExtendedEndpoints holds all the endpoints added by external usage of MicroCluster.
var ExtendedEndpoints = &Resources{
	Path:      client.ExtendedEndpoint,
	Endpoints: []rest.Endpoint{},
}
