package microcluster

import (
	"context"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/canonical/lxd/lxd/db/schema"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"golang.org/x/sys/unix"

	"github.com/canonical/microcluster/client"
	"github.com/canonical/microcluster/cluster"
	"github.com/canonical/microcluster/internal/daemon"
	internalClient "github.com/canonical/microcluster/internal/rest/client"
	internalTypes "github.com/canonical/microcluster/internal/rest/types"
	"github.com/canonical/microcluster/internal/sys"
	"github.com/canonical/microcluster/rest"
	"github.com/canonical/microcluster/rest/types"
	"github.com/canonical/microcluster/state"
)

// MicroCluster contains some basic filesystem information for interacting with the MicroCluster daemon.
type MicroCluster struct {
	FileSystem *sys.OS

	args Args
}

// Args contains options for configuring MicroCluster.
type Args struct {
	Verbose     bool
	Debug       bool
	StateDir    string
	SocketGroup string

	ListenPort string
	Client     *client.Client
	Proxy      func(*http.Request) (*url.URL, error)

	ExtensionServers []rest.Server
}

// App returns an instance of MicroCluster with a newly initialized filesystem if one does not exist.
func App(args Args) (*MicroCluster, error) {
	if args.StateDir == "" {
		return nil, fmt.Errorf("Missing state directory")
	}
	stateDir, err := filepath.Abs(args.StateDir)
	if err != nil {
		return nil, fmt.Errorf("Missing absolute state directory: %w", err)
	}
	os, err := sys.DefaultOS(stateDir, args.SocketGroup, true)
	if err != nil {
		return nil, err
	}

	return &MicroCluster{
		FileSystem: os,
		args:       args,
	}, nil
}

// Start starts up a brand new MicroCluster daemon. Only the local control socket will be available at this stage, no
// database exists yet. Any api or schema extensions can be applied here.
// - `extensionsSchema` is a list of schema updates in the order that they should be applied.
// - `hooks` are a set of functions that trigger at certain points during cluster communication.
func (m *MicroCluster) Start(ctx context.Context, extensionsSchema []schema.Update, apiExtensions []string, hooks *state.Hooks) error {
	// Initialize the logger.
	err := logger.InitLogger(m.FileSystem.LogFile, "", m.args.Verbose, m.args.Debug, nil)
	if err != nil {
		return err
	}

	// Start up a daemon with a basic control socket.
	defer logger.Info("Daemon stopped")
	d := daemon.NewDaemon(cluster.GetCallerProject())

	chIgnore := make(chan os.Signal, 1)
	signal.Notify(chIgnore, unix.SIGHUP)

	ctx, cancel := signal.NotifyContext(ctx, unix.SIGPWR, unix.SIGTERM, unix.SIGINT, unix.SIGQUIT)
	defer cancel()

	err = d.Run(ctx, m.args.ListenPort, m.FileSystem.StateDir, m.FileSystem.SocketGroup, extensionsSchema, apiExtensions, m.args.ExtensionServers, hooks)
	if err != nil {
		return fmt.Errorf("Daemon stopped with error: %w", err)
	}

	return nil
}

// Status returns basic status information about the cluster.
func (m *MicroCluster) Status(ctx context.Context) (*internalTypes.Server, error) {
	c, err := m.LocalClient()
	if err != nil {
		return nil, err
	}

	server := internalTypes.Server{}
	err = c.QueryStruct(ctx, "GET", internalTypes.PublicEndpoint, nil, nil, &server)
	if err != nil {
		return nil, fmt.Errorf("Failed to get cluster status: %w", err)
	}

	return &server, nil
}

// Ready waits for the daemon to report it has finished initial setup and is ready to be bootstrapped or join an
// existing cluster.
func (m *MicroCluster) Ready(ctx context.Context) error {
	finger := make(chan error, 1)
	var errLast error
	go func() {
		for i := 0; ; i++ {
			// Start logging only after the 10'th attempt (about 5
			// seconds). Then after the 30'th attempt (about 15
			// seconds), log only only one attempt every 10
			// attempts (about 5 seconds), to avoid being too
			// verbose.
			doLog := false
			if i > 10 {
				doLog = i < 30 || ((i % 10) == 0)
			}

			if doLog {
				logger.Debugf("Connecting to MicroCluster daemon (attempt %d)", i)
			}

			c, err := m.LocalClient()
			if err != nil {
				errLast = err
				if doLog {
					logger.Debugf("Failed connecting to MicroCluster daemon (attempt %d): %v", i, err)
				}

				time.Sleep(500 * time.Millisecond)
				continue
			}

			if doLog {
				logger.Debugf("Checking if MicroCluster daemon is ready (attempt %d)", i)
			}

			err = c.CheckReady(ctx)
			if err != nil {
				errLast = err
				if doLog {
					logger.Debugf("Failed to check if MicroCluster daemon is ready (attempt %d): %v", i, err)
				}

				time.Sleep(500 * time.Millisecond)
				continue
			}

			finger <- nil
			return
		}
	}()

	select {
	case <-finger:
	case <-ctx.Done():
		return fmt.Errorf("MicroCluster still not running after context deadline exceeded: %w", errLast)
	}

	return nil
}

