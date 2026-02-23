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

	intState, err := internalState.ToInternal(s)
	if err != nil {
		return types.SmartError(err)
	}

	err = validateServerConfigs(intState, req)
	if err != nil {
		return types.BadRequest(err)
	}

	daemonConfig := intState.LocalConfig()
	daemonConfig.SetServers(req)

	err = applyAndNotifyDaemonConfig(r.Context(), intState)
	if err != nil {
		return types.SmartError(err)
	}

	return types.EmptySyncResponse
}

// applyAndNotifyDaemonConfig persists the current daemon configuration to file,
// updates additional listeners, and notifies all cluster members by running
// the OnDaemonConfigUpdate hook.
func applyAndNotifyDaemonConfig(ctx context.Context, intState *internalState.InternalState) error {
	daemonConfig := intState.LocalConfig()

	// Persist the configuration changes to file.
	err := daemonConfig.Write()
	if err != nil {
		return err
	}

	// Update the additional listeners.
	err = intState.UpdateServers()
	if err != nil {
		return err
	}

	clients, err := intState.Connect().Cluster(false)
	if err != nil {
		return err
	}

	// Run the OnDaemonConfigUpdate hook on all other members.
	remotes := intState.Truststore()
	return clients.Query(ctx, true, func(ctx context.Context, c types.Client) error {
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
}

// validateServerConfigs checks that each server name has a matching additional
// listener and that no two servers share the same address.
func validateServerConfigs(s *internalState.InternalState, servers map[string]types.ServerConfig) error {
	extensionServers := s.ExtensionServers()
	for serverName := range servers {
		if !slices.Contains(extensionServers, serverName) {
			return fmt.Errorf("No matching additional listener found for %q", serverName)
		}
	}

	serverAddresses := []string{s.Address().Host}
	for _, server := range servers {
		serverAddress := server.Address.String()

		if slices.Contains(serverAddresses, serverAddress) {
			return fmt.Errorf("Address %q is already in use", serverAddress)
		}

		serverAddresses = append(serverAddresses, serverAddress)
	}

	return nil
}
