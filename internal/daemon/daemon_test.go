package daemon

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/canonical/microcluster/internal/config"
	"github.com/canonical/microcluster/internal/endpoints"
	"github.com/canonical/microcluster/internal/sys"
	"github.com/canonical/microcluster/rest"
	"github.com/canonical/microcluster/rest/types"
)

type daemonsSuite struct {
	suite.Suite
}

func TestDaemonsSuite(t *testing.T) {
	suite.Run(t, new(daemonsSuite))
}

func (t *daemonsSuite) Test_UpdateServers() {
	addrOne, err := types.ParseAddrPort("127.0.0.1:1234")
	require.NoError(t.T(), err)

	addrTwo, err := types.ParseAddrPort("127.0.0.1:1235")
	require.NoError(t.T(), err)

	tests := []struct {
		name                  string
		extensionServers      map[string]rest.Server
		extensionServerConfig map[string]types.ServerConfig
		modifier              func(daemon *Daemon)
		listeningOn           []types.AddrPort
		notListeningOn        []types.AddrPort
		expectedError         string
	}{
		{
			name: "Configure a single server",
			extensionServers: map[string]rest.Server{
				"server": {},
			},
			extensionServerConfig: map[string]types.ServerConfig{
				"server": {
					Address: addrOne,
				},
			},
			listeningOn: []types.AddrPort{addrOne},
		},
		{
			name: "Configure multiple servers",
			extensionServers: map[string]rest.Server{
				"server":  {},
				"server2": {},
			},
			extensionServerConfig: map[string]types.ServerConfig{
				"server": {
					Address: addrOne,
				},
				"server2": {
					Address: addrTwo,
				},
			},
			listeningOn: []types.AddrPort{addrOne, addrTwo},
		},
		{
			name: "Shutdown a single running server",
			extensionServers: map[string]rest.Server{
				"server":  {},
				"server2": {},
			},
			extensionServerConfig: map[string]types.ServerConfig{
				"server": {
					Address: addrOne,
				},
				"server2": {
					Address: addrTwo,
				},
			},
			modifier: func(daemon *Daemon) {
				daemon.config.SetServers(map[string]types.ServerConfig{
					"server2": {
						Address: addrTwo,
					},
				})
				require.NoError(t.T(), daemon.UpdateServers())
			},
			listeningOn:    []types.AddrPort{addrTwo},
			notListeningOn: []types.AddrPort{addrOne},
		},
		{
			name: "Shutdown all running servers",
			extensionServers: map[string]rest.Server{
				"server":  {},
				"server2": {},
			},
			extensionServerConfig: map[string]types.ServerConfig{
				"server": {
					Address: addrOne,
				},
				"server2": {
					Address: addrTwo,
				},
			},
			modifier: func(daemon *Daemon) {
				daemon.config.SetServers(map[string]types.ServerConfig{})
				require.NoError(t.T(), daemon.UpdateServers())
			},
			notListeningOn: []types.AddrPort{addrOne, addrTwo},
		},
		{
			name: "Rerunning an update on the same config is idempotent",
			extensionServers: map[string]rest.Server{
				"server":  {},
				"server2": {},
			},
			extensionServerConfig: map[string]types.ServerConfig{
				"server": {
					Address: addrOne,
				},
				"server2": {
					Address: addrTwo,
				},
			},
			modifier: func(daemon *Daemon) {
				daemon.config.SetServers(map[string]types.ServerConfig{
					"server": {
						Address: addrOne,
					},
					"server2": {
						Address: addrTwo,
					},
				})
				require.NoError(t.T(), daemon.UpdateServers())
			},
			listeningOn: []types.AddrPort{addrOne, addrTwo},
		},
		{
			name: "Fail to start servers with conflicting addresses",
			extensionServers: map[string]rest.Server{
				"server":  {},
				"server2": {},
			},
			extensionServerConfig: map[string]types.ServerConfig{
				"server": {
					Address: addrOne,
				},
				"server2": {
					Address: addrOne,
				},
			},
			expectedError: `"tcp" listener with address "127.0.0.1:1234" is already running`,
		},
		{
			name: "Fail to start servers with conflicting addresses if one is already running",
			extensionServers: map[string]rest.Server{
				"server":  {},
				"server2": {},
			},
			extensionServerConfig: map[string]types.ServerConfig{
				"server": {
					Address: addrOne,
				},
			},
			modifier: func(daemon *Daemon) {
				daemon.config.SetServers(map[string]types.ServerConfig{
					"server": {
						Address: addrOne,
					},
					"server2": {
						Address: addrOne,
					},
				})
				require.Equal(t.T(), `"tcp" listener with address "127.0.0.1:1234" is already running`, daemon.UpdateServers().Error())
			},
		},
	}

	for i, test := range tests {
		t.T().Logf("%s (case %d)", test.name, i)

		var err error

		// Create a new daemon and set some defaults.
		daemon := NewDaemon("project")
		daemon.version = "1.0.0"
		daemon.config = config.NewDaemonConfig(filepath.Join(t.T().TempDir(), "daemon.yaml"))
		daemon.extensionServers = test.extensionServers
		daemon.endpoints = endpoints.NewEndpoints(context.TODO(), map[string]endpoints.Endpoint{})
		daemon.clusterCert = shared.TestingAltKeyPair()
		daemon.shutdownCtx = context.TODO()

		daemon.os, err = sys.DefaultOS(filepath.Join(t.T().TempDir()), false)
		require.NoError(t.T(), err)

		daemon.config.SetServers(test.extensionServerConfig)
		err = daemon.UpdateServers()
		if test.expectedError != "" {
			require.Equal(t.T(), test.expectedError, err.Error())
		} else {
			require.NoError(t.T(), err)
		}

		// Run the modifier for extra test modification.
		if test.modifier != nil {
			test.modifier(daemon)
		}

		// Check if servers are up.
		for _, addr := range test.listeningOn {
			client, err := util.HTTPClient(string(daemon.ClusterCert().PublicKey()), nil)
			require.NoError(t.T(), err)

			_, err = client.Get(api.NewURL().Scheme("https").Host(addr.String()).String())
			require.NoError(t.T(), err)
		}

		// Check if servers are down.
		for _, addr := range test.notListeningOn {
			client, err := util.HTTPClient(string(daemon.ClusterCert().PublicKey()), nil)
			require.NoError(t.T(), err)

			_, err = client.Get(api.NewURL().Scheme("https").Host(addr.String()).String())
			require.Error(t.T(), err)
		}

		// Close all endpoints.
		err = daemon.endpoints.Down(endpoints.EndpointNetwork)
		require.NoError(t.T(), err)
	}
}
