package resources

import (
	"github.com/canonical/microcluster/internal/rest/client"
	"github.com/canonical/microcluster/rest"
)

// UnixEndpoints are the endpoints available over the unix socket.
var UnixEndpoints = rest.Resources{
	Path: rest.EndpointType(client.ControlEndpoint),
	Endpoints: []rest.Endpoint{
		controlCmd,
		shutdownCmd,
	},
}

// PublicEndpoints are the /cluster/1.0 API endpoints available without authentication.
var PublicEndpoints = rest.Resources{
	Path: rest.EndpointType(client.PublicEndpoint),
	Endpoints: []rest.Endpoint{
		api10Cmd,
		clusterCmd,
		clusterMemberCmd,
		tokensCmd,
		readyCmd,
	},
}

// InternalEndpoints are the /cluster/internal API endpoints available at the listen address.
var InternalEndpoints = rest.Resources{
	Path: rest.EndpointType(client.InternalEndpoint),
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
var ExtendedEndpoints = rest.Resources{
	Path:      rest.EndpointType(client.ExtendedEndpoint),
	Endpoints: []rest.Endpoint{},
}
