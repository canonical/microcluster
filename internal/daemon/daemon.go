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
	"github.com/gorilla/mux"
	"gopkg.in/yaml.v2"

	"github.com/canonical/microcluster/client"
	"github.com/canonical/microcluster/cluster"
	"github.com/canonical/microcluster/internal/db"
	"github.com/canonical/microcluster/internal/endpoints"
	"github.com/canonical/microcluster/internal/extensions"
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

	address api.URL // Listen Address.
	name    string  // Name of the cluster member.

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

	extensionServers []rest.Server
}

// NewDaemon initializes the Daemon context and channels.
func NewDaemon(project string) *Daemon {
	d := &Daemon{
		shutdownDoneCh: make(chan error),
		ReadyChan:      make(chan struct{}),
		project:        project,
	}

	d.stop = sync.OnceValue(func() error {
		d.shutdownCancel()

		err := d.db.Stop()
		if err != nil {
			return fmt.Errorf("Failed shutting down database: %w", err)
		}

		return d.endpoints.Down()
	})

	return d
}

// Run initializes the Daemon with the given configuration, starts the database, and blocks until the daemon is cancelled.
// - `extensionsSchema` is a list of schema updates in the order that they should be applied.
// - `extensionServers` is a list of rest.Server that will be initialized and managed by microcluster.
// - `hooks` are a set of functions that trigger at certain points during cluster communication.
func (d *Daemon) Run(ctx context.Context, listenPort string, stateDir string, socketGroup string, extensionsSchema []schema.Update, apiExtensions []string, extensionServers []rest.Server, hooks *state.Hooks) error {
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

	isAlreadyRunning, err := d.os.IsControlSocketPresent()
	if err != nil {
		return err
	}

	if isAlreadyRunning {
		return fmt.Errorf("Control socket already present (%q); is another daemon already running?", d.os.ControlSocketPath())
	}

	d.extensionServers = extensionServers

	err = d.init(listenPort, extensionsSchema, apiExtensions, hooks)
	if err != nil {
		return fmt.Errorf("Daemon failed to start: %w", err)
	}

	err = d.hooks.OnStart(d.State())
	if err != nil {
		return fmt.Errorf("Failed to run post-start hook: %w", err)
	}

	close(d.ReadyChan)

	for {
		select {
		case <-ctx.Done():
			return d.stop()
		case err := <-d.shutdownDoneCh:
			return err
		}
	}
}

