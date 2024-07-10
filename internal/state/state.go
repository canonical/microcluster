package state

import (
	"context"
	"time"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"

	"github.com/canonical/microcluster/client"
	internalConfig "github.com/canonical/microcluster/internal/config"
	"github.com/canonical/microcluster/internal/db"
	"github.com/canonical/microcluster/internal/endpoints"
	"github.com/canonical/microcluster/internal/extensions"
	internalClient "github.com/canonical/microcluster/internal/rest/client"
	"github.com/canonical/microcluster/internal/sys"
	"github.com/canonical/microcluster/internal/trust"
	"github.com/canonical/microcluster/rest/types"
)

// State is a gateway to the stateful components of the microcluster daemon.
type State struct {
	// Context.
	Context context.Context

	// Ready channel.
	ReadyCh chan struct{}

	// ShutdownDoneCh receives the result of the d.Stop() function and tells the daemon to end.
	ShutdownDoneCh chan error

	// File structure.
	OS *sys.OS

	// Listen Address.
	Address func() *api.URL

	// Name of the cluster member.
	Name func() string

	// Server.
	Endpoints *endpoints.Endpoints

	// Server certificate is used for server-to-server connection.
	ServerCert func() *shared.CertInfo

	// Cluster certificate is used for downstream connections within a cluster.
	ClusterCert func() *shared.CertInfo

	// Local daemon's config.
	LocalConfig func() *internalConfig.DaemonConfig

	// Database.
	Database *db.DB

	// Remotes.
	Remotes func() *trust.Remotes

	// Initialize APIs and bootstrap/join database.
	StartAPI func(bootstrap bool, initConfig map[string]string, newConfig *trust.Location, joinAddresses ...string) error

	// Update the additional listeners.
	UpdateServers func() error

	// Stop fully stops the daemon, its database, and all listeners.
	Stop func() (exit func(), stopErr error)

	// Runtime extensions.
	Extensions extensions.Extensions

	// Returns an immutable list of the daemon's additional listeners.
	ExtensionServers func() []string
}

// StopListeners stops the network listeners and the fsnotify listener.
var StopListeners func() error

// PostRemoveHook is a post-action hook that is run on all cluster members when a cluster member is removed.
var PostRemoveHook func(state *State, force bool) error

// PreRemoveHook is a post-action hook that is run on a cluster member just before it is is removed.
var PreRemoveHook func(state *State, force bool) error

// OnHeartbeatHook is a post-action hook that is run on the leader after a successful heartbeat round.
var OnHeartbeatHook func(state *State) error

// OnNewMemberHook is a post-action hook that is run on all cluster members when a new cluster member joins the cluster.
var OnNewMemberHook func(state *State) error

// OnDaemonConfigUpdate is a post-action hook that is run on all cluster members when any cluster member receives a local configuration update.
var OnDaemonConfigUpdate func(state *State, config types.DaemonConfig) error

// ReloadCert reloads the given keypair from the state directory.
var ReloadCert func(name types.CertificateName) error

// Cluster returns a client for every member of a cluster, except
// this one.
// All requests made by the client will have the UserAgentNotifier header set
// if isNotification is true.
func (s *State) Cluster(isNotification bool) (client.Cluster, error) {
	c, err := s.Leader()
	if err != nil {
		return nil, err
	}

	clusterMembers, err := c.GetClusterMembers(s.Context)
	if err != nil {
		return nil, err
	}

	clients := make(client.Cluster, 0, len(clusterMembers)-1)
	for _, clusterMember := range clusterMembers {
		if s.Address().URL.Host == clusterMember.Address.String() {
			continue
		}

		publicKey, err := s.ClusterCert().PublicKeyX509()
		if err != nil {
			return nil, err
		}

		url := api.NewURL().Scheme("https").Host(clusterMember.Address.String())
		c, err := internalClient.New(*url, s.ServerCert(), publicKey, isNotification)
		if err != nil {
			return nil, err
		}

		clients = append(clients, client.Client{Client: *c})
	}

	return clients, nil
}

// Leader returns a client connected to the dqlite leader.
func (s *State) Leader() (*client.Client, error) {
	ctx, cancel := context.WithTimeout(s.Context, time.Second*30)
	defer cancel()

	leaderClient, err := s.Database.Leader(ctx)
	if err != nil {
		return nil, err
	}

	leaderInfo, err := leaderClient.Leader(ctx)
	if err != nil {
		return nil, err
	}

	publicKey, err := s.ClusterCert().PublicKeyX509()
	if err != nil {
		return nil, err
	}

	url := api.NewURL().Scheme("https").Host(leaderInfo.Address)
	c, err := internalClient.New(*url, s.ServerCert(), publicKey, false)
	if err != nil {
		return nil, err
	}

	return &client.Client{Client: *c}, nil
}
