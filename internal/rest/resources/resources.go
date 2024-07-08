package resources

import (
	"fmt"
	"path/filepath"

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
// If the Server is a core API server, its resources must not conflict with any other server, and it must not have a defined address or certificate.
func ValidateEndpoints(extensionServers []rest.Server, coreAddress string) error {
	allExistingEndpoints := []rest.Resources{UnixEndpoints, PublicEndpoints, InternalEndpoints}
	existingEndpointPaths := make(map[string]bool)
	serverAddresses := map[string]bool{coreAddress: true}

	// Record the paths for all internal endpoints.
	for _, endpoints := range allExistingEndpoints {
		for _, e := range endpoints.Endpoints {
			url := filepath.Join(string(endpoints.PathPrefix), e.Path)
			existingEndpointPaths[url] = true
		}
	}

	for _, server := range extensionServers {
		// Ensure all servers have resources.
		if len(server.Resources) == 0 {
			return fmt.Errorf("Server must have defined resources")
		}

		if server.ServeUnix && !server.CoreAPI {
			return fmt.Errorf("Cannot serve non-core API resources over the core unix socket")
		}

		if server.CoreAPI && server.Certificate != nil {
			return fmt.Errorf("Core API server cannot have a pre-defined certificate")
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
		} else if server.Protocol != "" {
			return fmt.Errorf("Server protocol defined without address")
		}

		// Ensure no endpoint path conflicts with another endpoint on the same server.
		// If a server lacks an address, we need to compare it to every other server
		// that also lacks an address, as well as the internal endpoints.
		serverPaths, err := resourcesConflict(server, existingEndpointPaths)
		if err != nil {
			return err
		}

		// Update the existing paths now that we have checked a new Server.
		for k, v := range serverPaths {
			if !existingEndpointPaths[k] {
				existingEndpointPaths[k] = v
			}
		}
	}

	return nil
}

// resourcesConflict returns an error if the endpoint paths of the given server conflict with any paths in the given map of existing paths.
func resourcesConflict(server rest.Server, existingPaths map[string]bool) (map[string]bool, error) {
	perServerPaths := map[string]bool{}
	for _, resource := range server.Resources {
		for _, endpoint := range resource.Endpoints {
			url := filepath.Join(string(resource.PathPrefix), endpoint.Path)

			// If the server uses the core API, its resources must not conflict with any existing listener.
			if server.CoreAPI {
				if existingPaths[url] || perServerPaths[url] {
					return nil, fmt.Errorf("Core endpoint %q conflicts with another endpoint", url)
				}

				perServerPaths[url] = true
			} else {
				// The server has a unique address, so only compare it to other resources in the server.
				if perServerPaths[url] {
					return nil, fmt.Errorf("Endpoint %q conflicts with another endpoint on the same server", url)
				}

				perServerPaths[url] = true
			}
		}
	}

	return perServerPaths, nil
}
