package daemon

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/canonical/lxd/lxd/db/schema"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/revert"
	"github.com/gorilla/mux"

	"github.com/canonical/microcluster/client"
	"github.com/canonical/microcluster/cluster"
	internalConfig "github.com/canonical/microcluster/internal/config"
	"github.com/canonical/microcluster/internal/db"
	"github.com/canonical/microcluster/internal/endpoints"
	"github.com/canonical/microcluster/internal/extensions"
	"github.com/canonical/microcluster/internal/recover"
	internalREST "github.com/canonical/microcluster/internal/rest"
	internalClient "github.com/canonical/microcluster/internal/rest/client"
	"github.com/canonical/microcluster/internal/rest/resources"
	internalTypes "github.com/canonical/microcluster/internal/rest/types"
	internalState "github.com/canonical/microcluster/internal/state"
	"github.com/canonical/microcluster/internal/sys"
	"github.com/canonical/microcluster/internal/trust"
	"github.com/canonical/microcluster/rest"
	"github.com/canonical/microcluster/rest/types"
	"github.com/canonical/microcluster/state"
)

// Daemon holds information for the microcluster daemon.
type Daemon struct {
	project string // The project refers to the name of the go-project that is calling MicroCluster.
	version string // The version of the go-project that is calling MicroCluster

	config *internalConfig.DaemonConfig // Local daemon's configuration from daemon.yaml file.

	os         *sys.OS
	serverCert *shared.CertInfo

	clusterMu   sync.RWMutex
	clusterCert *shared.CertInfo

	endpoints *endpoints.Endpoints
	db        *db.DB

	fsWatcher  *sys.Watcher
	trustStore *trust.Store

	hooks state.Hooks // Hooks to be called upon various daemon actions.

	ReadyChan      chan struct{}      // Closed when the daemon is fully ready.
	shutdownCtx    context.Context    // Cancelled when shutdown starts.
	shutdownDoneCh chan error         // Receives the result of state.Stop() when exit() is called and tells the daemon to end.
	shutdownCancel context.CancelFunc // Cancels the shutdownCtx to indicate shutdown starting.

	Extensions extensions.Extensions // Extensions supported at runtime by the daemon.

	// stop is a sync.Once which wraps the daemon's stop sequence. Each call will block until the first one completes.
	stop func() error

	extensionServersMu sync.RWMutex
	extensionServers   map[string]rest.Server
}

// NewDaemon initializes the Daemon context and channels.
func NewDaemon(project string, version string) *Daemon {
	d := &Daemon{
		shutdownDoneCh:   make(chan error),
		ReadyChan:        make(chan struct{}),
		extensionServers: make(map[string]rest.Server),
		project:          project,
		version:          version,
	}

	d.stop = sync.OnceValue(func() error {
		if d.shutdownCancel != nil {
			d.shutdownCancel()
		}

		var dqliteErr error
		if d.db != nil {
			dqliteErr = d.db.Stop()
			if dqliteErr != nil {
				logger.Error("Failed shutting down database", logger.Ctx{"error": dqliteErr})
			}
		}

		if d.endpoints != nil {
			err := d.endpoints.Down()
			if err != nil {
				return err
			}
		}

		return dqliteErr
	})

	return d
}

