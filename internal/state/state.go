package state

import (
	"context"
	"fmt"
	"time"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"

	"github.com/canonical/microcluster/v3/client"
	internalConfig "github.com/canonical/microcluster/v3/internal/config"
	"github.com/canonical/microcluster/v3/internal/db"
	"github.com/canonical/microcluster/v3/internal/endpoints"
	"github.com/canonical/microcluster/v3/internal/extensions"
	internalClient "github.com/canonical/microcluster/v3/internal/rest/client"
	"github.com/canonical/microcluster/v3/internal/sys"
	"github.com/canonical/microcluster/v3/internal/trust"
	"github.com/canonical/microcluster/v3/rest/types"
)

// State exposes the internal daemon state for use with extended API handlers.
type State interface {
	// FileSystem structure.
	FileSystem() *sys.OS

	// Listen Address.
	Address() *api.URL

	// Name of the cluster member.
	Name() string

	// Version is provided by the MicroCluster consumer.
	Version() string

	// Server certificate is used for server-to-server connection.
	ServerCert() *shared.CertInfo

	// Cluster certificate is used for downstream connections within a cluster.
	ClusterCert() *shared.CertInfo

	// Database.
	Database() db.DB

	// Local truststore access.
	Remotes() *trust.Remotes

	// Cluster returns a client to every cluster member according to dqlite.
	Cluster(isNotification bool) (client.Cluster, error)

	// Leader returns a client to the dqlite cluster leader.
	Leader() (*client.Client, error)

	// HasExtension returns whether the given API extension is supported.
	HasExtension(ext string) bool

	// ExtensionServers returns an immutable list of the daemon's additional listeners.
	ExtensionServers() []string
}

// InternalState is a gateway to the stateful components of the microcluster daemon.
type InternalState struct {
	// Context.
	Context context.Context

	// Ready channel.
	ReadyCh chan struct{}

	// ShutdownDoneCh receives the result of the d.Stop() function and tells the daemon to end.
	ShutdownDoneCh chan error

	// Endpoints manages the network and unix socket listeners.
	Endpoints *endpoints.Endpoints

	// Local daemon's config.
	LocalConfig func() *internalConfig.DaemonConfig

	// SetConfig Applies and commits to memory the supplied daemon configuration.
	SetConfig func(trust.Location) error

	// Initialize APIs and bootstrap/join database.
	StartAPI func(ctx context.Context, bootstrap bool, initConfig map[string]string, joinAddresses ...string) error

	// Update the additional listeners.
	UpdateServers func() error

	// ReloadCert reloads the given keypair from the state directory.
	ReloadCert func(name types.CertificateName) error

	// StopListeners stops the network listeners and the fsnotify listener.
	StopListeners func() error

	// Stop fully stops the daemon, its database, and all listeners.
	Stop func() (exit func(), stopErr error)

	// Runtime extensions.
	Extensions extensions.Extensions

	// Hooks contain external implementations that are triggered by specific cluster actions.
	Hooks *Hooks

	InternalFileSystem       func() *sys.OS
	InternalAddress          func() *api.URL
	InternalName             func() string
	InternalVersion          func() string
	InternalServerCert       func() *shared.CertInfo
	InternalClusterCert      func() *shared.CertInfo
	InternalDatabase         *db.DqliteDB
	InternalRemotes          func() *trust.Remotes
	InternalExtensionServers func() []string
}

// FileSystem can be used to inspect the microcluster filesystem.
func (s *InternalState) FileSystem() *sys.OS {
	return s.InternalFileSystem()
}

// Address returns the core microcluster listen address.
func (s *InternalState) Address() *api.URL {
	return s.InternalAddress()
}

// Name returns the cluster name for the local system.
func (s *InternalState) Name() string {
	return s.InternalName()
}

// Version is provided by the MicroCluster consumer. The daemon includes it in
// its /cluster/1.0 response.
func (s *InternalState) Version() string {
	return s.InternalVersion()
}

// ServerCert returns the keypair identifying the local system.
func (s *InternalState) ServerCert() *shared.CertInfo {
	return s.InternalServerCert()
}

// ClusterCert returns the keypair identifying the cluster.
func (s *InternalState) ClusterCert() *shared.CertInfo {
	return s.InternalClusterCert()
}

// Database allows access to the dqlite database.
func (s *InternalState) Database() db.DB {
	return s.InternalDatabase
}

// Remotes returns the local record of cluster members in the truststore.
func (s *InternalState) Remotes() *trust.Remotes {
	return s.InternalRemotes()
}

// ExtensionServers returns an immutable list of the daemon's additional listeners.
func (s *InternalState) ExtensionServers() []string {
	return s.InternalExtensionServers()
}

// HasExtension returns whether the given API extension is supported.
func (s *InternalState) HasExtension(ext string) bool {
	return s.Extensions.HasExtension(ext)
}

// Cluster returns a client for every member of a cluster, except
// this one.
// All requests made by the client will have the UserAgentNotifier header set
// if isNotification is true.
func (s *InternalState) Cluster(isNotification bool) (client.Cluster, error) {
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
		c, err := internalClient.New(*url, s.ServerCert(), publicKey, s.InternalDatabase.GetSessionCache(), isNotification)
		if err != nil {
			return nil, err
		}

		clients = append(clients, client.Client{Client: *c})
	}

	return clients, nil
}

// Leader returns a client connected to the dqlite leader.
func (s *InternalState) Leader() (*client.Client, error) {
	ctx, cancel := context.WithTimeout(s.Context, time.Second*30)
	defer cancel()

	leaderClient, err := s.Database().Leader(ctx)
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
	c, err := internalClient.New(*url, s.ServerCert(), publicKey, s.InternalDatabase.GetSessionCache(), false)
	if err != nil {
		return nil, err
	}

	return &client.Client{Client: *c}, nil
}

// ToInternal returns the underlying InternalState from the exposed State interface.
func ToInternal(s State) (*InternalState, error) {
	internal, ok := s.(*InternalState)
	if ok {
		return internal, nil
	}

	return nil, fmt.Errorf("Underlying State is not an InternalState")
}
