package db

import (
	"bufio"
	"context"
	"crypto/tls"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	dqlite "github.com/canonical/go-dqlite/app"
	dqliteClient "github.com/canonical/go-dqlite/client"
	"github.com/canonical/lxd/lxd/db/schema"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/revert"
	"github.com/canonical/lxd/shared/tcp"

	"github.com/canonical/microcluster/v2/cluster"
	"github.com/canonical/microcluster/v2/internal/db/update"
	"github.com/canonical/microcluster/v2/internal/extensions"
	internalClient "github.com/canonical/microcluster/v2/internal/rest/client"
	internalTypes "github.com/canonical/microcluster/v2/internal/rest/types"
	"github.com/canonical/microcluster/v2/internal/sys"
	"github.com/canonical/microcluster/v2/rest/types"
)

// DqliteDB holds all information internal to the dqlite database.
type DqliteDB struct {
	memberName  func() string           // Local cluster member name
	clusterCert func() *shared.CertInfo // Cluster certificate for dqlite authentication.
	serverCert  func() *shared.CertInfo // Server certificate for dqlite authentication.
	listenAddr  api.URL                 // Listen address for this dqlite node.

	dbName string // This is db.bin.
	os     *sys.OS

	db        *sql.DB
	dqlite    *dqlite.App
	acceptCh  chan net.Conn
	upgradeCh chan struct{}

	ctx    context.Context
	cancel context.CancelFunc

	heartbeatLock     sync.Mutex
	heartbeatInterval time.Duration
	maxConns          int64

	schema *update.SchemaUpdate

	statusLock sync.RWMutex
	status     types.DatabaseStatus
}

const (
	// DefaultHeartbeatInterval is the default interval used for heartbeats and dqlite role probes.
	DefaultHeartbeatInterval time.Duration = time.Second * 10
)

// Accept sends the outbound connection through the acceptCh channel to be received by dqlite.
func (db *DqliteDB) Accept(conn net.Conn) {
	db.acceptCh <- conn
}

// NewDB creates an empty db struct with no dqlite connection.
func NewDB(ctx context.Context, serverCert func() *shared.CertInfo, clusterCert func() *shared.CertInfo, memberName func() string, os *sys.OS, heartbeatInterval time.Duration) *DqliteDB {
	shutdownCtx, shutdownCancel := context.WithCancel(ctx)

	if heartbeatInterval == 0 {
		heartbeatInterval = DefaultHeartbeatInterval
	}

	return &DqliteDB{
		memberName:        memberName,
		serverCert:        serverCert,
		clusterCert:       clusterCert,
		dbName:            filepath.Base(os.DatabasePath()),
		os:                os,
		acceptCh:          make(chan net.Conn),
		upgradeCh:         make(chan struct{}),
		heartbeatInterval: heartbeatInterval,
		ctx:               shutdownCtx,
		cancel:            shutdownCancel,
		status:            types.DatabaseNotReady,
		maxConns:          1,
	}
}

// SetSchema sets schema and API extensions on the DB.
func (db *DqliteDB) SetSchema(schemaExtensions []schema.Update, apiExtensions extensions.Extensions) {
	s := update.NewSchema()
	s.AppendSchema(schemaExtensions, apiExtensions)
	db.schema = s.Schema()
}

// Schema returns the update.SchemaUpdate for the DB.
func (db *DqliteDB) Schema() *update.SchemaUpdate {
	return db.schema
}

// SchemaVersion returns the current internal and external schema version, as well as all API extensions in memory.
func (db *DqliteDB) SchemaVersion() (versionInternal uint64, versionExternal uint64, apiExtensions extensions.Extensions) {
	return db.schema.Version()
}

