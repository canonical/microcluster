package microcluster

import (
	"context"
	"crypto/x509"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"time"

	"github.com/canonical/lxd/lxd/db/schema"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"golang.org/x/sys/unix"

	"github.com/canonical/microcluster/client"
	"github.com/canonical/microcluster/cluster"
	"github.com/canonical/microcluster/config"
	"github.com/canonical/microcluster/internal/daemon"
	internalClient "github.com/canonical/microcluster/internal/rest/client"
	internalTypes "github.com/canonical/microcluster/internal/rest/types"
	"github.com/canonical/microcluster/internal/sys"
	"github.com/canonical/microcluster/rest"
	"github.com/canonical/microcluster/rest/types"
)

// MicroCluster contains some basic filesystem information for interacting with the MicroCluster daemon.
type MicroCluster struct {
	FileSystem *sys.OS
	ctx        context.Context

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
}

// App returns an instance of MicroCluster with a newly initialized filesystem if one does not exist.
func App(ctx context.Context, args Args) (*MicroCluster, error) {
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
		ctx:        ctx,
		args:       args,
	}, nil
}

// Start starts up a brand new MicroCluster daemon. Only the local control socket will be available at this stage, no
// database exists yet. Any api or schema extensions can be applied here.
func (m *MicroCluster) Start(apiEndpoints []rest.Endpoint, schemaExtensions map[int]schema.Update, hooks *config.Hooks) error {
	// Initialize the logger.
	err := logger.InitLogger(m.FileSystem.LogFile, "", m.args.Verbose, m.args.Debug, nil)
	if err != nil {
		return err
	}

	// Start up a daemon with a basic control socket.
	defer logger.Info("Daemon stopped")
	d := daemon.NewDaemon(m.ctx, cluster.GetCallerProject())

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, unix.SIGPWR)
	signal.Notify(sigCh, unix.SIGINT)
	signal.Notify(sigCh, unix.SIGQUIT)
	signal.Notify(sigCh, unix.SIGTERM)

	chIgnore := make(chan os.Signal, 1)
	signal.Notify(chIgnore, unix.SIGHUP)

	err = d.Init(m.args.ListenPort, m.FileSystem.StateDir, m.FileSystem.SocketGroup, apiEndpoints, schemaExtensions, hooks)
	if err != nil {
		return fmt.Errorf("Unable to start daemon: %w", err)
	}

	stop := sync.OnceFunc(func() {
		logger.Info("Initiate daemon stop sequence")
		err := d.ShutdownCtx.Err()
		if stopErr := d.Stop(); stopErr != nil {
			err = stopErr
		}

		d.ShutdownDoneCh <- err
	})

	for {
		select {
		case err = <-d.ShutdownDoneCh:
			close(d.ShutdownDoneCh)
			return err
		case <-d.ShutdownCtx.Done():
			stop()
		case sig := <-sigCh:
			logCtx := logger.AddContext(logger.Ctx{"signal": sig})
			logCtx.Info("Received signal")
			if d.ShutdownCtx.Err() != nil {
				logCtx.Warn("Ignoring signal, shutdown already in progress")
			}
			stop()
		}
	}
}

// Status returns basic status information about the cluster.
func (m *MicroCluster) Status() (*internalTypes.Server, error) {
	c, err := m.LocalClient()
	if err != nil {
		return nil, err
	}

	server := internalTypes.Server{}
	err = c.QueryStruct(m.ctx, "GET", internalClient.PublicEndpoint, nil, nil, &server)
	if err != nil {
		return nil, fmt.Errorf("Failed to get cluster status: %w", err)
	}

	return &server, nil
}

// Ready waits for the daemon to report it has finished initial setup and is ready to be bootstrapped or join an
// existing cluster.
func (m *MicroCluster) Ready(timeoutSeconds int) error {
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

			err = c.CheckReady(m.ctx)
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

	if timeoutSeconds > 0 {
		select {
		case <-finger:
			break
		case <-time.After(time.Second * time.Duration(timeoutSeconds)):
			return fmt.Errorf("MicroCluster still not running after %ds timeout (%v)", timeoutSeconds, errLast)
		}
	} else {
		<-finger
	}

	return nil
}

// NewCluster bootstrapps a brand new cluster with this daemon as its only member.
func (m *MicroCluster) NewCluster(name string, address string, config map[string]string, timeout time.Duration) error {
	c, err := m.LocalClient()
	if err != nil {
		return err
	}

	addr, err := types.ParseAddrPort(address)
	if err != nil {
		return fmt.Errorf("Received invalid address %q: %w", address, err)
	}

	return c.ControlDaemon(m.ctx, internalTypes.Control{Bootstrap: true, Address: addr, Name: name, InitConfig: config}, timeout)
}

// JoinCluster joins an existing cluster with a join token supplied by an existing cluster member.
func (m *MicroCluster) JoinCluster(name string, address string, token string, initConfig map[string]string, timeout time.Duration) error {
	c, err := m.LocalClient()
	if err != nil {
		return err
	}

	addr, err := types.ParseAddrPort(address)
	if err != nil {
		return fmt.Errorf("Received invalid address %q: %w", address, err)
	}

	return c.ControlDaemon(m.ctx, internalTypes.Control{JoinToken: token, Address: addr, Name: name, InitConfig: initConfig}, timeout)
}

// NewJoinToken creates and records a new join token containing all the necessary credentials for joining a cluster.
// Join tokens are tied to the server certificate of the joining node, and will be deleted once the node has joined the
// cluster.
func (m *MicroCluster) NewJoinToken(name string) (string, error) {
	c, err := m.LocalClient()
	if err != nil {
		return "", err
	}

	secret, err := c.RequestToken(m.ctx, name)
	if err != nil {
		return "", err
	}

	return secret, nil
}

// ListJoinTokens lists all the join tokens currently available for use.
func (m *MicroCluster) ListJoinTokens() ([]internalTypes.TokenRecord, error) {
	c, err := m.LocalClient()
	if err != nil {
		return nil, err
	}

	records, err := c.GetTokenRecords(m.ctx)
	if err != nil {
		return nil, err
	}

	return records, nil
}

// RevokeJoinToken revokes the token record stored under the given name.
func (m *MicroCluster) RevokeJoinToken(name string) error {
	c, err := m.LocalClient()
	if err != nil {
		return err
	}

	err = c.DeleteTokenRecord(m.ctx, name)
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
		tx := c.Client.Client.Transport.(*http.Transport)
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
		tx := c.Client.Client.Transport.(*http.Transport)
		tx.Proxy = m.args.Proxy
		c.Client.Client.Transport = tx
	}

	return c, nil
}

// SQL performs either a GET or POST on /internal/sql with a given query. This is a useful helper for using direct SQL.
func (m *MicroCluster) SQL(query string) (string, *internalTypes.SQLBatch, error) {
	if query == "-" {
		// Read from stdin
		bytes, err := ioutil.ReadAll(os.Stdin)
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
		dump, err := c.GetSQL(context.Background(), query == ".schema")
		if err != nil {
			return "", nil, fmt.Errorf("failed to parse dump response: %w", err)
		}

		return fmt.Sprintf(dump.Text), nil, nil
	}

	data := internalTypes.SQLQuery{
		Query: query,
	}

	batch, err := c.PostSQL(context.Background(), data)

	return "", batch, err
}
