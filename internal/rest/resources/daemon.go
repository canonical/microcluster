package resources

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"

	"github.com/canonical/microcluster/v3/internal/rest/access"
	internalClient "github.com/canonical/microcluster/v3/internal/rest/client"
	internalState "github.com/canonical/microcluster/v3/internal/state"
	"github.com/canonical/microcluster/v3/microcluster/types"
)

var daemonServersCmd = types.Endpoint{
	Path: "daemon/servers",

	Get: types.EndpointAction{Handler: daemonServersGet, AccessHandler: access.AllowAuthenticated},
	Put: types.EndpointAction{Handler: daemonServersPut, AccessHandler: access.AllowAuthenticated},
}

func daemonServersGet(s types.State, r *http.Request) types.Response {
	intState, err := internalState.ToInternal(s)
	if err != nil {
		return types.SmartError(err)
	}

	return types.SyncResponse(true, intState.LocalConfig().GetServers())
}

func daemonServersPut(s types.State, r *http.Request) types.Response {
	req := make(map[string]types.ServerConfig)

	// Parse the request.
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return types.BadRequest(err)
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
			return types.BadRequest(fmt.Errorf("No matching additional listener found for %q", serverName))
		}
	}

	// Validate if there is an address conflict.
	// Initialize the list of active server addresses with the server's address.
	var serverAddresses = []string{s.Address().Host}
	for _, server := range req {
		serverAddress := server.Address.String()

		if slices.Contains(serverAddresses, serverAddress) {
			return types.BadRequest(fmt.Errorf("Address %q is already in use", serverAddress))
		}

		serverAddresses = append(serverAddresses, serverAddress)
	}

	intState, err := internalState.ToInternal(s)
	if err != nil {
		return types.SmartError(err)
	}

	daemonConfig := intState.LocalConfig()
	daemonConfig.SetServers(req)

	// Persist the configuration changes to file.
	err = daemonConfig.Write()
	if err != nil {
		return types.SmartError(err)
	}

	// Update the additional listeners.
	err = intState.UpdateServers()
	if err != nil {
		return types.SmartError(err)
	}

	clients, err := s.Connect().Cluster(false)
	if err != nil {
		return types.SmartError(err)
	}

	// Run the OnDaemonConfigUpdate hook on all other members.
	remotes := intState.InternalRemotes()
	err = clients.Query(r.Context(), true, func(ctx context.Context, c types.Client) error {
		c.SetClusterNotification()
		addrPort, err := types.ParseAddrPort(c.URL().Host)
		if err != nil {
			return err
		}

		remote := remotes.RemoteByAddress(addrPort)
		if remote == nil {
			return fmt.Errorf("No remote found at address %q to run the %q hook", c.URL().Host, types.OnDaemonConfigUpdate)
		}

		return internalClient.RunOnDaemonConfigUpdateHook(ctx, c.UseTarget(remote.Name), daemonConfig.Dump())
	})
	if err != nil {
		return types.SmartError(err)
	}

	return types.EmptySyncResponse
}
