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

	"github.com/gorilla/mux"
	"github.com/lxc/lxd/lxd/db/schema"
	"github.com/lxc/lxd/lxd/request"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/validate"

	"github.com/canonical/microcluster/client"
	"github.com/canonical/microcluster/cluster"
	"github.com/canonical/microcluster/internal/db"
	"github.com/canonical/microcluster/internal/db/update"
	"github.com/canonical/microcluster/internal/endpoints"
	internalREST "github.com/canonical/microcluster/internal/rest"
	internalClient "github.com/canonical/microcluster/internal/rest/client"
	"github.com/canonical/microcluster/internal/rest/resources"
	"github.com/canonical/microcluster/internal/state"
	"github.com/canonical/microcluster/internal/sys"
	"github.com/canonical/microcluster/internal/trust"
	"github.com/canonical/microcluster/rest"
	"github.com/canonical/microcluster/rest/types"
)

// Daemon holds information for the microcluster daemon.
type Daemon struct {
	Address api.URL // Listen Address.

	os          *sys.OS
	serverCert  *shared.CertInfo
	clusterCert *shared.CertInfo

	endpoints *endpoints.Endpoints
	db        *db.DB

	fsWatcher  *sys.Watcher
	trustStore *trust.Store

	ReadyChan      chan struct{}      // Closed when the daemon is fully ready.
	ShutdownCtx    context.Context    // Cancelled when shutdown starts.
	ShutdownDoneCh chan error         // Receives the result of the d.Stop() function and tells the daemon to end.
	ShutdownCancel context.CancelFunc // Cancels the shutdownCtx to indicate shutdown starting.
}

// NewDaemon initializes the Daemon context and channels.
func NewDaemon(ctx context.Context) *Daemon {
	ctx, cancel := context.WithCancel(ctx)
	return &Daemon{
		ShutdownCtx:    ctx,
		ShutdownCancel: cancel,
		ShutdownDoneCh: make(chan error),
		ReadyChan:      make(chan struct{}),
	}
}

// Init initializes the Daemon with the given configuration, and starts the database.
func (d *Daemon) Init(addr string, stateDir string, extendedEndpoints []rest.Endpoint, schemaExtensions map[int]schema.Update) error {
	if stateDir == "" {
		stateDir = os.Getenv(sys.StateDir)
	}

	err := d.validateConfig(addr, stateDir)
	if err != nil {
		return fmt.Errorf("Invalid daemon configuration: %w", err)
	}

	// TODO: Check if already running.

	d.os, err = sys.DefaultOS(stateDir, true)
	if err != nil {
		return fmt.Errorf("Failed to initialize directory structure: %w", err)
	}

	err = d.init(extendedEndpoints, schemaExtensions)
	if err != nil {
		return fmt.Errorf("Daemon failed to start: %w", err)
	}

	close(d.ReadyChan)

	return nil
}

func (d *Daemon) init(extendedEndpoints []rest.Endpoint, schemaExtensions map[int]schema.Update) error {
	var err error
	d.serverCert, err = util.LoadServerCert(d.os.StateDir)
	if err != nil {
		return err
	}

	err = d.initStore()
	if err != nil {
		return fmt.Errorf("Failed to initialize trust store: %w", err)
	}

	d.db = db.NewDB(d.ShutdownCtx, d.serverCert, d.os, d.Address)

	ctlServer := d.initServer(resources.ControlEndpoints)
	ctl := endpoints.NewSocket(d.ShutdownCtx, ctlServer, d.os.ControlSocket(), "") // TODO: add socket group.
	d.endpoints = endpoints.NewEndpoints(d.ShutdownCtx, ctl)
	err = d.endpoints.Up()
	if err != nil {
		return err
	}

	// Apply extensions to API/Schema.
	resources.ExtendedEndpoints.Endpoints = append(resources.ExtendedEndpoints.Endpoints, extendedEndpoints...)
	update.AppendSchema(schemaExtensions)

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

func (d *Daemon) reloadIfBootstrapped() error {
	_, err := os.Stat(filepath.Join(d.os.DatabaseDir, "info.yaml"))
	if err != nil {
		if os.IsNotExist(err) {
			logger.Warn("microcluster database is uninitialized")
			return nil
		}

		return err
	}

	err = d.StartAPI(false)
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

func (d *Daemon) validateConfig(addr string, stateDir string) error {
	isListenAddress := validate.IsListenAddress(true, true, false)
	if addr != "" {
		err := isListenAddress(addr)
		if err != nil {
			return fmt.Errorf("Invalid admin address %q: %w", addr, err)
		}

		d.Address = *api.NewURL().Scheme("https").Host(addr)
	}

	if stateDir == "" {
		return fmt.Errorf("State directory must be specified")
	}

	_, err := os.Stat(stateDir)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	return nil
}

// StartAPI starts up the admin and consumer APIs, and generates a cluster cert
// if we are bootstrapping the first node.
func (d *Daemon) StartAPI(bootstrap bool, joinAddresses ...string) error {
	addr, err := types.ParseAddrPort(d.Address.URL.Host)
	if err != nil {
		return fmt.Errorf("Failed to parse listen address when bootstrapping API: %w", err)
	}

	serverCert, err := d.serverCert.PublicKeyX509()
	if err != nil {
		return fmt.Errorf("Failed to parse server certificate when bootstrapping API: %w", err)
	}

	localNode := trust.Remote{
		Name:        filepath.Base(d.os.StateDir),
		Address:     addr,
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
	network := endpoints.NewNetwork(d.ShutdownCtx, endpoints.EndpointNetwork, server, d.Address, d.clusterCert)

	err = d.endpoints.Add(network)
	if err != nil {
		return err
	}

	// If bootstrapping the first node, just open the database and create an entry for ourselves.
	if bootstrap {
		err = d.db.Bootstrap(d.ClusterCert())
		if err != nil {
			return err
		}

		err = d.trustStore.Refresh()
		if err != nil {
			return err
		}

		err = d.db.Transaction(d.ShutdownCtx, func(ctx context.Context, tx *db.Tx) error {
			clusterMember := cluster.InternalClusterMember{
				Name:        localNode.Name,
				Address:     localNode.Address.String(),
				Certificate: localNode.Certificate.String(),
				Schema:      update.Schema().Version(),
				Heartbeat:   time.Time{},
				Role:        cluster.Pending,
			}

			_, err := cluster.CreateInternalClusterMember(ctx, tx, clusterMember)

			return err
		})

		return err
	}

	if len(joinAddresses) != 0 {
		err = d.db.Join(d.ClusterCert(), joinAddresses...)
		if err != nil {
			return fmt.Errorf("Failed to join cluster: %w", err)
		}
	} else {
		err = d.db.StartWithCluster(d.trustStore.Remotes().Addresses(), d.clusterCert)
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
		if d.Address.URL.Host == addr.String() {
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

		return nil
	})

	return err
}

// ClusterCert ensures both the daemon and state have the same cluster cert.
func (d *Daemon) ClusterCert() *shared.CertInfo {
	return d.clusterCert
}

// ServerCert ensures both the daemon and state have the same server cert.
func (d *Daemon) ServerCert() *shared.CertInfo {
	return d.serverCert
}

// State creates a State instance with the daemon's stateful components.
func (d *Daemon) State() *state.State {
	state := &state.State{
		Context:        d.ShutdownCtx,
		ReadyCh:        d.ReadyChan,
		ShutdownDoneCh: d.ShutdownDoneCh,
		OS:             d.os,
		Address:        d.Address,
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