// Run initializes the Daemon with the given configuration, starts the database, and blocks until the daemon is cancelled.
// - `extensionsSchema` is a list of schema updates in the order that they should be applied.
// - `extensionServers` is a list of rest.Server that will be initialized and managed by microcluster.
// - `hooks` are a set of functions that trigger at certain points during cluster communication.
func (d *Daemon) Run(ctx context.Context, listenAddress string, stateDir string, socketGroup string, extensionsSchema []schema.Update, apiExtensions []string, extensionServers map[string]rest.Server, hooks *state.Hooks) error {
	d.shutdownCtx, d.shutdownCancel = context.WithCancel(ctx)
	if stateDir == "" {
		stateDir = os.Getenv(sys.StateDir)
	}

	if stateDir == "" {
		return fmt.Errorf("State directory must be specified")
	}

	_, err := os.Stat(stateDir)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("Failed to find state directory: %w", err)
	}

	d.os, err = sys.DefaultOS(stateDir, socketGroup, true)
	if err != nil {
		return fmt.Errorf("Failed to initialize directory structure: %w", err)
	}

	// Setup the deamon's internal config.
	d.config = internalConfig.NewDaemonConfig(filepath.Join(d.os.StateDir, "daemon.yaml"))

	// Clean up the daemon state on an error during init.
	reverter := revert.New()
	defer reverter.Fail()
	reverter.Add(func() {
		err := d.stop()
		if err != nil {
			logger.Error("Failed to cleanly stop the daemon", logger.Ctx{"error": err})
		}
	})

	err = recover.MaybeUnpackRecoveryTarball(d.os)
	if err != nil {
		return fmt.Errorf("Database recovery failed: %w", err)
	}

	d.extensionServersMu.Lock()
	// Deep copy the supplied extension servers to prevent assigning the map by reference.
	for k, v := range extensionServers {
		// `core` and `unix` are reserved server names.
		if shared.ValueInSlice(k, []string{endpoints.EndpointsCore, endpoints.EndpointsUnix}) {
			return fmt.Errorf("Cannot use the reserved server name %q", k)
		}

		d.extensionServers[k] = v
	}

	d.extensionServersMu.Unlock()

	err = d.init(listenAddress, extensionsSchema, apiExtensions, hooks)
	if err != nil {
		return fmt.Errorf("Daemon failed to start: %w", err)
	}

	err = d.hooks.OnStart(d.shutdownCtx, d.State())
	if err != nil {
		return fmt.Errorf("Failed to run post-start hook: %w", err)
	}

	close(d.ReadyChan)

	reverter.Success()

	for {
		select {
		case <-ctx.Done():
			return d.stop()
		case err := <-d.shutdownDoneCh:
			return err
		}
	}
}

func (d *Daemon) init(listenAddress string, schemaExtensions []schema.Update, apiExtensions []string, hooks *state.Hooks) error {
	d.applyHooks(hooks)

	var err error
	name, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("Failed to assign default system name: %w", err)
	}

	d.config.SetName(name)

	// Initialize the extensions registry with the internal extensions.
	d.Extensions, err = extensions.NewExtensionRegistry(true)
	if err != nil {
		return err
	}

	// Register the extensions passed at initialization.
	err = d.Extensions.Register(apiExtensions)
	if err != nil {
		return err
	}

	d.serverCert, err = util.LoadServerCert(d.os.StateDir)
	if err != nil {
		return err
	}

	err = d.initStore()
	if err != nil {
		return fmt.Errorf("Failed to initialize trust store: %w", err)
	}

	d.db = db.NewDB(d.shutdownCtx, d.serverCert, d.ClusterCert, d.os)

	listenAddr := api.NewURL()
	if listenAddress != "" {
		listenAddr = listenAddr.Host(listenAddress)

		addrPort, err := types.ParseAddrPort(listenAddress)
		if err != nil {
			return fmt.Errorf("Failed to parse initial listen address: %w", err)
		}

		d.config.SetAddress(addrPort)
	}

	d.extensionServersMu.RLock()
	err = resources.ValidateEndpoints(d.extensionServers, listenAddr.URL.Host)
	if err != nil {
		return err
	}

	d.extensionServersMu.RUnlock()

	serverEndpoints := []rest.Resources{
		resources.UnixEndpoints,
		resources.InternalEndpoints,
		resources.PublicEndpoints,
	}

	d.extensionServersMu.RLock()
	for _, server := range d.extensionServers {
		if server.ServeUnix {
			serverEndpoints = append(serverEndpoints, server.Resources...)
		}
	}

	d.extensionServersMu.RUnlock()

	err = d.startUnixServer(serverEndpoints)
	if err != nil {
		return err
	}

	if listenAddress != "" {
		serverEndpoints = []rest.Resources{resources.PublicEndpoints}
		err = d.addCoreServers(true, *listenAddr, d.ServerCert(), serverEndpoints)
		if err != nil {
			return err
		}
	}

	// Add extension servers before post-join hook.
	err = d.addExtensionServers(true, d.ServerCert(), listenAddr.URL.Host)
	if err != nil {
		return err
	}

	d.db.SetSchema(schemaExtensions, d.Extensions)

	err = d.reloadIfBootstrapped()
	if err != nil {
		return err
	}

	err = d.trustStore.Refresh()
	if err != nil {
		return err
	}

	return nil
}