// Bootstrap dqlite.
func (db *DqliteDB) Bootstrap(extensions extensions.Extensions, project string, addr api.URL, clusterRecord cluster.CoreClusterMember) error {
	var err error
	db.listenAddr = addr
	db.dqlite, err = dqlite.New(db.os.DatabaseDir,
		dqlite.WithAddress(db.listenAddr.URL.Host),
		dqlite.WithRolesAdjustmentFrequency(db.heartbeatInterval),
		dqlite.WithRolesAdjustmentHook(db.heartbeat),
		dqlite.WithConcurrentLeaderConns(&db.maxConns),
		dqlite.WithExternalConn(db.dialFunc(), db.acceptCh),
		dqlite.WithUnixSocket(os.Getenv(sys.DqliteSocket)))
	if err != nil {
		return fmt.Errorf("Failed to bootstrap dqlite: %w", err)
	}

	err = db.Open(extensions, true, project)
	if err != nil {
		return err
	}

	// Apply initial API extensions on the bootstrap node.
	clusterRecord.APIExtensions = extensions
	err = db.Transaction(db.ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := cluster.CreateCoreClusterMember(ctx, tx, clusterRecord)

		return err
	})
	if err != nil {
		return err
	}

	return nil
}

// Join a dqlite cluster with the address of a member.
func (db *DqliteDB) Join(extensions extensions.Extensions, project string, addr api.URL, joinAddresses ...string) error {
	var err error
	db.listenAddr = addr
	db.dqlite, err = dqlite.New(db.os.DatabaseDir,
		dqlite.WithCluster(joinAddresses),
		dqlite.WithRolesAdjustmentFrequency(db.heartbeatInterval),
		dqlite.WithRolesAdjustmentHook(db.heartbeat),
		dqlite.WithAddress(db.listenAddr.URL.Host),
		dqlite.WithConcurrentLeaderConns(&db.maxConns),
		dqlite.WithExternalConn(db.dialFunc(), db.acceptCh),
		dqlite.WithUnixSocket(os.Getenv(sys.DqliteSocket)))
	if err != nil {
		return fmt.Errorf("Failed to join dqlite cluster %w", err)
	}

	for {
		err := db.Open(extensions, false, project)
		if err == nil {
			break
		}

		// If this is a graceful abort, then we should loop back and try to start the database again.
		if errors.Is(err, schema.ErrGracefulAbort) {
			logger.Debug("Re-attempting schema upgrade and API extension checks", logger.Ctx{"address": db.listenAddr.String()})

			continue
		}

		return err
	}

	return nil
}

// StartWithCluster starts up dqlite and joins the cluster.
func (db *DqliteDB) StartWithCluster(extensions extensions.Extensions, project string, addr api.URL, clusterMembers map[string]types.AddrPort) error {
	allClusterAddrs := []string{}
	for _, clusterMemberAddrs := range clusterMembers {
		allClusterAddrs = append(allClusterAddrs, clusterMemberAddrs.String())
	}

	return db.Join(extensions, project, addr, allClusterAddrs...)
}

// Leader returns a client connected to the leader of the dqlite cluster.
func (db *DqliteDB) Leader(ctx context.Context) (*dqliteClient.Client, error) {
	// Always only try one connection at a time when fetching the leader manually, as this can be an expensive call.
	return db.dqlite.Leader(ctx, dqliteClient.WithConcurrentLeaderConns(1))
}

// Cluster returns information about dqlite cluster members.
func (db *DqliteDB) Cluster(ctx context.Context, client *dqliteClient.Client) ([]dqliteClient.NodeInfo, error) {
	members, err := client.Cluster(ctx)
	if err != nil {
		return nil, fmt.Errorf("Failed to get dqlite cluster information: %w", err)
	}

	return members, nil
}

// Status returns the current status of the database.
func (db *DqliteDB) Status() types.DatabaseStatus {
	if db == nil {
		return types.DatabaseNotReady
	}

	db.statusLock.RLock()
	status := db.status
	db.statusLock.RUnlock()

	return status
}

