package resources

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/canonical/microcluster/v3/internal/endpoints"
	internalTypes "github.com/canonical/microcluster/v3/internal/rest/types"
	"github.com/canonical/microcluster/v3/rest"
	"github.com/canonical/microcluster/v3/rest/types"
)

// UnixEndpoints are the endpoints available over the unix socket.
var UnixEndpoints = rest.Resources{
	PathPrefix: internalTypes.ControlEndpoint,
	Endpoints: []rest.Endpoint{
		controlCmd,
		shutdownCmd,
		tokensCmd,
	},
}

// PublicEndpoints are the /core/1.0 API endpoints available at the listen address.
var PublicEndpoints = rest.Resources{
	PathPrefix: internalTypes.PublicEndpoint,
	Endpoints: []rest.Endpoint{
		api10Cmd,
		clusterCertificatesCmd,
		clusterCmd,
		clusterMemberCmd,
		daemonCmd,
		tokenCmd,
		readyCmd,
	},
}

// InternalEndpoints are the /core/internal API endpoints available at the listen address.
var InternalEndpoints = rest.Resources{
	PathPrefix: internalTypes.InternalEndpoint,
	Endpoints: []rest.Endpoint{
		clusterInternalCmd,
		clusterMemberInternalCmd,
		databaseCmd,
		sqlCmd,
		heartbeatCmd,
		trustCmd,
		trustEntryCmd,
		hooksCmd,
	},
}

// ValidateEndpoints checks if any endpoints defined in extensionServers conflict with other endpoints.
// An invalid server is defined as one of the following:
// - The PathPrefix+Path of an endpoint conflicts with another endpoint in the same server.
// - The address of the server clashes with another server.
// - The server does not have defined resources.
// - If the Server is a core API server:
//   - The PathPrefix+Path of an endpoint must not begin with `core`.
//   - Its resources must not conflict with any other core API server.
//   - It must not have a defined address or certificate.
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

					// Reserve the /core namespace for microcluster
					if firstPathElement(url) == endpoints.EndpointsCore {
						return fmt.Errorf("Endpoint from server %q conflicts with reserved namespace \"/%s\"", serverName, endpoints.EndpointsCore)
					}
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

// "core/"   -> "core"
// "/core"   -> "core"
// "/core/x" -> "core"
// "/"       -> ""
// Returns the first non-root path element in `path`.
func firstPathElement(path string) string {
	parts := strings.SplitN(path, "/", 3)
	switch len(parts) {
	case 0:
		// Shouldn't be possible
		return path
	case 1:
		return parts[0]
	default:
		if parts[0] == "" {
			// "/core"
			return parts[1]
		} else {
			// "core/"
			return parts[0]
		}
	}
}