func (d *Daemon) applyHooks(hooks *state.Hooks) {
	// Apply a no-op hooks for any missing hooks.
	noOpHook := func(ctx context.Context, s state.State) error { return nil }
	noOpRemoveHook := func(ctx context.Context, s state.State, force bool) error { return nil }
	noOpInitHook := func(ctx context.Context, s state.State, initConfig map[string]string) error { return nil }
	noOpConfigHook := func(ctx context.Context, s state.State, config types.DaemonConfig) error { return nil }
	noOpNewMemberHook := func(ctx context.Context, s state.State, newMember types.ClusterMemberLocal) error { return nil }

	if hooks == nil {
		d.hooks = state.Hooks{}
	} else {
		d.hooks = *hooks
	}

	if d.hooks.PreBootstrap == nil {
		d.hooks.PreBootstrap = noOpInitHook
	}

	if d.hooks.PostBootstrap == nil {
		d.hooks.PostBootstrap = noOpInitHook
	}

	if d.hooks.PostJoin == nil {
		d.hooks.PostJoin = noOpInitHook
	}

	if d.hooks.PreJoin == nil {
		d.hooks.PreJoin = noOpInitHook
	}

	if d.hooks.OnStart == nil {
		d.hooks.OnStart = noOpHook
	}

	if d.hooks.OnHeartbeat == nil {
		d.hooks.OnHeartbeat = noOpHook
	}

	if d.hooks.OnNewMember == nil {
		d.hooks.OnNewMember = noOpNewMemberHook
	}

	if d.hooks.PreRemove == nil {
		d.hooks.PreRemove = noOpRemoveHook
	}

	if d.hooks.PostRemove == nil {
		d.hooks.PostRemove = noOpRemoveHook
	}

	if d.hooks.OnDaemonConfigUpdate == nil {
		d.hooks.OnDaemonConfigUpdate = noOpConfigHook
	}
}

func (d *Daemon) reloadIfBootstrapped() error {
	_, err := os.Stat(filepath.Join(d.os.DatabaseDir, "info.yaml"))
	if err != nil {
		if os.IsNotExist(err) {
			logger.Warn("microcluster database is uninitialized")
			return nil
		}

		return err
	}

	_, err = os.Stat(filepath.Join(d.os.StateDir, "daemon.yaml"))
	if err != nil {
		if os.IsNotExist(err) {
			logger.Warn("microcluster daemon config is missing")
			return nil
		}

		return err
	}

	err = d.config.Load()
	if err != nil {
		return fmt.Errorf("Failed to retrieve daemon configuration yaml: %w", err)
	}

	err = d.StartAPI(false, nil, nil)
	if err != nil {
		return err
	}

	return nil
}

func (d *Daemon) initStore() error {
	var err error
	d.fsWatcher, err = sys.NewWatcher(d.shutdownCtx, d.os.StateDir)
	if err != nil {
		return err
	}

	d.trustStore, err = trust.Init(d.fsWatcher, nil, d.os.TrustDir)
	if err != nil {
		return err
	}

	return nil
}

