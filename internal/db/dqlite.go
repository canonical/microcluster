package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	dqlite "github.com/canonical/go-dqlite/v3/app"
	dqliteClient "github.com/canonical/go-dqlite/v3/client"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/revert"
	"github.com/canonical/lxd/shared/tcp"

	"github.com/canonical/microcluster/v3/internal/cluster"
	"github.com/canonical/microcluster/v3/internal/db/update"
	internalClient "github.com/canonical/microcluster/v3/internal/rest/client"
	"github.com/canonical/microcluster/v3/internal/sys"
	clusterDB "github.com/canonical/microcluster/v3/microcluster/db"
	"github.com/canonical/microcluster/v3/microcluster/types"
)

// DqliteDB holds all information internal to the dqlite database.
type DqliteDB struct {
	memberName    func() string           // Local cluster member name
	clusterCert   func() *shared.CertInfo // Cluster certificate for dqlite authentication.
	serverCert    func() *shared.CertInfo // Server certificate for dqlite authentication.
	failureDomain func() uint64           // Local daemon failure-domain value applied when starting dqlite.
	listenAddr    *url.URL                // Listen address for this dqlite node.

	dbName string // This is db.bin.
	os     types.OS

	db        *sql.DB
	dqlite    *dqlite.App
	acceptCh  chan net.Conn
	acceptMu  sync.RWMutex
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

// Accept passes conn to dqlite. During normal operation the send completes
// immediately. During a Restart the channel has no consumer yet, so the call
// blocks for up to 10s, keeping clients "on hold" until the new dqlite
// instance is ready, making the restart invisible to them.
// acceptMu is held so that Restart can atomically rotate the channel.
func (db *DqliteDB) Accept(conn net.Conn) {
	db.acceptMu.RLock()
	defer db.acceptMu.RUnlock()

	select {
	case db.acceptCh <- conn:
	case <-time.After(10 * time.Second):
		conn.Close()
	case <-db.ctx.Done():
		conn.Close()
	}
}

// NewDB creates an empty db struct with no dqlite connection.
func NewDB(ctx context.Context, serverCert func() *shared.CertInfo, clusterCert func() *shared.CertInfo, memberName func() string, failureDomain func() uint64, os types.OS, heartbeatInterval time.Duration) (*DqliteDB, error) {
	shutdownCtx, shutdownCancel := context.WithCancel(ctx)

	if heartbeatInterval == 0 {
		heartbeatInterval = DefaultHeartbeatInterval
	}

	db := &DqliteDB{
		memberName:        memberName,
		serverCert:        serverCert,
		clusterCert:       clusterCert,
		failureDomain:     failureDomain,
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

	initialized, err := db.isInitialized()
	if err != nil {
		return nil, fmt.Errorf("Failed to check if database is initialized: %w", err)
	}

	if initialized {
		db.status = types.DatabaseStarting
	}

	return db, nil
}

// log is a convenience to retrieve the internal logger from the database's context.
// We always expect the logger to be present.
func (db *DqliteDB) log() *slog.Logger {
	return db.ctx.Value(types.CtxLogger).(*slog.Logger) //nolint:revive
}

// SetSchema sets schema and API extensions on the DB.
func (db *DqliteDB) SetSchema(schemaExtensions []clusterDB.Update, apiExtensions types.Extensions) {
	s := update.NewSchema()
	s.AppendSchema(schemaExtensions, apiExtensions)
	db.schema = s.Schema()
}

// Schema returns the update.SchemaUpdate for the DB.
func (db *DqliteDB) Schema() *update.SchemaUpdate {
	return db.schema
}

// SchemaVersion returns the current internal and external schema version, as well as all API extensions in memory.
func (db *DqliteDB) SchemaVersion() (versionInternal uint64, versionExternal uint64, apiExtensions types.Extensions) {
	return db.schema.Version()
}

// isInitialized determines whether the database has been bootstrapped or joined to a cluster.
// This is an internal helper function; external callers should use Status() instead.
func (db *DqliteDB) isInitialized() (bool, error) {
	_, err := os.Stat(filepath.Join(db.os.DatabaseDir(), "info.yaml"))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}

		return false, err
	}

	return true, nil
}

