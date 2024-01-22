package daemon

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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
	"github.com/canonical/microcluster/config"
	"github.com/canonical/microcluster/internal/db"
	"github.com/canonical/microcluster/internal/endpoints"
	internalREST "github.com/canonical/microcluster/internal/rest"
	internalClient "github.com/canonical/microcluster/internal/rest/client"
	"github.com/canonical/microcluster/internal/rest/resources"
	internalTypes "github.com/canonical/microcluster/internal/rest/types"
	"github.com/canonical/microcluster/internal/state"
	"github.com/canonical/microcluster/internal/sys"
	"github.com/canonical/microcluster/internal/trust"
	"github.com/canonical/microcluster/rest"
	"github.com/canonical/microcluster/rest/types"
)

// Daemon holds information for the microcluster daemon.
type Daemon struct {
	project string // The project refers to the name of the go-project that is calling MicroCluster.

	address api.URL // Listen Address.
	name    string  // Name of the cluster member.

	os          *sys.OS
	serverCert  *shared.CertInfo
	clusterCert *shared.CertInfo

	endpoints *endpoints.Endpoints
	db        *db.DB

	fsWatcher  *sys.Watcher
	trustStore *trust.Store

	hooks config.Hooks // Hooks to be called upon various daemon actions.

	ReadyChan      chan struct{}      // Closed when the daemon is fully ready.
	ShutdownCtx    context.Context    // Cancelled when shutdown starts.
	ShutdownDoneCh chan error         // Receives the result of the d.Stop() function and tells the daemon to end.
	ShutdownCancel context.CancelFunc // Cancels the shutdownCtx to indicate shutdown starting.
}

// NewDaemon initializes the Daemon context and channels.
func NewDaemon(ctx context.Context, project string) *Daemon {
	ctx, cancel := context.WithCancel(ctx)
	return &Daemon{
		ShutdownCtx:    ctx,
		ShutdownCancel: cancel,
		ShutdownDoneCh: make(chan error),
		ReadyChan:      make(chan struct{}),
		project:        project,
	}
}

// Init initializes the Daemon with the given configuration, and starts the database.
func (d *Daemon) Init(listenPort string, stateDir string, socketGroup string, extendedEndpoints []rest.Endpoint, schemaExtensions map[int]schema.Update, hooks *config.Hooks) error {
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

	// TODO: Check if already running.
	d.os, err = sys.DefaultOS(stateDir, socketGroup, true)
	if err != nil {
		return fmt.Errorf("Failed to initialize directory structure: %w", err)
	}

	err = d.init(listenPort, extendedEndpoints, schemaExtensions, hooks)
	if err != nil {
		return fmt.Errorf("Daemon failed to start: %w", err)
	}

	err = d.hooks.OnStart(d.State())
	if err != nil {
		return fmt.Errorf("Failed to run post-start hook: %w", err)
	}

	close(d.ReadyChan)

	return nil
}