func (d *Daemon) init(listenPort string, schemaExtensions []schema.Update, apiExtensions []string, hooks *state.Hooks) error {
	d.applyHooks(hooks)

	var err error
	d.name, err = os.Hostname()
	if err != nil {
		return fmt.Errorf("Failed to assign default system name: %w", err)
	}

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

	// Extract user defined endpoints for core listener.
	coreEndpoints, err := resources.GetAndValidateCoreEndpoints(d.extensionServers)
	if err != nil {
		return err
	}

	serverEndpoints := []rest.Resources{
		resources.UnixEndpoints,
		resources.InternalEndpoints,
		resources.PublicEndpoints,
	}
	serverEndpoints = append(serverEndpoints, coreEndpoints...)
	ctlServer := d.initServer(serverEndpoints...)
	ctl := endpoints.NewSocket(d.shutdownCtx, ctlServer, d.os.ControlSocket(), d.os.SocketGroup)
	d.endpoints = endpoints.NewEndpoints(d.shutdownCtx, ctl)
	err = d.endpoints.Up()
	if err != nil {
		return err
	}

	if listenPort != "" {
		serverEndpoints = []rest.Resources{resources.PublicEndpoints}
		serverEndpoints = append(serverEndpoints, coreEndpoints...)
		server := d.initServer(serverEndpoints...)
		url := api.NewURL().Host(fmt.Sprintf(":%s", listenPort))
		network := endpoints.NewNetwork(d.shutdownCtx, endpoints.EndpointNetwork, server, *url, d.serverCert)
		err = d.endpoints.Add(network)
		if err != nil {
			return err
		}
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
	noOpHook := func(s state.State) error { return nil }
	noOpRemoveHook := func(s state.State, force bool) error { return nil }
	noOpInitHook := func(s state.State, initConfig map[string]string) error { return nil }

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
		d.hooks.OnNewMember = noOpHook
	}

	if d.hooks.PreRemove == nil {
		d.hooks.PreRemove = noOpRemoveHook
	}

	if d.hooks.PostRemove == nil {
		d.hooks.PostRemove = noOpRemoveHook
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

	err = d.setDaemonConfig(nil)
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
		err := d.setDaemonConfig(newConfig)
		if err != nil {
			return fmt.Errorf("Failed to apply and save new daemon configuration: %w", err)
		}
	}

	if bootstrap {
		err := d.hooks.PreBootstrap(d.State(), initConfig)
		if err != nil {
			return fmt.Errorf("Failed to run pre-bootstrap hook before starting the API: %w", err)
		}
	}

	if d.address.URL.Host == "" || d.name == "" {
		return fmt.Errorf("Cannot start network API without valid daemon configuration")
	}

	serverCert, err := d.serverCert.PublicKeyX509()
	if err != nil {
		return fmt.Errorf("Failed to parse server certificate when bootstrapping API: %w", err)
	}

	addrPort, err := types.ParseAddrPort(d.address.URL.Host)
	if err != nil {
		return fmt.Errorf("Failed to parse listen address when bootstrapping API: %w", err)
	}

	localNode := trust.Remote{
		Location:    trust.Location{Name: d.name, Address: addrPort},
		Certificate: types.X509Certificate{Certificate: serverCert},
	}

	if bootstrap {
		err = d.trustStore.Remotes().Add(d.os.TrustDir, localNode)
		if err != nil {
			return fmt.Errorf("Failed to initialize local remote entry: %w", err)
		}
	}

	err = d.ReloadClusterCert()
	if err != nil {
		return err
	}

	// Extract user defined endpoints for core listener.
	coreEndpoints, err := resources.GetAndValidateCoreEndpoints(d.extensionServers)
	if err != nil {
		return err
	}

	serverEndpoints := []rest.Resources{resources.InternalEndpoints, resources.PublicEndpoints}
	serverEndpoints = append(serverEndpoints, coreEndpoints...)
	server := d.initServer(serverEndpoints...)
	network := endpoints.NewNetwork(d.shutdownCtx, endpoints.EndpointNetwork, server, d.address, d.ClusterCert())
	err = d.endpoints.Down(endpoints.EndpointNetwork)
	if err != nil {
		return err
	}

	err = d.endpoints.Add(network)
	if err != nil {
		return err
	}

	// If bootstrapping the first node, just open the database and create an entry for ourselves.
	if bootstrap {
		clusterMember := cluster.InternalClusterMember{
			Name:        localNode.Name,
			Address:     localNode.Address.String(),
			Certificate: localNode.Certificate.String(),
			Heartbeat:   time.Time{},
			Role:        cluster.Pending,
		}

		clusterMember.SchemaInternal, clusterMember.SchemaExternal = d.db.Schema().Version()

		err = d.db.Bootstrap(d.Extensions, d.project, d.address, clusterMember)
		if err != nil {
			return err
		}

		err = d.trustStore.Refresh()
		if err != nil {
			return err
		}

		// Add extension servers before post-bootstrap hook.
		err = d.addExtensionServers()
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
		err = d.db.Join(d.Extensions, d.project, d.address, joinAddresses...)
		if err != nil {
			return fmt.Errorf("Failed to join cluster: %w", err)
		}
	} else {
		err = d.db.StartWithCluster(d.Extensions, d.project, d.address, d.trustStore.Remotes().Addresses())
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

	localMemberInfo := internalTypes.ClusterMemberLocal{Name: localNode.Name, Address: localNode.Address, Certificate: localNode.Certificate}
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
			if d.address.URL.Host == c.URL().URL.Host {
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
		if d.address.URL.Host == c.URL().URL.Host {
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
			err = internalClient.RunNewMemberHook(ctx, c.Client.UseTarget(remote.Name), internalTypes.HookNewMemberOptions{Name: localMemberInfo.Name})
			if err != nil && err.Error() != "Daemon not yet initialized" {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return err
	}

	// Add extension servers before post-join hook.
	err = d.addExtensionServers()
	if err != nil {
		return err
	}

	if len(joinAddresses) > 0 {
		return d.hooks.PostJoin(d.State(), initConfig)
	}

	return nil
}

// addExtensionServers initialises a new *endpoints.Network for each extension server and adds it to the Daemon endpoints.
func (d *Daemon) addExtensionServers() error {
	var networks []endpoints.Endpoint
	for _, extensionServer := range d.extensionServers {
		if extensionServer.CoreAPI {
			continue
		}

		cert := extensionServer.Certificate
		if cert == nil {
			cert = d.ClusterCert()
		}

		server := d.initServer(extensionServer.Resources...)
		url := api.NewURL().Scheme(extensionServer.Protocol).Host(extensionServer.Address.String())
		network := endpoints.NewNetwork(d.shutdownCtx, endpoints.EndpointNetwork, server, *url, cert)
		networks = append(networks, network)
	}

	err := d.endpoints.Add(networks...)
	if err != nil {
		return err
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

// ReloadClusterCert reloads the cluster keypair from the state directory.
func (d *Daemon) ReloadClusterCert() error {
	d.clusterMu.Lock()
	defer d.clusterMu.Unlock()

	clusterCert, err := util.LoadClusterCert(d.os.StateDir)
	if err != nil {
		return err
	}

	d.clusterCert = clusterCert
	d.endpoints.UpdateTLS(clusterCert)

	return nil
}

// ServerCert ensures both the daemon and state have the same server cert.
func (d *Daemon) ServerCert() *shared.CertInfo {
	return d.serverCert
}

// Address ensures both the daemon and state have the same address.
func (d *Daemon) Address() *api.URL {
	copyURL := d.address
	return &copyURL
}

// Name ensures both the daemon and state have the same name.
func (d *Daemon) Name() string {
	return d.name
}

// FileSystem returns the filesystem structure for the daemon.
func (d *Daemon) FileSystem() *sys.OS {
	copyOS := *d.os
	return &copyOS
}

// State creates a State instance with the daemon's stateful components.
func (d *Daemon) State() state.State {
	internalState.ReloadClusterCert = d.ReloadClusterCert
	internalState.StopListeners = func() error {
		err := d.fsWatcher.Close()
		if err != nil {
			return err
		}

		return d.endpoints.Down()
	}

	state := &internalState.InternalState{
		Context:             d.shutdownCtx,
		ReadyCh:             d.ReadyChan,
		StartAPI:            d.StartAPI,
		Endpoints:           d.endpoints,
		InternalFileSystem:  d.FileSystem,
		InternalAddress:     d.Address,
		InternalName:        d.Name,
		InternalServerCert:  d.ServerCert,
		InternalClusterCert: d.ClusterCert,
		InternalDatabase:    d.db,
		InternalRemotes:     d.trustStore.Remotes,
		Stop: func() (exit func(), stopErr error) {
			stopErr = d.stop()
			exit = func() {
				d.shutdownDoneCh <- stopErr
			}

			return exit, stopErr
		},
		Extensions: d.Extensions,
	}

	return state
}

// setDaemonConfig sets the daemon's address and name from the given location information. If none is supplied, the file
// at `state-dir/daemon.yaml` will be read for the information.
func (d *Daemon) setDaemonConfig(config *trust.Location) error {
	if config != nil {
		bytes, err := yaml.Marshal(config)
		if err != nil {
			return fmt.Errorf("Failed to parse daemon config to yaml: %w", err)
		}

		err = os.WriteFile(filepath.Join(d.os.StateDir, "daemon.yaml"), bytes, 0644)
		if err != nil {
			return fmt.Errorf("Failed to write daemon configuration yaml: %w", err)
		}
	} else {
		data, err := os.ReadFile(filepath.Join(d.os.StateDir, "daemon.yaml"))
		if err != nil {
			return fmt.Errorf("Failed to find daemon configuration: %w", err)
		}

		config = &trust.Location{}
		err = yaml.Unmarshal(data, config)
		if err != nil {
			return fmt.Errorf("Failed to parse daemon config from yaml: %w", err)
		}
	}

	d.address = *api.NewURL().Scheme("https").Host(config.Address.String())
	d.name = config.Name

	return nil
}