// Bootstrap dqlite.
func (db *DqliteDB) Bootstrap(extensions types.Extensions, addr *url.URL, clusterRecord cluster.CoreClusterMember) error {
	var err error
	db.listenAddr = addr
	db.dqlite, err = dqlite.New(db.os.DatabaseDir(),
		dqlite.WithAddress(db.listenAddr.Host),
		dqlite.WithFailureDomain(db.failureDomain()),
		dqlite.WithRolesAdjustmentFrequency(db.heartbeatInterval),
		dqlite.WithRolesAdjustmentHook(db.heartbeat),
		dqlite.WithConcurrentLeaderConns(&db.maxConns),
		dqlite.WithExternalConn(db.dialFunc(), db.acceptCh),
		dqlite.WithUnixSocket(os.Getenv(sys.DqliteSocket)))
	if err != nil {
		return fmt.Errorf("Failed to bootstrap dqlite: %w", err)
	}

	// Only close the dqlite app if the entire Bootstrap process fails or exits.
	// NOTE: dqlite.Close() tears down Raft connections but does NOT remove
	// the node from the dqlite cluster. For bootstrap this is fine since
	// the node is the sole member.
	reverter := revert.New()
	defer reverter.Fail()
	reverter.Add(func() {
		if db.dqlite != nil {
			closeErr := db.dqlite.Close()
			if closeErr != nil {
				db.log().Error("Failed to close dqlite app", slog.String("address", db.listenAddr.String()), slog.String("error", closeErr.Error()))
			}

			db.dqlite = nil
		}
	})

	err = db.Open(extensions, true)
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

	reverter.Success()
	return nil
}

// Join a dqlite cluster with the address of a member.
func (db *DqliteDB) Join(extensions types.Extensions, addr *url.URL, joinAddresses ...string) error {
	var err error
	db.listenAddr = addr
	db.dqlite, err = dqlite.New(db.os.DatabaseDir(),
		dqlite.WithCluster(joinAddresses),
		dqlite.WithRolesAdjustmentFrequency(db.heartbeatInterval),
		dqlite.WithRolesAdjustmentHook(db.heartbeat),
		dqlite.WithAddress(db.listenAddr.Host),
		dqlite.WithFailureDomain(db.failureDomain()),
		dqlite.WithConcurrentLeaderConns(&db.maxConns),
		dqlite.WithExternalConn(db.dialFunc(), db.acceptCh),
		dqlite.WithUnixSocket(os.Getenv(sys.DqliteSocket)))
	if err != nil {
		return fmt.Errorf("Failed to join dqlite cluster %w", err)
	}

	// Only close the dqlite app if the entire Join process fails or exits.
	// This ensures dqlite.Close() is not called on every Open() retry, but only if we fully give up joining.
	//
	// NOTE: dqlite.Close() tears down Raft connections but does NOT remove
	// the node from the dqlite cluster. We must explicitly ask the leader to
	// remove us before closing; otherwise the node remains as a ghost member,
	// which can break quorum calculations and membership consistency checks.
	reverter := revert.New()
	defer reverter.Fail()
	reverter.Add(func() {
		if db.dqlite == nil {
			return
		}

		removeCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		cli, err := db.dqlite.Leader(removeCtx, dqliteClient.WithConcurrentLeaderConns(1))
		if err != nil {
			db.log().Warn("Failed to connect to dqlite leader to remove ourselves from the cluster", slog.String("address", db.listenAddr.String()), slog.String("error", err.Error()))
		} else {
			err = cli.Remove(removeCtx, db.dqlite.ID())
			cli.Close()
			if err != nil {
				db.log().Warn("Failed to remove ourselves from the dqlite cluster", slog.Uint64("id", db.dqlite.ID()), slog.String("address", db.listenAddr.String()), slog.String("error", err.Error()))
			}
		}

		closeErr := db.dqlite.Close()
		if closeErr != nil {
			db.log().Error("Failed to close dqlite app", slog.String("address", db.listenAddr.String()), slog.String("error", closeErr.Error()))
		}

		db.dqlite = nil
	})

	for {
		err := db.Open(extensions, false)
		if err == nil {
			break
		}

		// If this is a graceful abort, then we should loop back and try to start the database again.
		if errors.Is(err, update.ErrGracefulAbort) {
			db.log().Debug("Re-attempting schema upgrade and API extension checks", slog.String("address", db.listenAddr.String()))

			continue
		}

		return err
	}

	reverter.Success()
	return nil
}

