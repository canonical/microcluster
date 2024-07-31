package resources

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/shared"

	"github.com/canonical/microcluster/v2/client"
	internalClient "github.com/canonical/microcluster/v2/internal/rest/client"
	internalTypes "github.com/canonical/microcluster/v2/internal/rest/types"
	internalState "github.com/canonical/microcluster/v2/internal/state"
	"github.com/canonical/microcluster/v2/rest"
	"github.com/canonical/microcluster/v2/rest/access"
	"github.com/canonical/microcluster/v2/rest/types"
	"github.com/canonical/microcluster/v2/state"
)

var daemonCmd = rest.Endpoint{
	Path: "daemon/servers",

	Get: rest.EndpointAction{Handler: daemonServersGet, AccessHandler: access.AllowAuthenticated},
	Put: rest.EndpointAction{Handler: daemonServersPut, AccessHandler: access.AllowAuthenticated},
}

func daemonServersGet(s state.State, r *http.Request) response.Response {
	intState, err := internalState.ToInternal(s)
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponse(true, intState.LocalConfig().GetServers())
}

func daemonServersPut(s state.State, r *http.Request) response.Response {
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

	intState, err := internalState.ToInternal(s)
	if err != nil {
		return response.SmartError(err)
	}

	daemonConfig := intState.LocalConfig()
	daemonConfig.SetServers(req)

	// Persist the configuration changes to file.
	err = daemonConfig.Write()
	if err != nil {
		return response.SmartError(err)
	}

	// Update the additional listeners.
	err = intState.UpdateServers()
	if err != nil {
		return response.SmartError(err)
	}

	cluster, err := s.Cluster(false)
	if err != nil {
		return response.SmartError(err)
	}

	// Run the OnDaemonConfigUpdate hook on all other members.
	remotes := s.Remotes()
	err = cluster.Query(r.Context(), true, func(ctx context.Context, c *client.Client) error {
		c.SetClusterNotification()
		addrPort, err := types.ParseAddrPort(c.URL().URL.Host)
		if err != nil {
			return err
		}

		remote := remotes.RemoteByAddress(addrPort)
		if remote == nil {
			return fmt.Errorf("No remote found at address %q to run the %q hook", c.URL().URL.Host, internalTypes.OnDaemonConfigUpdate)
		}

		return internalClient.RunOnDaemonConfigUpdateHook(ctx, c.Client.UseTarget(remote.Name), daemonConfig.Dump())
	})
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}