func (d *Daemon) init(listenPort string, extendedEndpoints []rest.Endpoint, schemaExtensions map[int]schema.Update, hooks *config.Hooks) error {
	d.applyHooks(hooks)

	var err error
	d.name, err = os.Hostname()
	if err != nil {
		return fmt.Errorf("Failed to assign default system name: %w", err)
	}

	d.serverCert, err = util.LoadServerCert(d.os.StateDir)
	if err != nil {
		return err
	}

	err = d.initStore()
	if err != nil {
		return fmt.Errorf("Failed to initialize trust store: %w", err)
	}

	d.db = db.NewDB(d.ShutdownCtx, d.serverCert, d.os)

	// Apply extensions to API/Schema.
	resources.ExtendedEndpoints.Endpoints = append(resources.ExtendedEndpoints.Endpoints, extendedEndpoints...)

	ctlServer := d.initServer(resources.UnixEndpoints, resources.InternalEndpoints, resources.PublicEndpoints, resources.ExtendedEndpoints)
	ctl := endpoints.NewSocket(d.ShutdownCtx, ctlServer, d.os.ControlSocket(), d.os.SocketGroup)
	d.endpoints = endpoints.NewEndpoints(d.ShutdownCtx, ctl)
	err = d.endpoints.Up()
	if err != nil {
		return err
	}

	if listenPort != "" {
		server := d.initServer(resources.PublicEndpoints, resources.ExtendedEndpoints)
		url := api.NewURL().Host(fmt.Sprintf(":%s", listenPort))
		network := endpoints.NewNetwork(d.ShutdownCtx, endpoints.EndpointNetwork, server, *url, d.serverCert)
		err = d.endpoints.Add(network)
		if err != nil {
			return err
		}
	}

	d.db.SetSchema(schemaExtensions)

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

func (d *Daemon) applyHooks(hooks *config.Hooks) {
	// Apply a no-op hooks for any missing hooks.
	noOpHook := func(s *state.State) error { return nil }
	noOpRemoveHook := func(s *state.State, force bool) error { return nil }
	noOpInitHook := func(s *state.State, initConfig map[string]string) error { return nil }

	if hooks == nil {
		d.hooks = config.Hooks{}
	} else {
		d.hooks = *hooks
	}

	if d.hooks.OnBootstrap == nil {
		d.hooks.OnBootstrap = noOpInitHook
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
	d.fsWatcher, err = sys.NewWatcher(d.ShutdownCtx, d.os.StateDir)
	if err != nil {
		return err
	}

	d.trustStore, err = trust.Init(d.fsWatcher, nil, d.os.TrustDir)
	if err != nil {
		return err
	}

	return nil
}

func (d *Daemon) initServer(resources ...*resources.Resources) *http.Server {
	/* Setup the web server */
	mux := mux.NewRouter()
	mux.StrictSlash(false)
	mux.SkipClean(true)
	mux.UseEncodedPath()

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

	state := d.State()
	for _, endpoints := range resources {
		for _, e := range endpoints.Endpoints {
			internalREST.HandleEndpoint(state, mux, string(endpoints.Path), e)

			for _, alias := range e.Aliases {
				ae := e
				ae.Name = alias.Name
				ae.Path = alias.Path

				internalREST.HandleEndpoint(state, mux, string(endpoints.Path), ae)
			}
		}
	}

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

	d.clusterCert, err = util.LoadClusterCert(d.os.StateDir)
	if err != nil {
		return err
	}

	server := d.initServer(resources.InternalEndpoints, resources.PublicEndpoints, resources.ExtendedEndpoints)
	network := endpoints.NewNetwork(d.ShutdownCtx, endpoints.EndpointNetwork, server, d.address, d.clusterCert)
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
			Schema:      d.db.Schema().Version(),
			Heartbeat:   time.Time{},
			Role:        cluster.Pending,
		}

		err = d.db.Bootstrap(d.project, d.address, d.ClusterCert(), clusterMember)
		if err != nil {
			return err
		}

		err = d.trustStore.Refresh()
		if err != nil {
			return err
		}

		return d.hooks.OnBootstrap(d.State(), initConfig)
	}

	if len(joinAddresses) != 0 {
		err = d.db.Join(d.project, d.address, d.ClusterCert(), joinAddresses...)
		if err != nil {
			return fmt.Errorf("Failed to join cluster: %w", err)
		}
	} else {
		err = d.db.StartWithCluster(d.project, d.address, d.trustStore.Remotes().Addresses(), d.clusterCert)
		if err != nil {
			return fmt.Errorf("Failed to re-establish cluster connection: %w", err)
		}
	}

	err = d.trustStore.Refresh()
	if err != nil {
		return err
	}

	// Get a client for every other cluster member in the newly refreshed local store.
	cluster := make(client.Cluster, 0, d.trustStore.Remotes().Count()-1)
	for _, addr := range d.trustStore.Remotes().Addresses() {
		if d.address.URL.Host == addr.String() {
			continue
		}

		publicKey, err := d.ClusterCert().PublicKeyX509()
		if err != nil {
			return err
		}

		url := api.NewURL().Scheme("https").Host(addr.String())
		c, err := internalClient.New(*url, d.ServerCert(), publicKey, true)
		if err != nil {
			return err
		}

		cluster = append(cluster, client.Client{Client: *c})
	}

	if len(joinAddresses) > 0 {
		err = d.hooks.PreJoin(d.State(), initConfig)
		if err != nil {
			return err
		}
	}

	// Send notification that this node is upgraded to all other cluster members.
	err = cluster.Query(d.ShutdownCtx, true, func(ctx context.Context, c *client.Client) error {
		path := c.URL()
		parts := strings.Split(string(internalClient.InternalEndpoint), "/")
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
			return fmt.Errorf("Database upgrade notification failed: %s", resp.Status)
		}

		if len(joinAddresses) > 0 {
			localMemberInfo := internalTypes.ClusterMemberLocal{Name: localNode.Name, Address: localNode.Address, Certificate: localNode.Certificate}
			_, err = c.AddClusterMember(ctx, internalTypes.ClusterMember{ClusterMemberLocal: localMemberInfo})
			if err != nil {
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

// ClusterCert ensures both the daemon and state have the same cluster cert.
func (d *Daemon) ClusterCert() *shared.CertInfo {
	return d.clusterCert
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

// State creates a State instance with the daemon's stateful components.
func (d *Daemon) State() *state.State {
	state.PreRemoveHook = d.hooks.PreRemove
	state.PostRemoveHook = d.hooks.PostRemove
	state.OnHeartbeatHook = d.hooks.OnHeartbeat
	state.OnNewMemberHook = d.hooks.OnNewMember
	state.StopListeners = func() error {
		err := d.fsWatcher.Close()
		if err != nil {
			return err
		}

		return d.endpoints.Down()
	}

	state := &state.State{
		Context:        d.ShutdownCtx,
		ReadyCh:        d.ReadyChan,
		ShutdownDoneCh: d.ShutdownDoneCh,
		OS:             d.os,
		Address:        d.Address,
		Name:           d.Name,
		Endpoints:      d.endpoints,
		ServerCert:     d.ServerCert,
		ClusterCert:    d.ClusterCert,
		Database:       d.db,
		Remotes:        d.trustStore.Remotes,
		StartAPI:       d.StartAPI,
		Stop:           d.Stop,
	}

	return state
}

// Stop stops the Daemon via its shutdown channel.
func (d *Daemon) Stop() error {
	d.ShutdownCancel()

	err := d.db.Stop()
	if err != nil {
		return fmt.Errorf("Failed shutting down database: %w", err)
	}

	return d.endpoints.Down()
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