// NewCluster bootstrapps a brand new cluster with this daemon as its only member.
func (m *MicroCluster) NewCluster(ctx context.Context, name string, address string, config map[string]string) error {
	c, err := m.LocalClient()
	if err != nil {
		return err
	}

	addr, err := types.ParseAddrPort(address)
	if err != nil {
		return fmt.Errorf("Received invalid address %q: %w", address, err)
	}

	return c.ControlDaemon(ctx, internalTypes.Control{Bootstrap: true, Address: addr, Name: name, InitConfig: config})
}

// JoinCluster joins an existing cluster with a join token supplied by an existing cluster member.
func (m *MicroCluster) JoinCluster(ctx context.Context, name string, address string, token string, initConfig map[string]string) error {
	c, err := m.LocalClient()
	if err != nil {
		return err
	}

	addr, err := types.ParseAddrPort(address)
	if err != nil {
		return fmt.Errorf("Received invalid address %q: %w", address, err)
	}

	return c.ControlDaemon(ctx, internalTypes.Control{JoinToken: token, Address: addr, Name: name, InitConfig: initConfig})
}

// NewJoinToken creates and records a new join token containing all the necessary credentials for joining a cluster.
// Join tokens are tied to the server certificate of the joining node, and will be deleted once the node has joined the
// cluster.
func (m *MicroCluster) NewJoinToken(ctx context.Context, name string) (string, error) {
	c, err := m.LocalClient()
	if err != nil {
		return "", err
	}

	secret, err := c.RequestToken(ctx, name)
	if err != nil {
		return "", err
	}

	return secret, nil
}

// ListJoinTokens lists all the join tokens currently available for use.
func (m *MicroCluster) ListJoinTokens(ctx context.Context) ([]internalTypes.TokenRecord, error) {
	c, err := m.LocalClient()
	if err != nil {
		return nil, err
	}

	records, err := c.GetTokenRecords(ctx)
	if err != nil {
		return nil, err
	}

	return records, nil
}

// RevokeJoinToken revokes the token record stored under the given name.
func (m *MicroCluster) RevokeJoinToken(ctx context.Context, name string) error {
	c, err := m.LocalClient()
	if err != nil {
		return err
	}

	err = c.DeleteTokenRecord(ctx, name)
	if err != nil {
		return err
	}

	return nil
}

// LocalClient returns a client connected to the local control socket.
func (m *MicroCluster) LocalClient() (*client.Client, error) {
	c := m.args.Client
	if c == nil {
		internalClient, err := internalClient.New(m.FileSystem.ControlSocket(), nil, nil, false)
		if err != nil {
			return nil, err
		}

		c = &client.Client{Client: *internalClient}
	}

	if m.args.Proxy != nil {
		tx, ok := c.Client.Client.Transport.(*http.Transport)
		if !ok {
			return nil, fmt.Errorf("Invalid underlying client transport, expected %T, got %T", &http.Transport{}, c.Client.Client.Transport)
		}

		tx.Proxy = m.args.Proxy
		c.Client.Client.Transport = tx
	}

	return c, nil
}

// RemoteClient gets a client for the specified cluster member URL.
// The filesystem will be parsed for the cluster and server certificates.
func (m *MicroCluster) RemoteClient(address string) (*client.Client, error) {
	c := m.args.Client
	if c == nil {
		serverCert, err := m.FileSystem.ServerCert()
		if err != nil {
			return nil, err
		}

		var publicKey *x509.Certificate
		clusterCert, err := m.FileSystem.ClusterCert()
		if err == nil {
			publicKey, err = clusterCert.PublicKeyX509()
			if err != nil {
				return nil, err
			}
		}

		url := api.NewURL().Scheme("https").Host(address)
		internalClient, err := internalClient.New(*url, serverCert, publicKey, false)
		if err != nil {
			return nil, err
		}

		c = &client.Client{Client: *internalClient}
	}

	if m.args.Proxy != nil {
		tx, ok := c.Client.Client.Transport.(*http.Transport)
		if !ok {
			return nil, fmt.Errorf("Invalid underlying client transport, expected %T, got %T", &http.Transport{}, c.Client.Client.Transport)
		}

		tx.Proxy = m.args.Proxy
		c.Client.Client.Transport = tx
	}

	return c, nil
}

// SQL performs either a GET or POST on /internal/sql with a given query. This is a useful helper for using direct SQL.
func (m *MicroCluster) SQL(ctx context.Context, query string) (string, *internalTypes.SQLBatch, error) {
	if query == "-" {
		// Read from stdin
		bytes, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", nil, fmt.Errorf("Failed to read from stdin: %w", err)
		}

		query = string(bytes)
	}

	c, err := m.LocalClient()
	if err != nil {
		return "", nil, err
	}

	if query == ".dump" || query == ".schema" {
		dump, err := c.GetSQL(ctx, query == ".schema")
		if err != nil {
			return "", nil, fmt.Errorf("failed to parse dump response: %w", err)
		}

		return fmt.Sprintf(dump.Text), nil, nil
	}

	data := internalTypes.SQLQuery{
		Query: query,
	}

	batch, err := c.PostSQL(ctx, data)

	return "", batch, err
}
