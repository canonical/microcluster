package state

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/canonical/lxd/lxd/cluster/request"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"

	"github.com/canonical/microcluster/client"
	"github.com/canonical/microcluster/internal/db"
	"github.com/canonical/microcluster/internal/endpoints"
	internalClient "github.com/canonical/microcluster/internal/rest/client"
	"github.com/canonical/microcluster/internal/rest/types"
	"github.com/canonical/microcluster/internal/sys"
	"github.com/canonical/microcluster/internal/trust"
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

	// Role of the cluster member, whether it is part of dqlite (cluster) or not (non-cluster).
	Role func() trust.Role

	// Server.
	Endpoints *endpoints.Endpoints

	// Server certificate is used for server-to-server connection.
	ServerCert func() *shared.CertInfo

	// Cluster certificate is used for downstream connections within a cluster.
	ClusterCert func() *shared.CertInfo

	// Database.
	Database *db.DB

	// Returns the locally stored set of microcluster systems. If no role is specified, defaults to "cluster" for dqlite peers.
	Remotes func(roles trust.Role) *trust.Remotes

	// Initialize APIs and bootstrap/join database.
	StartAPI func(bootstrap bool, initConfig map[string]string, newConfig *trust.Location, joinAddresses ...string) error

	// UpgradeAPI re-initializes the APIs and starts the database on a non-clustered node.
	UpgradeAPI func(config *trust.Location) error

	// Stop fully stops the daemon, its database, and all listeners.
	Stop func() error
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

// Cluster returns a client for every member of a cluster, except
// this one, with the UserAgentNotifier header set if a request is given.
func (s *State) Cluster(r *http.Request, role trust.Role) (client.Cluster, error) {
	if r != nil {
		r.Header.Set("User-Agent", request.UserAgentNotifier)
	}

	c, err := s.Leader()
	if err != nil {
		return nil, err
	}

	var clusterMembers []types.ClusterMemberLocal
	switch role {
	case trust.Cluster:
		dqliteClusterMembers, err := c.GetClusterMembers(s.Context)
		if err != nil {
			return nil, err
		}

		clusterMembers = make([]types.ClusterMemberLocal, 0, len(dqliteClusterMembers))
		for _, member := range dqliteClusterMembers {
			clusterMembers = append(clusterMembers, member.ClusterMemberLocal)
		}

	case trust.NonCluster:
		clusterMembers, err = c.GetNonClusterMembers(s.Context)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("%q is not a valid microcluster role", role)
	}

	if len(clusterMembers) == 0 {
		return client.Cluster{}, nil
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
		c, err := internalClient.New(*url, s.ServerCert(), publicKey, true)
		if err != nil {
			return nil, err
		}

		clients = append(clients, client.Client{Client: *c})
	}

	return clients, nil
}

// Leader returns a client connected to the dqlite leader.
func (s *State) Leader() (*client.Client, error) {
	if !s.Database.IsOpen() {
		return nil, fmt.Errorf("Failed to check for database leader, the database is offline")
	}

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
