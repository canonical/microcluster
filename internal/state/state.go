package state

import (
	"context"
	"net/http"
	"time"

	"github.com/lxc/lxd/lxd/cluster/request"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"

	"github.com/canonical/microcluster/client"
	"github.com/canonical/microcluster/internal/db"
	"github.com/canonical/microcluster/internal/endpoints"
	internalClient "github.com/canonical/microcluster/internal/rest/client"
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
	Address api.URL

	// Server.
	Endpoints *endpoints.Endpoints

	// Server certificate is used for server-to-server connection.
	ServerCert func() *shared.CertInfo

	// Cluster certificate is used for downstream connections within a cluster.
	ClusterCert func() *shared.CertInfo

	// Database.
	Database *db.DB

	// Remotes.
	Remotes func() *trust.Remotes

	// Initialize APIs and bootstrap/join database.
	StartAPI func(bootstrap bool, joinAddresses ...string) error

	// Stop fully stops the daemon, its database, and all listeners.
	Stop func() error
}

// Cluster returns a client for every member of a cluster, except
// this one, with the UserAgentNotifier header set if a request is given.
func (s *State) Cluster(r *http.Request) (client.Cluster, error) {
	if r != nil {
		r.Header.Set("User-Agent", request.UserAgentNotifier)
	}

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
		if s.Address.URL.Host == clusterMember.Address.String() {
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
	ctx, cancel := context.WithTimeout(s.Context, time.Second*5)
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
