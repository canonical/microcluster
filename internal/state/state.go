package state

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"sort"
	"time"

	dqliteClient "github.com/canonical/go-dqlite/v2/client"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"

	"github.com/canonical/microcluster/v2/client"
	"github.com/canonical/microcluster/v2/cluster"
	internalConfig "github.com/canonical/microcluster/v2/internal/config"
	"github.com/canonical/microcluster/v2/internal/db"
	"github.com/canonical/microcluster/v2/internal/endpoints"
	"github.com/canonical/microcluster/v2/internal/extensions"
	internalClient "github.com/canonical/microcluster/v2/internal/rest/client"
	"github.com/canonical/microcluster/v2/internal/sys"
	"github.com/canonical/microcluster/v2/internal/trust"
	"github.com/canonical/microcluster/v2/rest/types"
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

	// StopListeners stops the network listeners and servers, and the fsnotify listener.
	StopListeners func() error

	// Stop fully stops the daemon, its database, all listeners, and all servers.
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
// Uses the trust store instead of database for better fault tolerance -
// trust store is updated on heartbeats and shouldn't contain crashed nodes.
func (s *InternalState) Cluster(isNotification bool) (client.Cluster, error) {
	publicKey, err := s.ClusterCert().PublicKeyX509()
	if err != nil {
		return nil, err
	}

	// Use trust store instead of database - it's updated on heartbeats
	// and is more likely to reflect current reachable cluster state
	remotes := s.Remotes()
	allClients, err := remotes.Cluster(isNotification, s.ServerCert(), publicKey)
	if err != nil {
		return nil, err
	}

	// Filter out ourselves from the client list
	clients := make(client.Cluster, 0, len(allClients)-1)
	for _, client := range allClients {
		if s.Address().URL.Host != client.URL().URL.Host {
			clients = append(clients, client)
		}
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
	c, err := internalClient.New(*url, s.ServerCert(), publicKey, false)
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

// CheckMembershipConsistency verifies that core_cluster_members, truststore, and dqlite
// all have consistent membership information. This should be called before any member
// add/remove operations or join token generation to ensure the cluster is in a healthy state.
func (s *InternalState) CheckMembershipConsistency(ctx context.Context) error {
	// Assign a context timeout if we don't already have one.
	_, ok := ctx.Deadline()
	if !ok {
		timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		ctx = timeoutCtx
		defer cancel()
	}

	for {
		coreClusterMembers, truststoreRemotes, dqliteNodes, err := s.getMembershipData(ctx)
		if err != nil {
			return fmt.Errorf("Failed to gather membership data for consistency check: %w", err)
		}

		err = s.checkMembershipConsistency(coreClusterMembers, truststoreRemotes, dqliteNodes)
		if err != nil {
			select {
			case <-ctx.Done():
				return fmt.Errorf("Membership consistency check failed: %w", err)
			case <-time.After(200 * time.Millisecond):
				continue
			}
		}

		return nil
	}
}

// getMembershipData retrieves membership information from all sources.
func (s *InternalState) getMembershipData(ctx context.Context) ([]cluster.CoreClusterMember, map[string]trust.Remote, []dqliteClient.NodeInfo, error) {
	// Get database core cluster members
	var coreClusterMembers []cluster.CoreClusterMember
	err := s.Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		coreClusterMembers, err = cluster.GetCoreClusterMembers(ctx, tx)
		return err
	})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("Failed to get core cluster members from database: %w", err)
	}

	// Get truststore remotes
	truststoreRemotes := s.Remotes().RemotesByName()

	// Get dqlite cluster info
	leaderClient, err := s.Database().Leader(ctx)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("Failed to get dqlite leader: %w", err)
	}

	// Get dqlite cluster members
	dqliteNodes, err := s.Database().Cluster(ctx, leaderClient)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("Failed to get dqlite cluster info: %w", err)
	}

	return coreClusterMembers, truststoreRemotes, dqliteNodes, nil
}

// checkMembershipConsistency checks consistency across all three membership sources using addresses.
func (s *InternalState) checkMembershipConsistency(coreClusterMembers []cluster.CoreClusterMember, truststoreRemotes map[string]trust.Remote, dqliteNodes []dqliteClient.NodeInfo) error {
	// Collect addresses from each source into sorted slices
	var coreClusterAddresses []string
	for _, member := range coreClusterMembers {
		coreClusterAddresses = append(coreClusterAddresses, member.Address)
	}

	sort.Strings(coreClusterAddresses)

	var trustAddresses []string
	for _, remote := range truststoreRemotes {
		trustAddresses = append(trustAddresses, remote.Address.String())
	}

	sort.Strings(trustAddresses)

	var dqliteAddresses []string
	for _, node := range dqliteNodes {
		dqliteAddresses = append(dqliteAddresses, node.Address)
	}

	sort.Strings(dqliteAddresses)

	// Check if all three slices are equal
	if !slices.Equal(coreClusterAddresses, trustAddresses) || !slices.Equal(coreClusterAddresses, dqliteAddresses) {
		return fmt.Errorf("Microcluster node membership is inconsistent across core_cluster_members (%v), truststore (%v) and dqlite (%v)", coreClusterAddresses, trustAddresses, dqliteAddresses)
	}

	return nil
}