// IsOpen returns nil  only if the DB has been opened and the schema loaded.
// Otherwise, it returns an error describing why the database is offline.
// The returned error may have the http status 503, indicating that the database is in a valid but unavailable state.
func (db *DqliteDB) IsOpen(ctx context.Context) error {
	if db == nil {
		return api.StatusErrorf(http.StatusServiceUnavailable, string(types.DatabaseNotReady))
	}

	db.statusLock.RLock()
	status := db.status
	db.statusLock.RUnlock()

	switch status {
	case types.DatabaseReady:
		return nil
	case types.DatabaseNotReady:
		fallthrough
	case types.DatabaseOffline:
		fallthrough
	case types.DatabaseStarting:
		return api.StatusErrorf(http.StatusServiceUnavailable, string(status))

	case types.DatabaseWaiting:
		intVersion, extversion, apiExtensions := db.Schema().Version()

		awaitingSystems := 0
		err := db.Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
			allMembers, awaitingMembers, err := cluster.GetUpgradingClusterMembers(ctx, tx, intVersion, extversion, apiExtensions)
			if err != nil {
				return err
			}

			for _, member := range allMembers {
				if member.Address == db.listenAddr.URL.Host {
					continue
				}

				if awaitingMembers[member.Name] {
					awaitingSystems++
				}
			}

			return nil
		})
		if err != nil {
			return api.StatusErrorf(http.StatusInternalServerError, "Failed to fetch awaiting cluster members: %w", err)
		}

		return api.StatusErrorf(http.StatusServiceUnavailable, "%s: %d cluster members have not yet received the update", status, awaitingSystems)
	default:
		return api.StatusErrorf(http.StatusInternalServerError, "Database status is invalid")
	}
}

// NotifyUpgraded sends a notification that we can stop waiting for a cluster member to be upgraded.
func (db *DqliteDB) NotifyUpgraded() {
	select {
	case db.upgradeCh <- struct{}{}:
	default:
	}
}

// dialFunc to be passed to dqlite.
func (db *DqliteDB) dialFunc() dqliteClient.DialFunc {
	return func(ctx context.Context, address string) (net.Conn, error) {
		conn, err := dqliteNetworkDial(ctx, address, db)
		if err != nil {
			return nil, fmt.Errorf("Failed to dial https socket: %w", err)
		}

		return conn, nil
	}
}

// GetHeartbeatInterval returns the current database heartbeat interval.
func (db *DqliteDB) GetHeartbeatInterval() time.Duration {
	return db.heartbeatInterval
}

// SendHeartbeat initiates a new heartbeat sequence if this is a leader node.
func (db *DqliteDB) SendHeartbeat(ctx context.Context, c *internalClient.Client, hbInfo internalTypes.HeartbeatInfo) error {
	// set the heartbeat timeout to twice the heartbeat interval.
	heartbeatTimeout := db.heartbeatInterval * 2
	queryCtx, cancel := context.WithTimeout(ctx, heartbeatTimeout)
	defer cancel()

	return c.QueryStruct(queryCtx, "POST", internalTypes.InternalEndpoint, api.NewURL().Path("heartbeat"), hbInfo, nil)
}

func (db *DqliteDB) heartbeat(leaderInfo dqliteClient.NodeInfo, servers []dqliteClient.NodeInfo) error {
	// Use the heartbeat lock to prevent another heartbeat attempt if we are currently initiating one.
	db.heartbeatLock.Lock()
	defer db.heartbeatLock.Unlock()

	if db.IsOpen(db.ctx) != nil {
		logger.Debug("Database is not yet open, aborting heartbeat", logger.Ctx{"address": db.listenAddr.String()})
		return nil
	}

	if leaderInfo.Address != db.listenAddr.URL.Host {
		logger.Debug("Not performing heartbeat, this system is not the dqlite leader", logger.Ctx{"address": db.listenAddr.String()})
		return nil
	}

	client, err := internalClient.New(db.os.ControlSocket(), nil, nil, false)
	if err != nil {
		logger.Error("Failed to get local client", logger.Ctx{"address": db.listenAddr.String(), "error": err})
		return nil
	}

	// Initiate a heartbeat from this node.
	hbInfo := internalTypes.HeartbeatInfo{
		BeginRound:    true,
		LeaderAddress: leaderInfo.Address,
		DqliteRoles:   make(map[string]string, len(servers)),
	}

	for _, server := range servers {
		hbInfo.DqliteRoles[server.Address] = server.Role.String()
	}

	err = db.SendHeartbeat(db.ctx, client, hbInfo)
	if err != nil && err.Error() != "Attempt to initiate heartbeat from non-leader" {
		logger.Error("Failed to initiate heartbeat round", logger.Ctx{"address": db.dqlite.Address(), "error": err})
		return nil
	}

	return nil
}

