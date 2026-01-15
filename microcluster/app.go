package microcluster

import (
	"context"
	"crypto/x509"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/canonical/lxd/shared/api"
	"golang.org/x/sys/unix"

	"github.com/canonical/microcluster/v3/internal/daemon"
	"github.com/canonical/microcluster/v3/internal/log"
	"github.com/canonical/microcluster/v3/internal/recover"
	internalClient "github.com/canonical/microcluster/v3/internal/rest/client"
	"github.com/canonical/microcluster/v3/internal/sys"
	"github.com/canonical/microcluster/v3/microcluster/types"
)

// DaemonArgs are the data needed to start a MicroCluster daemon.
type DaemonArgs = daemon.Args

// MicroCluster contains some basic filesystem information for interacting with the MicroCluster daemon.
type MicroCluster struct {
	FileSystem types.OS

	args Args
}

// Args contains options for configuring MicroCluster.
type Args struct {
	StateDir string

	// LogHandler can be used to pass a custom logging handler to Microcluster.
	// The handler allows setting options like the log level and output.
	// If none is provided a default handler is used.
	LogHandler slog.Handler

	Client types.Client
	Proxy  func(*http.Request) (*url.URL, error)
}

// App returns an instance of MicroCluster with a newly initialized filesystem if one does not exist.
func App(args Args) (*MicroCluster, error) {
	// Initialize the logging handler if none was provided.
	if args.LogHandler == nil {
		args.LogHandler = slog.NewTextHandler(os.Stdout, nil)
	}

	if args.StateDir == "" {
		return nil, fmt.Errorf("Missing state directory")
	}

	stateDir, err := filepath.Abs(args.StateDir)
	if err != nil {
		return nil, fmt.Errorf("Missing absolute state directory: %w", err)
	}

	os, err := sys.DefaultOS(stateDir, true)
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
func (m *MicroCluster) Start(ctx context.Context, daemonArgs DaemonArgs) error {
	logger := m.LoggerFromContext(ctx)

	// Start up a daemon with a basic control socket.
	defer logger.Info("Daemon stopped")
	d := daemon.NewDaemon()

	chIgnore := make(chan os.Signal, 1)
	signal.Notify(chIgnore, unix.SIGHUP)

	ctx, cancel := signal.NotifyContext(ctx, unix.SIGPWR, unix.SIGTERM, unix.SIGINT, unix.SIGQUIT)
	defer cancel()

	// Attach the logger to the parent context.
	ctx = context.WithValue(ctx, log.CtxLogger, logger)

	err := d.Run(ctx, m.FileSystem.StateDir(), daemonArgs)
	if err != nil {
		return fmt.Errorf("Daemon stopped with error: %w", err)
	}

	return nil
}

// Shutdown stops the local Microcluster daemon.
func (m *MicroCluster) Shutdown(ctx context.Context) error {
	c, err := m.LocalClient()
	if err != nil {
		return err
	}

	return internalClient.ShutdownDaemon(ctx, c)
}

// Status returns basic status information about the cluster.
func (m *MicroCluster) Status(ctx context.Context) (*types.Server, error) {
	c, err := m.LocalClient()
	if err != nil {
		return nil, err
	}

	server := types.Server{}
	err = c.Query(ctx, "GET", types.PublicEndpoint, nil, nil, &server)
	if err != nil {
		return nil, fmt.Errorf("Failed to get cluster status: %w", err)
	}

	return &server, nil
}

// Ready waits for the daemon to report it has finished initial setup and is ready to be bootstrapped or join an
// existing cluster.
func (m *MicroCluster) Ready(ctx context.Context) error {
	logger := slog.New(m.args.LogHandler)

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
				logger.Debug(fmt.Sprintf("Connecting to MicroCluster daemon (attempt %d)", i))
			}

			c, err := m.LocalClient()
			if err != nil {
				errLast = err
				if doLog {
					logger.Debug(fmt.Sprintf("Failed connecting to MicroCluster daemon (attempt %d): %v", i, err))
				}

				time.Sleep(500 * time.Millisecond)
				continue
			}

			if doLog {
				logger.Debug(fmt.Sprintf("Checking if MicroCluster daemon is ready (attempt %d)", i))
			}

			err = internalClient.CheckReady(ctx, c)
			if err != nil {
				errLast = err
				if doLog {
					logger.Debug(fmt.Sprintf("Failed to check if MicroCluster daemon is ready (attempt %d): %v", i, err))
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

	return internalClient.ControlDaemon(ctx, c, types.Control{Bootstrap: true, Address: addr, Name: name, InitConfig: config})
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

	return internalClient.ControlDaemon(ctx, c, types.Control{JoinToken: token, Address: addr, Name: name, InitConfig: initConfig})
}

// GetClusterMembers returns a list of cluster members.
func (m *MicroCluster) GetClusterMembers(ctx context.Context) ([]types.ClusterMember, error) {
	c, err := m.LocalClient()
	if err != nil {
		return nil, err
	}

	return internalClient.GetClusterMembers(ctx, c)
}

// GetDqliteClusterMembers retrieves the current local cluster configuration
// (derived from the trust store & dqlite metadata); it does not query the
// database.
// This is primarily intended for modifying the cluster configuration via
// MicroCluster.RecoverFromQuorumLoss.
func (m *MicroCluster) GetDqliteClusterMembers() ([]types.DqliteMember, error) {
	return recover.GetDqliteClusterMembers(m.FileSystem)
}

// RecoverFromQuorumLoss can be used to recover database access when a quorum of
// members is lost and cannot be recovered (e.g. hardware failure).
// This function requires that:
//   - All cluster members' databases are not running
//   - The current member has the most up-to-date raft log (usually the member
//     which was most recently the leader)
//
// RecoverFromQuorumLoss will take a database backup before attempting the
// recovery operation.
//
// RecoverFromQuorumLoss should be invoked _exactly once_ for the entire cluster.
// This function creates a gz-compressed tarball and returns its path. This
// tarball should be manually copied by the user to the state dir of all other
// cluster members.
//
// On start, Microcluster will automatically check for & load the recovery
// tarball. A database backup will be taken before the load.
func (m *MicroCluster) RecoverFromQuorumLoss(members []types.DqliteMember) (string, error) {
	// Double check to make sure the cluster configuration has actually changed
	oldMembers, err := m.GetDqliteClusterMembers()
	if err != nil {
		return "", err
	}

	err = recover.ValidateMemberChanges(oldMembers, members)
	if err != nil {
		return "", err
	}

	// Derive a new context with the central logger attached.
	// As we don't have a running daemon at this stage, we cannot use its context.
	// Instead we use the logger populated for the app which uses the custom handler if supplied.
	ctx := context.WithValue(context.Background(), log.CtxLogger, m.LoggerFromContext(context.Background()))

	return recover.RecoverFromQuorumLoss(ctx, m.FileSystem, members)
}

// NewJoinToken creates and records a new join token containing all the necessary credentials for joining a cluster.
// Join tokens are tied to the server certificate of the joining node, and will be deleted once the node has joined the
// cluster.
func (m *MicroCluster) NewJoinToken(ctx context.Context, name string, expireAfter time.Duration) (string, error) {
	c, err := m.LocalClient()
	if err != nil {
		return "", err
	}

	secret, err := internalClient.RequestToken(ctx, c, name, expireAfter)
	if err != nil {
		return "", err
	}

	return secret, nil
}

// ListJoinTokens lists all the join tokens currently available for use.
func (m *MicroCluster) ListJoinTokens(ctx context.Context) ([]types.TokenRecord, error) {
	c, err := m.LocalClient()
	if err != nil {
		return nil, err
	}

	records, err := internalClient.GetTokenRecords(ctx, c)
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

	err = internalClient.DeleteTokenRecord(ctx, c, name)
	if err != nil {
		return err
	}

	return nil
}

// RemoveClusterMember removes a member from the cluster.
func (m *MicroCluster) RemoveClusterMember(ctx context.Context, name string, force bool) error {
	c, err := m.LocalClient()
	if err != nil {
		return err
	}

	return internalClient.DeleteClusterMember(ctx, c, name, force)
}

// LocalClient returns a client connected to the local control socket.
func (m *MicroCluster) LocalClient() (types.Client, error) {
	c := m.args.Client
	if c == nil {
		url := api.NewURL()
		url.URL = *m.FileSystem.ControlSocket()

		internalClient, err := internalClient.New(*url, nil, nil, false)
		if err != nil {
			return nil, err
		}

		c = internalClient
	}

	if m.args.Proxy != nil {
		tx, ok := c.HTTP().Transport.(*http.Transport)
		if !ok {
			return nil, fmt.Errorf("Invalid underlying client transport, expected %T, got %T", &http.Transport{}, c.HTTP().Transport)
		}

		tx.Proxy = m.args.Proxy
		c.HTTP().Transport = tx
	}

	return c, nil
}

// RemoteClient gets a client for the specified cluster member URL.
// The filesystem will be parsed for the cluster and server certificates.
func (m *MicroCluster) RemoteClient(address string) (types.Client, error) {
	var publicKey *x509.Certificate
	clusterCert, err := m.FileSystem.ClusterCert()
	if err == nil {
		publicKey, err = clusterCert.PublicKeyX509()
		if err != nil {
			return nil, err
		}
	}

	return m.RemoteClientWithCert(address, publicKey)
}

// RemoteClientWithCert gets a client for the specified cluster member URL using the remote server cert.
// The filesystem will be parsed for the server client certificate.
func (m *MicroCluster) RemoteClientWithCert(address string, cert *x509.Certificate) (types.Client, error) {
	c := m.args.Client
	if c == nil {
		serverCert, err := m.FileSystem.ServerCert()
		if err != nil {
			return nil, err
		}

		url := api.NewURL().Scheme("https").Host(address)
		internalClient, err := internalClient.New(*url, serverCert, cert, false)
		if err != nil {
			return nil, err
		}

		c = internalClient
	}

	if m.args.Proxy != nil {
		tx, ok := c.HTTP().Transport.(*http.Transport)
		if !ok {
			return nil, fmt.Errorf("Invalid underlying client transport, expected %T, got %T", &http.Transport{}, c.HTTP().Transport)
		}

		tx.Proxy = m.args.Proxy
		c.HTTP().Transport = tx
	}

	return c, nil
}

// SQL performs either a GET or POST on /internal/sql with a given query. This is a useful helper for using direct SQL.
func (m *MicroCluster) SQL(ctx context.Context, query string) (string, *types.SQLBatch, error) {
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
		dump, err := internalClient.GetSQL(ctx, c, query == ".schema")
		if err != nil {
			return "", nil, fmt.Errorf("failed to parse dump response: %w", err)
		}

		return dump.Text, nil, nil
	}

	data := types.SQLQuery{
		Query: query,
	}

	batch, err := internalClient.PostSQL(ctx, c, data)

	return "", batch, err
}

// LoggerFromContext returns a logger instance using the provided logging handler.
// A default handler is used if none is provided.
// If the context doesn't contain a logger, a new one is returned instead.
func (m *MicroCluster) LoggerFromContext(ctx context.Context) *slog.Logger {
	logger, err := log.LoggerFromContext(ctx)
	if err != nil {
		logger := slog.New(m.args.LogHandler)
		logger.Warn("Failed to get logger from context", slog.String("error", err.Error()))

		// If the logger cannot be retrieved from the context, return a new logger with the defined logging handler.
		return logger
	}

	return logger
}

// UpdateServers updates the extension servers defined when starting the daemon.
func (m *MicroCluster) UpdateServers(ctx context.Context, config map[string]types.ServerConfig) error {
	c, err := m.LocalClient()
	if err != nil {
		return err
	}

	return internalClient.UpdateServers(ctx, c, config)
}

// UpdateCertificates updates the named certificate of either the core or extension server.
func (m *MicroCluster) UpdateCertificates(ctx context.Context, name types.CertificateName, args types.KeyPair) error {
	c, err := m.LocalClient()
	if err != nil {
		return err
	}

	return internalClient.UpdateCertificate(ctx, c, name, args)
}