func (d *Daemon) initServer(resources ...rest.Resources) *http.Server {
	/* Setup the web server */
	mux := mux.NewRouter()
	mux.StrictSlash(false)
	mux.SkipClean(true)
	mux.UseEncodedPath()

	state := d.State()
	for _, endpoints := range resources {
		for _, e := range endpoints.Endpoints {
			internalREST.HandleEndpoint(state, mux, string(endpoints.PathPrefix), e)

			for _, alias := range e.Aliases {
				ae := e
				ae.Name = alias.Name
				ae.Path = alias.Path

				internalREST.HandleEndpoint(state, mux, string(endpoints.PathPrefix), ae)
			}
		}
	}

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		err := response.SyncResponse(true, []string{"/1.0"}).Render(w)
		if err != nil {
			logger.Error("Failed to write HTTP response", logger.Ctx{"url": r.URL, "err": err})
		}
	})

	mux.NotFoundHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger.Info("Sending top level 404", logger.Ctx{"url": r.URL})
		w.Header().Set("Content-Type", "application/json")
		err := response.NotFound(nil).Render(w)
		if err != nil {
			logger.Error("Failed to write HTTP response", logger.Ctx{"url": r.URL, "err": err})
		}
	})

	return &http.Server{
		Handler:     mux,
		ConnContext: request.SaveConnectionInContext,
	}
}

