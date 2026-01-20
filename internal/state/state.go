package state

import (
	"context"
	"crypto/x509"
	"database/sql"
	"fmt"
	"math/rand"
	"net/url"
	"slices"
	"sort"
	"time"

	dqliteClient "github.com/canonical/go-dqlite/v3/client"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"

	"github.com/canonical/microcluster/v3/internal/cluster"
	internalConfig "github.com/canonical/microcluster/v3/internal/config"
	"github.com/canonical/microcluster/v3/internal/db"
	"github.com/canonical/microcluster/v3/internal/endpoints"
	"github.com/canonical/microcluster/v3/internal/extensions"
	internalClient "github.com/canonical/microcluster/v3/internal/rest/client"
	"github.com/canonical/microcluster/v3/internal/trust"
	"github.com/canonical/microcluster/v3/microcluster/types"
)

// State exposes the internal daemon state for use with extended API handlers.
type State interface {
	// FileSystem structure.
	FileSystem() types.OS

	// Listen Address.
	Address() *url.URL

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
	Truststore() types.Store

	// Returns a connector for interconnection with the cluster.
	Connect() types.Connector

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

	InternalFileSystem       func() types.OS
	InternalAddress          func() *url.URL
	InternalName             func() string
	InternalVersion          func() string
	InternalServerCert       func() *shared.CertInfo
	InternalClusterCert      func() *shared.CertInfo
	InternalDatabase         *db.DqliteDB
	InternalRemotes          func() *trust.Remotes
	InternalExtensionServers func() []string
}

// FileSystem can be used to inspect the microcluster filesystem.
func (s *InternalState) FileSystem() types.OS {
	return s.InternalFileSystem()
}

// Address returns the core microcluster listen address.
func (s *InternalState) Address() *url.URL {
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

func (s *InternalState) Connect() types.Connector {
	return s
}

// Cluster returns a client for every member of a cluster, except
// this one.
// All requests made by the client will have the UserAgentNotifier header set
// if isNotification is true.
// Uses the trust store instead of database for better fault tolerance -
// trust store is updated on heartbeats and shouldn't contain crashed nodes.
func (s *InternalState) Cluster(isNotification bool) (types.Clients, error) {
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
	clients := make(types.Clients, 0, len(allClients)-1)
	for _, client := range allClients {
		if s.Address().Host != client.URL().Host {
			clients = append(clients, client)
		}
	}

	// Return error if no other cluster members exist.
	if len(clients) == 0 {
		return nil, fmt.Errorf("No other cluster members available.")
	}

	return clients, nil
}

// Leader returns a client connected to the dqlite leader.
func (s *InternalState) Leader(isNotification bool) (types.Client, error) {
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

	url := &api.NewURL().Scheme("https").Host(leaderInfo.Address).URL
	c, err := internalClient.New(url, s.ServerCert(), publicKey, isNotification)
	if err != nil {
		return nil, err
	}

	return c, nil
}

// Member returns a client to a specific cluster member based on the given url.
// An additional certificate can be provided to verify the remote endpoint.
func (s *InternalState) Member(url *url.URL, isNotification bool, cert *x509.Certificate) (types.Client, error) {
	// If no certificate was provided fallback to the cluster cert.
	if cert == nil {
		var err error

		cert, err = s.ClusterCert().PublicKeyX509()
		if err != nil {
			return nil, err
		}
	}

	c, err := internalClient.New(url, s.ServerCert(), cert, isNotification)
	if err != nil {
		return nil, err
	}

	return c, nil
}

// RandomMember returns a client for a random cluster member.
func (s *InternalState) RandomMember(isNotification bool) (types.Client, error) {
	clusterClients, err := s.Cluster(isNotification)
	if err != nil {
		return nil, err
	}

	clusterClientNum := len(clusterClients)

	switch clusterClientNum {
	case 0:
		// Returns an error if the cluster is uninitialized (not bootstrapped, not joined).
		return nil, fmt.Errorf("Cluster is uninitialized or has no members")
	case 1:
		// Returns the only available client if cluster size is 1.
		return clusterClients[0], nil
	default:
		// Returns a randomly selected client for clusters with multiple members.
		return clusterClients[rand.Intn(clusterClientNum)], nil
	}
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
				return fmt.Errorf("Membership consistency check failed after timeout: %w", err)
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
