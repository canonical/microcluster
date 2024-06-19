package resources

import (
	"fmt"
	"path/filepath"

	"github.com/canonical/microcluster/internal/rest/types"
	"github.com/canonical/microcluster/rest"
)

// UnixEndpoints are the endpoints available over the unix socket.
var UnixEndpoints = rest.Resources{
	PathPrefix: types.ControlEndpoint,
	Endpoints: []rest.Endpoint{
		controlCmd,
		shutdownCmd,
	},
}

// PublicEndpoints are the /cluster/1.0 API endpoints available without authentication.
var PublicEndpoints = rest.Resources{
	PathPrefix: types.PublicEndpoint,
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
	PathPrefix: types.InternalEndpoint,
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

// checkInternalEndpointsConflict checks if any endpoints defined in extensionServers conflict with internal endpoints.
func checkInternalEndpointsConflict(extensionServerEndpoints rest.Resources) error {
	allExistingEndpoints := []rest.Resources{UnixEndpoints, PublicEndpoints, InternalEndpoints}
	existingEndpointPaths := make(map[string]bool)

	for _, endpoints := range allExistingEndpoints {
		for _, e := range endpoints.Endpoints {
			url := filepath.Join(string(endpoints.PathPrefix), e.Path)
			existingEndpointPaths[url] = true
		}
	}

	for _, e := range extensionServerEndpoints.Endpoints {
		url := filepath.Join(string(extensionServerEndpoints.PathPrefix), e.Path)
		if existingEndpointPaths[url] {
			return fmt.Errorf("Endpoint %q conflicts with internal endpoint", url)
		}
	}

	return nil
}

// GetAndValidateCoreEndpoints extracts all endpoints from extensionServers that should be allocated to the core listener.
// It also performs the following validations:
// 1. Only one core API server is allowed.
// 2. Server configurations are properly set.
// 3. Path prefixes for endpoints belonging to a single server are not duplicated
// 4. Enpoints defined in extensionServers do not conflict with internal endpoints.
func GetAndValidateCoreEndpoints(extensionServers []rest.Server) ([]rest.Resources, error) {
	var coreEndpoints []rest.Resources
	var numCoreAPIs int

	for _, extensionServer := range extensionServers {
		if !extensionServer.CoreAPI {
			continue
		}

		numCoreAPIs++
		if numCoreAPIs > 1 {
			return nil, fmt.Errorf("Only one core API server is allowed")
		}

		err := extensionServer.ValidateServerConfigs()
		if err != nil {
			return nil, err
		}

		seen := make(map[string]bool)
		for _, endpoints := range extensionServer.Resources {
			if seen[string(endpoints.PathPrefix)] {
				return nil, fmt.Errorf("Path prefix %q is duplicated in server configuration", endpoints.PathPrefix)
			}

			err = checkInternalEndpointsConflict(endpoints)
			if err != nil {
				return nil, err
			}

			coreEndpoints = append(coreEndpoints, endpoints)
			seen[string(endpoints.PathPrefix)] = true
		}
	}

	return coreEndpoints, nil
}
