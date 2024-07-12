package resources

import (
	"fmt"
	"path/filepath"

	"github.com/canonical/microcluster/internal/endpoints"
	internalTypes "github.com/canonical/microcluster/internal/rest/types"
	"github.com/canonical/microcluster/rest"
	"github.com/canonical/microcluster/rest/types"
)

// UnixEndpoints are the endpoints available over the unix socket.
var UnixEndpoints = rest.Resources{
	PathPrefix: internalTypes.ControlEndpoint,
	Endpoints: []rest.Endpoint{
		controlCmd,
		shutdownCmd,
	},
}

// PublicEndpoints are the /cluster/1.0 API endpoints available without authentication.
var PublicEndpoints = rest.Resources{
	PathPrefix: internalTypes.PublicEndpoint,
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
	PathPrefix: internalTypes.InternalEndpoint,
	Endpoints: []rest.Endpoint{
		databaseCmd,
		clusterCertificatesCmd,
		sqlCmd,
		tokenCmd,
		heartbeatCmd,
		trustCmd,
		trustEntryCmd,
		hooksCmd,
		daemonCmd,
	},
}

// ValidateEndpoints checks if any endpoints defined in extensionServers conflict with other endpoints.
// An invalid server is defined as one of the following:
// - The PathPrefix+Path of an endpoint conflicts with another endpoint in the same server.
// - The address of the server clashes with another server.
// - The server does not have defined resources.
// If the Server is a core API server, its resources must not conflict with any other core API server, and it must not have a defined address or certificate.
func ValidateEndpoints(extensionServers map[string]rest.Server, coreAddress string) error {
	serverAddresses := map[string]bool{coreAddress: true}
	baseCoreEndpoints := []rest.Resources{UnixEndpoints, PublicEndpoints, InternalEndpoints}
	existingEndpointPaths := map[string]map[string]bool{endpoints.EndpointsCore: {}}

	// Record the paths for all internal endpoints on the core listener.
	for _, existing := range baseCoreEndpoints {
		for _, e := range existing.Endpoints {
			url := filepath.Join(string(existing.PathPrefix), e.Path)
			existingEndpointPaths[endpoints.EndpointsCore][url] = true
		}
	}

	for serverName, server := range extensionServers {
		// Ensure all servers have resources.
		if len(server.Resources) == 0 {
			return fmt.Errorf("Server must have defined resources")
		}

		if server.ServeUnix && !server.CoreAPI {
			return fmt.Errorf("Cannot serve non-core API resources over the core unix socket")
		}

		if server.CoreAPI && server.DedicatedCertificate {
			return fmt.Errorf("Core API server cannot have a dedicated certificate")
		}

		if server.CoreAPI && server.Address != (types.AddrPort{}) {
			return fmt.Errorf("Core API server cannot have a pre-defined address")
		}

		// Ensure all servers with a defined address are unique.
		if server.Address != (types.AddrPort{}) {
			if serverAddresses[server.Address.String()] {
				return fmt.Errorf("Server address %q conflicts with another Server", server.Address.String())
			}

			serverAddresses[server.Address.String()] = true
		}

		// Ensure no endpoint path conflicts with another endpoint on the same server.
		// If a server is an extension to the core API, it will be compared against all core API paths.
		for _, resource := range server.Resources {
			for _, e := range resource.Endpoints {
				url := filepath.Join(string(resource.PathPrefix), e.Path)

				// Internally, core API servers will be lumped together with the "core" server so
				// record them in the map under that name. Otherwise, use the given server name.
				internalName := serverName
				if server.CoreAPI {
					internalName = endpoints.EndpointsCore
				}

				_, ok := existingEndpointPaths[internalName]
				if !ok {
					existingEndpointPaths[internalName] = map[string]bool{}
				}

				if existingEndpointPaths[internalName][url] {
					return fmt.Errorf("Endpoint %q from server %q conflicts with another endpoint from server %q", url, serverName, internalName)
				}

				existingEndpointPaths[internalName][url] = true
			}
		}
	}

	return nil
}
