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

// ExtendedEndpoints holds all the endpoints added by external usage of MicroCluster.
var ExtendedEndpoints = rest.Resources{
	Path:      types.ExtendedEndpoint,
	Endpoints: []rest.Endpoint{},
}

// checkDuplicateEndpoints checks if any endpoints defined in extensionServers conflict with internal endpoints.
func checkDuplicateEndpoints(extensionServerEndpoints rest.Resources, extendedEndpoints rest.Resources) error {
	allExistingEndpoints := []rest.Resources{UnixEndpoints, PublicEndpoints, InternalEndpoints, extendedEndpoints}
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

// MergeExtendedEndpoints merges the endpoints from extensionServers with endpoints defined in extendedEndpoints.
// It also ensures that there are no conflicts between endpoints from extensionServers and internal endpoints.
func MergeExtendedEndpoints(extensionServers []rest.Server, extendedEndpoints rest.Resources) ([]rest.Resources, error) {
	mergedEndpoints := []rest.Resources{extendedEndpoints}
	for _, extensionServer := range extensionServers {
		if !extensionServer.CoreAPI {
			continue
		}

		for _, endpoints := range extensionServer.Resources {
			err := checkDuplicateEndpoints(endpoints, extendedEndpoints)
			if err != nil {
				return nil, err
			}

			mergedEndpoints = append(mergedEndpoints, endpoints)
		}
	}

	return mergedEndpoints, nil
}