// dqliteNetworkDial creates a connection to the internal database endpoint.
func dqliteNetworkDial(ctx context.Context, addr string, db *DqliteDB) (net.Conn, error) {
	peerCert, err := db.clusterCert().PublicKeyX509()
	if err != nil {
		return nil, err
	}

	config, err := internalClient.TLSClientConfig(db.serverCert(), peerCert)
	if err != nil {
		return nil, fmt.Errorf("Failed to parse TLS config: %w", err)
	}

	// Establish the connection
	request := &http.Request{
		Method:     "POST",
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     make(http.Header),
		Host:       addr,
	}

	path := fmt.Sprintf("https://%s/%s/%s", addr, internalTypes.InternalEndpoint, "database")
	request.URL, err = url.Parse(path)
	if err != nil {
		return nil, err
	}

	request.Header.Set("Upgrade", "dqlite")
	request.Header.Set("X-Dqlite-Version", fmt.Sprintf("%d", 1))
	request = request.WithContext(ctx)

	revert := revert.New()
	defer revert.Fail()

	deadline, _ := ctx.Deadline()
	dialer := &net.Dialer{Timeout: time.Until(deadline)}
	tlsDialer := tls.Dialer{NetDialer: dialer, Config: config}
	conn, err := tlsDialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("Failed connecting to HTTP endpoint %q: %w", addr, err)
	}

	revert.Add(func() {
		err := conn.Close()
		if err != nil {
			logger.Error("Failed to close connection to dqlite", logger.Ctx{"error": err})
		}
	})
	logCtx := logger.AddContext(logger.Ctx{"local": conn.LocalAddr().String(), "remote": conn.RemoteAddr().String()})
	logCtx.Debug("Dqlite connected outbound")

	// Set outbound timeouts.
	remoteTCP, err := tcp.ExtractConn(conn)
	if err != nil {
		logCtx.Error("Failed extracting TCP connection from remote connection", logger.Ctx{"error": err})
	} else {
		err := tcp.SetTimeouts(remoteTCP, 0)
		if err != nil {
			logCtx.Error("Failed setting TCP timeouts on remote connection", logger.Ctx{"error": err})
		}
	}

	err = request.Write(conn)
	if err != nil {
		return nil, fmt.Errorf("Failed sending HTTP requrest to %q: %w", request.URL, err)
	}

	response, err := http.ReadResponse(bufio.NewReader(conn), request)
	if err != nil {
		return nil, fmt.Errorf("Failed to read response: %w", err)
	}

	defer response.Body.Close()
	_, err = io.Copy(io.Discard, response.Body)
	if err != nil {
		logger.Error("Failed to read dqlite response body", logger.Ctx{"error": err})
	}

	// If the remote server has detected that we are out of date, let's
	// trigger an upgrade.
	if response.StatusCode == http.StatusUpgradeRequired {
		// TODO: trigger update.
		return nil, fmt.Errorf("Upgrade needed")
	}

	if response.StatusCode != http.StatusSwitchingProtocols {
		return nil, fmt.Errorf("Dialing failed: expected status code 101 got %d", response.StatusCode)
	}

	if response.Header.Get("Upgrade") != "dqlite" {
		return nil, fmt.Errorf("Missing or unexpected Upgrade header in response")
	}

	revert.Success()
	return conn, nil
}

// Stop closes the database and dqlite connection.
func (db *DqliteDB) Stop() error {
	db.statusLock.Lock()
	db.cancel()
	db.status = types.DatabaseOffline
	db.statusLock.Unlock()

	if db.IsOpen(context.TODO()) == nil {
		// The database might refuse to close if many nodes are stopping at the same time,
		// because the dqlite connection will have been lost.
		_ = db.db.Close()
	}

	if db.dqlite != nil {
		err := db.dqlite.Close()
		if err != nil {
			return err
		}
	}

	return nil
}