// StartWithCluster starts up dqlite and joins the cluster.
func (db *DqliteDB) StartWithCluster(extensions types.Extensions, addr *url.URL, clusterMembers map[string]types.AddrPort) error {
	allClusterAddrs := []string{}
	for _, clusterMemberAddrs := range clusterMembers {
		allClusterAddrs = append(allClusterAddrs, clusterMemberAddrs.String())
	}

	return db.Join(extensions, addr, allClusterAddrs...)
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
		return api.StatusErrorf(http.StatusServiceUnavailable, "%s", string(status))

	case types.DatabaseWaiting:
		intVersion, extversion, apiExtensions := db.Schema().Version()

		awaitingSystems := 0
		err := db.Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
			allMembers, awaitingMembers, err := cluster.GetUpgradingClusterMembers(ctx, tx, intVersion, extversion, apiExtensions)
			if err != nil {
				return err
			}

			for _, member := range allMembers {
				if member.Address == db.listenAddr.Host {
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
func (db *DqliteDB) SendHeartbeat(ctx context.Context, c types.Client, hbInfo types.HeartbeatInfo) error {
	// set the heartbeat timeout to twice the heartbeat interval.
	heartbeatTimeout := db.heartbeatInterval * 2
	queryCtx, cancel := context.WithTimeout(ctx, heartbeatTimeout)
	defer cancel()

	return c.Query(queryCtx, "POST", types.InternalEndpoint, &api.NewURL().Path("heartbeat").URL, hbInfo, nil)
}

func (db *DqliteDB) heartbeat(leaderInfo dqliteClient.NodeInfo, servers []dqliteClient.NodeInfo) error {
	// Use the heartbeat lock to prevent another heartbeat attempt if we are currently initiating one.
	db.heartbeatLock.Lock()
	defer db.heartbeatLock.Unlock()

	if db.IsOpen(db.ctx) != nil {
		db.log().Debug("Database is not yet open, aborting heartbeat", slog.String("address", db.listenAddr.String()))
		return nil
	}

	if leaderInfo.Address != db.listenAddr.Host {
		db.log().Debug("Not performing heartbeat, this system is not the dqlite leader", slog.String("address", db.listenAddr.String()))
		return nil
	}

	client, err := internalClient.New(db.os.ControlSocket(), nil, nil, false)
	if err != nil {
		db.log().Error("Failed to get local client", slog.String("address", db.listenAddr.String()), slog.String("error", err.Error()))
		return nil
	}

	// Initiate a heartbeat from this node.
	hbInfo := types.HeartbeatInfo{
		BeginRound:    true,
		LeaderAddress: leaderInfo.Address,
		DqliteRoles:   make(map[string]string, len(servers)),
	}

	for _, server := range servers {
		hbInfo.DqliteRoles[server.Address] = server.Role.String()
	}

	err = db.SendHeartbeat(db.ctx, client, hbInfo)
	if err != nil && err.Error() != "Attempt to initiate heartbeat from non-leader" {
		db.log().Error("Failed to initiate heartbeat round", slog.String("address", db.dqlite.Address()), slog.String("error", err.Error()))
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

	conn, err := internalClient.DialDqlite(ctx, addr, config)
	if err != nil {
		return nil, err
	}

	slogGroup := slog.Group("peers", slog.String("local", conn.LocalAddr().String()), slog.String("remote", conn.RemoteAddr().String()))
	db.log().Debug("Successfully established outbound dqlite connection", slogGroup)

	// Set outbound timeouts.
	remoteTCP, err := tcp.ExtractConn(conn)
	if err != nil {
		db.log().Error("Failed extracting TCP connection from remote connection", slogGroup, slog.String("error", err.Error()))
	} else {
		err := tcp.SetTimeouts(remoteTCP, 0)
		if err != nil {
			db.log().Error("Failed setting TCP timeouts on remote connection", slogGroup, slog.String("error", err.Error()))
		}
	}

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

// Restart closes the dqlite app and SQL connection without cancelling the daemon context,
// then reconnects to the cluster. This allows changes like failure-domain to take effect
// without a full daemon restart.
func (db *DqliteDB) Restart(extensions types.Extensions, clusterMembers map[string]types.AddrPort) error {
	if db.listenAddr != nil && db.dqlite != nil {
		ctx, cancel := context.WithTimeout(db.ctx, 30*time.Second)
		defer cancel()

		// Query the current cluster leader before stopping the local dqlite app.
		leader, err := db.Leader(ctx)
		if err != nil {
			return fmt.Errorf("Failed to determine current dqlite leader before restart: %w", err)
		}

		defer leader.Close()

		leaderInfo, err := leader.Leader(ctx)
		if err != nil {
			return fmt.Errorf("Failed to fetch dqlite leader information before restart: %w", err)
		}

		// Only transfer leadership when restarting the current leader. Followers can
		// restart immediately without disrupting cluster leadership.
		if leaderInfo.Address == db.listenAddr.Host {
			err = db.transferLeadership(ctx, leader, leaderInfo)
			if err != nil {
				return err
			}
		}
	}

	db.statusLock.Lock()
	db.status = types.DatabaseOffline
	db.statusLock.Unlock()

	// Swap in a fresh accept channel for the next dqlite instance and close the
	// old one so any stale go-dqlite reader ranging over it can exit.
	// Accept() holds acceptMu for every send, so once the write lock is held there
	// can be no remaining senders on the old channel.
	db.acceptMu.Lock()
	oldAcceptCh := db.acceptCh
	db.acceptCh = make(chan net.Conn)
	db.acceptMu.Unlock()

	if oldAcceptCh != nil {
		close(oldAcceptCh)
	}

	if db.db != nil {
		_ = db.db.Close()
		db.db = nil
	}

	if db.dqlite != nil {
		_ = db.dqlite.Close()
		db.dqlite = nil
	}

	return db.StartWithCluster(extensions, db.listenAddr, clusterMembers)
}

// transferLeadership hands leadership to another voter before the local leader
// restarts. This avoids forcing the cluster to detect leader loss mid-restart.
func (db *DqliteDB) transferLeadership(ctx context.Context, leader *dqliteClient.Client, leaderInfo *dqliteClient.NodeInfo) error {
	info, err := leader.Cluster(ctx)
	if err != nil {
		return fmt.Errorf("Failed to fetch dqlite cluster information before restart: %w", err)
	}

	if len(info) < 2 {
		return nil
	}

	// In a 2-node cluster, ensure the remaining node is promotable before transferring leadership.
	if len(info) == 2 {
		for _, node := range info {
			if node.Address != leaderInfo.Address && node.Role != dqliteClient.Voter {
				err = leader.Assign(ctx, node.ID, dqliteClient.Voter)
				if err != nil {
					return fmt.Errorf("Failed to promote peer to voter before leadership transfer: %w", err)
				}
			}
		}

		info, err = leader.Cluster(ctx)
		if err != nil {
			return fmt.Errorf("Failed to refresh dqlite cluster information before restart: %w", err)
		}
	}

	// Prefer transferring directly to another voter, since only voters are eligible
	// to become raft leader.
	var otherVoters []uint64
	for _, node := range info {
		if node.Address != leaderInfo.Address && node.Role == dqliteClient.Voter {
			otherVoters = append(otherVoters, node.ID)
		}
	}

	if len(otherVoters) == 0 {
		return fmt.Errorf("Found no voters to transfer dqlite leadership to before restart")
	}

	target := otherVoters[rand.Intn(len(otherVoters))]
	err = leader.Transfer(ctx, target)
	if err != nil {
		return fmt.Errorf("Failed to transfer dqlite leadership before restart: %w", err)
	}

	return nil
}
