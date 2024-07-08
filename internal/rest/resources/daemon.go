package resources

import (
	"encoding/json"
	"net/http"

	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/shared"

	"github.com/canonical/microcluster/rest"
	"github.com/canonical/microcluster/rest/access"
	"github.com/canonical/microcluster/rest/types"
	"github.com/canonical/microcluster/state"
)

var daemonCmd = rest.Endpoint{
	Path: "daemon/servers",

	Get: rest.EndpointAction{Handler: daemonServersGet, AccessHandler: access.AllowAuthenticated},
	Put: rest.EndpointAction{Handler: daemonServersPut, AccessHandler: access.AllowAuthenticated},
}

func daemonServersGet(s *state.State, r *http.Request) response.Response {
	return response.SyncResponse(true, s.LocalConfig().GetServers())
}

func daemonServersPut(s *state.State, r *http.Request) response.Response {
	req := make(map[string]types.ServerConfig)

	// Parse the request.
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check if an additional listener exists for that name.
	for serverName := range req {
		found := false
		for _, name := range s.ExtensionServers() {
			if name == serverName {
				found = true
			}
		}

		if !found {
			return response.BadRequest(fmt.Errorf("No matching additional listener found for %q", serverName))
		}
	}

	// Validate if there is an address conflict.
	// Initialize the list of active server addresses with the server's address.
	var serverAddresses = []string{s.Address().URL.Host}
	for _, server := range req {
		serverAddress := server.Address.String()

		if shared.ValueInSlice(serverAddress, serverAddresses) {
			return response.BadRequest(fmt.Errorf("Address %q is already in use", serverAddress))
		}

		serverAddresses = append(serverAddresses, serverAddress)
	}

	daemonConfig := s.LocalConfig()
	daemonConfig.SetServers(req)

	// Persist the configuration changes to file.
	err = daemonConfig.Write()
	if err != nil {
		return response.SmartError(err)
	}

	// Update the additional listeners.
	err = s.UpdateServers()
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}
