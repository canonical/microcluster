package microcluster

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"os/signal"
	"time"

	"github.com/lxc/lxd/lxd/db/schema"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
	"golang.org/x/sys/unix"

	"github.com/canonical/microcluster/client"
	"github.com/canonical/microcluster/internal/daemon"
	internalClient "github.com/canonical/microcluster/internal/rest/client"
	"github.com/canonical/microcluster/internal/rest/types"
	"github.com/canonical/microcluster/internal/sys"
	"github.com/canonical/microcluster/rest"
)

// MicroCluster contains some basic filesystem information for interacting with the MicroCluster daemon.
type MicroCluster struct {
	FileSystem *sys.OS

	debug   bool
	verbose bool
	ctx     context.Context
}

// App returns an instance of MicroCluster with a newly initialized filesystem if one does not exist.
func App(ctx context.Context, stateDir string, verbose bool, debug bool) (*MicroCluster, error) {
	os, err := sys.DefaultOS(stateDir, true)
	if err != nil {
		return nil, err
	}

	return &MicroCluster{
		FileSystem: os,
		ctx:        ctx,
		verbose:    verbose,
		debug:      debug,
	}, nil
}

// Start starts up a brand new MicroCluster daemon. Only the local control socket will be available at this stage, no
// database exists yet. Any api or schema extensions can be applied here.
func (m *MicroCluster) Start(listenAddr string, apiEndpoints []rest.Endpoint, schemaExtensions map[int]schema.Update) error {
	// Initialize the logger.
	err := logger.InitLogger(m.FileSystem.LogFile, "", m.verbose, m.debug, nil)
	if err != nil {
		return err
	}

	// Start up a daemon with a basic control socket.
	defer logger.Info("Daemon stopped")
	d := daemon.NewDaemon(m.ctx)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, unix.SIGPWR)
	signal.Notify(sigCh, unix.SIGINT)
	signal.Notify(sigCh, unix.SIGQUIT)
	signal.Notify(sigCh, unix.SIGTERM)

	chIgnore := make(chan os.Signal, 1)
	signal.Notify(chIgnore, unix.SIGHUP)

	err = d.Init(listenAddr, m.FileSystem.StateDir, apiEndpoints, schemaExtensions)
	if err != nil {
		return fmt.Errorf("Unable to start daemon: %w", err)
	}

	for {
		select {
		case sig := <-sigCh:
			logCtx := logger.AddContext(nil, logger.Ctx{"signal": sig})
			logCtx.Info("Received signal")
			if d.ShutdownCtx.Err() != nil {
				logCtx.Warn("Ignoring signal, shutdown already in progress")
			} else {
				go func() {
					d.ShutdownDoneCh <- d.Stop()
				}()
			}

		case err = <-d.ShutdownDoneCh:
			return err
		}
	}
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
func (m *MicroCluster) NewCluster() error {
	c, err := m.LocalClient()
	if err != nil {
		return err
	}

	return c.ControlDaemon(m.ctx, types.Control{Bootstrap: true})
}

// JoinCluster joins an existing cluster with a join token supplied by an existing cluster member.
func (m *MicroCluster) JoinCluster(token string) error {
	c, err := m.LocalClient()
	if err != nil {
		return err
	}

	return c.ControlDaemon(m.ctx, types.Control{JoinToken: token})
}

// NewJoinToken creates and records a new join token containing all the necessary credentials for joining a cluster.
// Join tokens are tied to the server certificate of the joining node, and will be deleted once the node has joined the
// cluster.
func (m *MicroCluster) NewJoinToken(joinerCert string) (string, error) {
	cert, err := shared.ReadCert(joinerCert)
	if err != nil {
		return "", fmt.Errorf("Failed to read server certificate: %w", err)
	}

	c, err := internalClient.New(m.FileSystem.ControlSocket(), nil, nil, false)
	if err != nil {
		return "", err
	}

	secret, err := c.RequestToken(m.ctx, shared.CertFingerprint(cert))
	if err != nil {
		return "", err
	}

	return secret, nil
}

// LocalClient returns a client connected to the local control socket.
func (m *MicroCluster) LocalClient() (*client.Client, error) {
	c, err := internalClient.New(m.FileSystem.ControlSocket(), nil, nil, false)
	if err != nil {
		return nil, err
	}

	return &client.Client{Client: *c}, nil
}

// Client gets a client for the specified cluster member URL. The filesystem will be parsed for the cluster and server
// certificates.
func (m *MicroCluster) Client(address string) (*client.Client, error) {
	serverCert, err := m.FileSystem.ServerCert()
	if err != nil {
		return nil, err
	}

	clusterCert, err := m.FileSystem.ClusterCert()
	if err != nil {
		return nil, err
	}

	publicKey, err := clusterCert.PublicKeyX509()
	if err != nil {
		return nil, err
	}

	url := api.NewURL().Scheme("https").Host(address)
	c, err := internalClient.New(*url, serverCert, publicKey, false)
	if err != nil {
		return nil, err
	}

	return &client.Client{Client: *c}, nil
}

// SQL performs either a GET or POST on /internal/sql with a given query. This is a useful helper for using direct SQL.
func (m *MicroCluster) SQL(query string) (string, *types.SQLBatch, error) {
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

	data := types.SQLQuery{
		Query: query,
	}

	batch, err := c.PostSQL(context.Background(), data)

	return "", batch, err
}
