package resources

import (
	"fmt"
	"path/filepath"

	"github.com/canonical/microcluster/internal/rest/types"

	"github.com/canonical/microcluster/rest"
)

// UnixEndpoints are the endpoints available over the unix socket.
var UnixEndpoints = rest.Resources{
	Path: types.ControlEndpoint,
	Endpoints: []rest.Endpoint{
		controlCmd,
		shutdownCmd,
	},
}

// PublicEndpoints are the /cluster/1.0 API endpoints available without authentication.
var PublicEndpoints = rest.Resources{
	Path: types.PublicEndpoint,
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
	Path: types.InternalEndpoint,
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

// checkDuplicateEndpoints checks if any endpoints defined in extensionServers conflict with internal endpoints.
func checkDuplicateEndpoints(extensionServerEndpoints rest.Resources) error {
	allExistingEndpoints := []rest.Resources{UnixEndpoints, PublicEndpoints, InternalEndpoints}
	existingEndpointPaths := make(map[string]bool)

	for _, endpoints := range allExistingEndpoints {
		for _, e := range endpoints.Endpoints {
			url := filepath.Join(string(endpoints.Path), e.Path)
			existingEndpointPaths[url] = true
		}
	}

	for _, e := range extensionServerEndpoints.Endpoints {
		url := filepath.Join(string(extensionServerEndpoints.Path), e.Path)
		if existingEndpointPaths[url] {
			return fmt.Errorf("Endpoint %q conflicts with internal endpoint", url)
		}
	}

	return nil
}

// GetCoreEndpoints extracts all endpoints from extensionServers that should be allocated to the core listener.
// It also ensures that there are no conflicts between endpoints from extensionServers and internal endpoints.
func GetCoreEndpoints(extensionServers []rest.Server) ([]rest.Resources, error) {
	var coreEndpoints []rest.Resources
	for _, extensionServer := range extensionServers {
		if !extensionServer.CoreAPI {
			continue
		}

		err := extensionServer.ValidateServerConfigs()
		if err != nil {
			return nil, err
		}

		for _, endpoints := range extensionServer.Resources {
			err = checkDuplicateEndpoints(endpoints)
			if err != nil {
				return nil, err
			}

			coreEndpoints = append(coreEndpoints, endpoints)
		}
	}

	return coreEndpoints, nil
}