// StartAPI starts up the admin and consumer APIs, and generates a cluster cert
// if we are bootstrapping the first node.
func (d *Daemon) StartAPI(bootstrap bool, initConfig map[string]string, newConfig *trust.Location, joinAddresses ...string) error {
	if newConfig != nil {
		d.config.SetAddress(newConfig.Address)
		d.config.SetName(newConfig.Name)

		// Write the latest config to disk.
		err := d.config.Write()
		if err != nil {
			return err
		}
	} else {
		err := d.config.Load()
		if err != nil {
			return err
		}
	}

	if bootstrap {
		err := d.hooks.PreBootstrap(d.State(), initConfig)
		if err != nil {
			return fmt.Errorf("Failed to run pre-bootstrap hook before starting the API: %w", err)
		}
	}

	if d.Address().URL.Host == "" || d.config.GetName() == "" {
		return fmt.Errorf("Cannot start network API without valid daemon configuration")
	}

	serverCert, err := d.serverCert.PublicKeyX509()
	if err != nil {
		return fmt.Errorf("Failed to parse server certificate when bootstrapping API: %w", err)
	}

	addrPort, err := types.ParseAddrPort(d.Address().URL.Host)
	if err != nil {
		return fmt.Errorf("Failed to parse listen address when bootstrapping API: %w", err)
	}

	localNode := trust.Remote{
		Location:    trust.Location{Name: d.config.GetName(), Address: addrPort},
		Certificate: types.X509Certificate{Certificate: serverCert},
	}

	if bootstrap {
		err = d.trustStore.Remotes().Add(d.os.TrustDir, localNode)
		if err != nil {
			return fmt.Errorf("Failed to initialize local remote entry: %w", err)
		}
	}

	err = d.ReloadCert(types.ClusterCertificateName)
	if err != nil {
		return err
	}

	// Validate the extension servers again now that we have applied addresses.
	d.extensionServersMu.RLock()
	err = resources.ValidateEndpoints(d.extensionServers, d.Address().URL.Host)
	if err != nil {
		return err
	}

	d.extensionServersMu.RUnlock()

	err = d.endpoints.Down(endpoints.EndpointNetwork)
	if err != nil {
		return err
	}

	serverEndpoints := []rest.Resources{resources.InternalEndpoints, resources.PublicEndpoints}
	err = d.addCoreServers(false, *d.Address(), d.ClusterCert(), serverEndpoints)
	if err != nil {
		return err
	}

	// Add extension servers before post-join hook.
	err = d.addExtensionServers(false, d.ClusterCert(), d.Address().URL.Host)
	if err != nil {
		return err
	}

	// If bootstrapping the first node, just open the database and create an entry for ourselves.
	if bootstrap {
		clusterMember := cluster.CoreClusterMember{
			Name:        localNode.Name,
			Address:     localNode.Address.String(),
			Certificate: localNode.Certificate.String(),
			Heartbeat:   time.Time{},
			Role:        cluster.Pending,
		}

		clusterMember.SchemaInternal, clusterMember.SchemaExternal, _ = d.db.Schema().Version()

		err = d.db.Bootstrap(d.Extensions, d.project, *d.Address(), clusterMember)
		if err != nil {
			return err
		}

		err = d.trustStore.Refresh()
		if err != nil {
			return err
		}

		err = d.hooks.PostBootstrap(d.State(), initConfig)
		if err != nil {
			return fmt.Errorf("Failed to run post-bootstrap actions: %w", err)
		}

		// Return as we have completed the bootstrap process.
		return nil
	}

	if len(joinAddresses) != 0 {
		err = d.db.Join(d.Extensions, d.project, *d.Address(), joinAddresses...)
		if err != nil {
			return fmt.Errorf("Failed to join cluster: %w", err)
		}
	} else {
		err = d.db.StartWithCluster(d.Extensions, d.project, *d.Address(), d.trustStore.Remotes().Addresses())
		if err != nil {
			return fmt.Errorf("Failed to re-establish cluster connection: %w", err)
		}
	}

	err = d.trustStore.Refresh()
	if err != nil {
		return err
	}

	// Get a client for every other cluster member in the newly refreshed local store.
	publicKey, err := d.ClusterCert().PublicKeyX509()
	if err != nil {
		return err
	}

	cluster, err := d.trustStore.Remotes().Cluster(false, d.ServerCert(), publicKey)
	if err != nil {
		return err
	}

	localMemberInfo := types.ClusterMemberLocal{Name: localNode.Name, Address: localNode.Address, Certificate: localNode.Certificate}
	if len(joinAddresses) > 0 {
		err = d.hooks.PreJoin(d.State(), initConfig)
		if err != nil {
			return err
		}
	}

	if len(joinAddresses) > 0 {
		var lastErr error
		var clusterConfirmation bool
		err = cluster.Query(d.shutdownCtx, true, func(ctx context.Context, c *client.Client) error {
			// No need to send a request to ourselves.
			if d.Address().URL.Host == c.URL().URL.Host {
				return nil
			}

			// At this point the joiner is only trusted on the node that was leader at the time,
			// so find it and have it instruct all dqlite members to trust this system now that it is functional.
			if !clusterConfirmation {
				err := internalClient.AddTrustStoreEntry(ctx, &c.Client, localMemberInfo)
				if err != nil {
					lastErr = err
				} else {
					clusterConfirmation = true
				}
			}

			return nil
		})
		if err != nil {
			return err
		}

		if !clusterConfirmation {
			return fmt.Errorf("Failed to confirm new member %q on any existing system (%d): %w", localMemberInfo.Name, len(cluster)-1, lastErr)
		}
	}

	// Tell the other nodes that this system is up.
	remotes := d.trustStore.Remotes()
	err = cluster.Query(d.shutdownCtx, true, func(ctx context.Context, c *client.Client) error {
		c.SetClusterNotification()

		// No need to send a request to ourselves.
		if d.Address().URL.Host == c.URL().URL.Host {
			return nil
		}

		// Send notification about this node's dqlite version to all other cluster members.
		err = d.sendUpgradeNotification(ctx, c)
		if err != nil {
			return err
		}

		// If this was a join request, instruct all peers to run their OnNewMember hook.
		if len(joinAddresses) > 0 {
			addrPort, err := types.ParseAddrPort(c.URL().URL.Host)
			if err != nil {
				return err
			}

			remote := remotes.RemoteByAddress(addrPort)
			if remote == nil {
				return fmt.Errorf("No remote found at address %q run the post-remove hook", c.URL().URL.Host)
			}

			// Run the OnNewMember hook, and skip errors on any nodes that are still in the process of joining.
			err = internalClient.RunNewMemberHook(ctx, c.Client.UseTarget(remote.Name), internalTypes.HookNewMemberOptions{NewMember: localMemberInfo})
			if err != nil && !api.StatusErrorCheck(err, http.StatusServiceUnavailable) {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return err
	}

	if len(joinAddresses) > 0 {
		return d.hooks.PostJoin(d.State(), initConfig)
	}

	return nil
}

// UpdateServers updates and start/stops the additional listeners.
func (d *Daemon) UpdateServers() error {
	configuredServers := d.config.GetServers()

	// Create a list of additional listeners which are currently configured.
	var configuredServerNames []string
	for name := range configuredServers {
		configuredServerNames = append(configuredServerNames, name)
	}

	// Stop all additional listeners which got removed from the config.
	for _, serverName := range d.ExtensionServers() {
		if !shared.ValueInSlice(serverName, configuredServerNames) {
			// Remove their config.
			d.extensionServersMu.Lock()
			extensionServer, ok := d.extensionServers[serverName]
			if !ok {
				// There isn't any additional listener set that matches this name.
				d.extensionServersMu.Unlock()
				continue
			}

			// Set an empty config for this specific additional listener.
			extensionServer.ServerConfig = types.ServerConfig{}

			// Reassign the map struct.
			d.extensionServers[serverName] = extensionServer
			d.extensionServersMu.Unlock()

			err := d.endpoints.DownByName(serverName)
			if err != nil {
				return err
			}
		}
	}

	// Restart all additional listeners which received an update.
	for serverName, extensionServerConfig := range configuredServers {
		// Lock the entire section to ensure the reassignment can happen.
		d.extensionServersMu.Lock()
		extensionServer, ok := d.extensionServers[serverName]
		if !ok {
			// There isn't any additional listener set that matches this name.
			d.extensionServersMu.Unlock()
			continue
		}

		modified := false
		if extensionServer.ServerConfig != extensionServerConfig {
			modified = true
		}

		extensionServer.ServerConfig = extensionServerConfig

		// Reassign the map struct.
		d.extensionServers[serverName] = extensionServer
		d.extensionServersMu.Unlock()

		// Stop the additional listener in case it got modified.
		if modified {
			err := d.endpoints.DownByName(serverName)
			if err != nil {
				return err
			}
		}
	}

	// Start any additional listener.
	// This operation is idempotent.
	err := d.addExtensionServers(false, d.ClusterCert(), d.Address().URL.Host)
	if err != nil {
		return err
	}

	return nil
}

// startUnixServer starts up the core unix listener with the given resources.
func (d *Daemon) startUnixServer(serverEndpoints []rest.Resources) error {
	ctlServer := d.initServer(serverEndpoints...)
	ctl := endpoints.NewSocket(d.shutdownCtx, ctlServer, d.os.ControlSocket(), d.os.SocketGroup)
	d.endpoints = endpoints.NewEndpoints(d.shutdownCtx, map[string]endpoints.Endpoint{
		endpoints.EndpointsUnix: ctl,
	})

	return d.endpoints.Up()
}

// addCoreServers initializes the default resources with the default address and certificate.
// If the default address and certificate may be applied to any extension servers, those will be started as well.
func (d *Daemon) addCoreServers(preInit bool, defaultURL api.URL, defaultCert *shared.CertInfo, defaultResources []rest.Resources) error {
	serverEndpoints := []rest.Resources{}
	serverEndpoints = append(serverEndpoints, defaultResources...)

	// Append all extension servers whose address is empty or matches the default URL.
	d.extensionServersMu.RLock()
	for _, s := range d.extensionServers {
		// If the server is not available prior to initialization, then skip it if we are before initialization.
		if !s.PreInit && preInit {
			continue
		}

		// If the Server resources are not part of the core API, then skip it.
		if !s.CoreAPI {
			continue
		}

		serverEndpoints = append(serverEndpoints, s.Resources...)
	}

	d.extensionServersMu.RUnlock()

	server := d.initServer(serverEndpoints...)
	network := endpoints.NewNetwork(d.shutdownCtx, endpoints.EndpointNetwork, server, defaultURL, defaultCert)

	return d.endpoints.Add(map[string]endpoints.Endpoint{
		endpoints.EndpointsCore: network,
	})
}

// addExtensionServers initialises a new *endpoints.Network for each extension server and adds it to the Daemon endpoints.
// Only servers with a defined address will be started.
// If a server lacks a certificate, the fallbackCert will be used instead.
// The function is idempotent and doesn't start already running extension servers.
func (d *Daemon) addExtensionServers(preInit bool, fallbackCert *shared.CertInfo, coreAddress string) error {
	var networks = make(map[string]endpoints.Endpoint)
	d.extensionServersMu.RLock()
	for serverName, extensionServer := range d.extensionServers {
		// Skip any core API servers.
		if extensionServer.CoreAPI {
			continue
		}

		// If we are before initialization, only start the servers who have `PreInit` set.
		if !extensionServer.PreInit && preInit {
			continue
		}

		// If the server has no defined address, then do not start it as it should have already started with the core servers.
		if extensionServer.Address == (types.AddrPort{}) {
			continue
		}

		// If the server address matches the core address, then it should have already started.
		if extensionServer.Address.String() == coreAddress {
			continue
		}

		url := api.NewURL().Scheme("https").Host(extensionServer.Address.String())
		alreadyRunning := false
		for name := range d.endpoints.List(endpoints.EndpointNetwork) {
			if name == serverName {
				alreadyRunning = true
				break
			}
		}

		// Skip already running listeners.
		if alreadyRunning {
			continue
		}

		customCertExists := shared.PathExists(filepath.Join(d.os.CertificatesDir, fmt.Sprintf("%s.crt", serverName)))

		var err error
		var cert *shared.CertInfo
		if !extensionServer.DedicatedCertificate && !customCertExists {
			// If there is no certificate defined, apply the default certificate for the core server.
			cert = fallbackCert
		} else {
			// Generate a dedicated certificate or load the custom one if it exists.
			// When updating the additional listeners the dedicated certificate from before will be reused.
			cert, err = shared.KeyPairAndCA(d.os.CertificatesDir, serverName, shared.CertServer, true)
			if err != nil {
				return fmt.Errorf("Failed to setup dedicated certificate for additional server %q: %w", serverName, err)
			}
		}

		server := d.initServer(extensionServer.Resources...)
		network := endpoints.NewNetwork(d.shutdownCtx, endpoints.EndpointNetwork, server, *url, cert)
		networks[serverName] = network
	}

	d.extensionServersMu.RUnlock()

	if len(networks) > 0 {
		err := d.endpoints.Add(networks)
		if err != nil {
			return err
		}
	}

	return nil
}

func (d *Daemon) sendUpgradeNotification(ctx context.Context, c *client.Client) error {
	path := c.URL()
	parts := strings.Split(string(internalTypes.InternalEndpoint), "/")
	parts = append(parts, "database")
	path = *path.Path(parts...)
	upgradeRequest, err := http.NewRequest("PATCH", path.String(), nil)
	if err != nil {
		return err
	}

	upgradeRequest.Header.Set("X-Dqlite-Version", fmt.Sprintf("%d", 1))
	upgradeRequest = upgradeRequest.WithContext(ctx)

	resp, err := c.Client.Do(upgradeRequest)
	if err != nil {
		logger.Error("Failed to send database upgrade request", logger.Ctx{"error": err})
		return nil
	}

	defer resp.Body.Close()
	_, err = io.Copy(io.Discard, resp.Body)
	if err != nil {
		logger.Error("Failed to read upgrade notification response body", logger.Ctx{"error": err})
	}

	if resp.StatusCode != http.StatusOK {
		logger.Errorf("Database upgrade notification failed: %s", resp.Status)
	}

	return nil
}

// ClusterCert ensures both the daemon and state have the same cluster cert.
func (d *Daemon) ClusterCert() *shared.CertInfo {
	d.clusterMu.RLock()
	defer d.clusterMu.RUnlock()

	return shared.NewCertInfo(d.clusterCert.KeyPair(), d.clusterCert.CA(), d.clusterCert.CRL())
}

// ReloadCert reloads a specific certificate from the filesytem.
func (d *Daemon) ReloadCert(name types.CertificateName) error {
	d.clusterMu.Lock()
	defer d.clusterMu.Unlock()

	var dir string
	if name == types.ClusterCertificateName {
		dir = d.os.StateDir
	} else {
		dir = d.os.CertificatesDir
	}

	cert, err := shared.KeyPairAndCA(dir, string(name), shared.CertServer, true)
	if err != nil {
		return fmt.Errorf("Failed to load TLS certificate %q: %w", name, err)
	}

	// In case the cluster certificate gets reloaded also populate its value.
	if name == types.ClusterCertificateName {
		d.clusterCert = cert

		// The core API endpoints are labeled with core.
		// When the cluster certificate gets updated reload those.
		d.endpoints.UpdateTLSByName(endpoints.EndpointsCore, cert)

		// Reload all the other additional listeners that also use the cluster certificate.
		// This might be the case if they
		// - don't use a dedicated certificate
		// - aren't part of the core API
		// - and cannot load a custom certificate which shares their name
		d.extensionServersMu.RLock()
		for name, server := range d.extensionServers {
			certExists := shared.PathExists(filepath.Join(d.os.CertificatesDir, fmt.Sprintf("%s.crt", name)))
			if !server.CoreAPI && !server.DedicatedCertificate && !certExists {
				d.endpoints.UpdateTLSByName(name, cert)
			}
		}

		d.extensionServersMu.RUnlock()
	} else {
		d.endpoints.UpdateTLSByName(string(name), cert)
	}

	return nil
}

// ServerCert ensures both the daemon and state have the same server cert.
func (d *Daemon) ServerCert() *shared.CertInfo {
	return d.serverCert
}

// Address is the listen address for the daemon.
func (d *Daemon) Address() *api.URL {
	return api.NewURL().Scheme("https").Host(d.config.GetAddress().String())
}

// Name is this daemon's cluster member name.
func (d *Daemon) Name() string {
	return d.config.GetName()
}

// Version is provided by the MicroCluster consumer. The daemon includes it in
// its /cluster/1.0 response.
func (d *Daemon) Version() string {
	return d.version
}

// LocalConfig returns the daemon's internal config implementation.
// It is thread safe and can be used to both read and write config.
func (d *Daemon) LocalConfig() *internalConfig.DaemonConfig {
	return d.config
}

// ExtensionServers returns an immutable list of the daemon's additional listeners.
// Only the listeners which can be modified are returned.
// The listeners which are part of the core API are excluded.
func (d *Daemon) ExtensionServers() []string {
	d.extensionServersMu.RLock()
	defer d.extensionServersMu.RUnlock()

	var serverNames []string
	for name, server := range d.extensionServers {
		if server.CoreAPI {
			continue
		}

		serverNames = append(serverNames, name)
	}

	return serverNames
}

// FileSystem returns the filesystem structure for the daemon.
func (d *Daemon) FileSystem() *sys.OS {
	copyOS := *d.os
	return &copyOS
}

// State creates a State instance with the daemon's stateful components.
func (d *Daemon) State() state.State {
	state := &internalState.InternalState{
		Hooks:                    &d.hooks,
		Context:                  d.shutdownCtx,
		ReadyCh:                  d.ReadyChan,
		StartAPI:                 d.StartAPI,
		Extensions:               d.Extensions,
		Endpoints:                d.endpoints,
		UpdateServers:            d.UpdateServers,
		LocalConfig:              d.LocalConfig,
		ReloadCert:               d.ReloadCert,
		InternalFileSystem:       d.FileSystem,
		InternalAddress:          d.Address,
		InternalName:             d.Name,
		InternalVersion:          d.Version,
		InternalServerCert:       d.ServerCert,
		InternalClusterCert:      d.ClusterCert,
		InternalDatabase:         d.db,
		InternalRemotes:          d.trustStore.Remotes,
		InternalExtensionServers: d.ExtensionServers,
		Stop: func() (exit func(), stopErr error) {
			stopErr = d.stop()
			exit = func() {
				d.shutdownDoneCh <- stopErr
			}

			return exit, stopErr
		},
		StopListeners: func() error {
			err := d.fsWatcher.Close()
			if err != nil {
				return err
			}

			return d.endpoints.Down()
		},
	}

	return state
}
