package resources

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/shared/logger"

	"github.com/canonical/microcluster/v3/client"
	"github.com/canonical/microcluster/v3/cluster"
	internalTypes "github.com/canonical/microcluster/v3/internal/rest/types"
	internalState "github.com/canonical/microcluster/v3/internal/state"
	"github.com/canonical/microcluster/v3/rest"
	"github.com/canonical/microcluster/v3/rest/types"
	"github.com/canonical/microcluster/v3/state"
)

var heartbeatCmd = rest.Endpoint{
	Path: "heartbeat",

	Post: rest.EndpointAction{Handler: heartbeatPost, AllowUntrusted: true},
}

func heartbeatPost(s state.State, r *http.Request) response.Response {
	var hbInfo internalTypes.HeartbeatInfo
	err := json.NewDecoder(r.Body).Decode(&hbInfo)
	if err != nil {
		return response.SmartError(err)
	}

	if hbInfo.BeginRound {
		return beginHeartbeat(r.Context(), s, hbInfo)
	}

	// If we are not beginning a heartbeat, we are receiving one sent by the leader,
	// so we should update our local store of cluster members with the data from the heartbeat.

	err = s.Database().IsOpen(r.Context())
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed to respond to heartbeat, database is not yet open: %w", err))
	}

	clusterMemberList := []types.ClusterMember{}
	for _, clusterMember := range hbInfo.ClusterMembers {
		clusterMemberList = append(clusterMemberList, clusterMember)
	}

	err = s.Remotes().Replace(s.FileSystem().TrustDir, clusterMemberList...)
	if err != nil {
		return response.SmartError(err)
	}

	var internalSchemaVersion, externalSchemaVersion uint64
	err = s.Database().Transaction(r.Context(), func(ctx context.Context, tx *sql.Tx) error {
		localClusterMember, err := cluster.GetCoreClusterMember(ctx, tx, s.Name())
		if err != nil {
			return err
		}

		internalSchemaVersion = localClusterMember.SchemaInternal
		externalSchemaVersion = localClusterMember.SchemaExternal

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	intState, err := internalState.ToInternal(s)
	if err != nil {
		return response.SmartError(err)
	}

	if internalSchemaVersion != hbInfo.MaxSchemaInternal || externalSchemaVersion != hbInfo.MaxSchemaExternal {
		err := intState.InternalDatabase.Update()
		if err != nil {
			return response.SmartError(err)
		}
	}

	// TODO: If our schema version is behind, we should try to update here.

	return response.EmptySyncResponse
}

// beginHeartbeat initiates a heartbeat from the leader node to all other cluster members, if we haven't sent one out
// recently.
func beginHeartbeat(ctx context.Context, s state.State, hbReq internalTypes.HeartbeatInfo) response.Response {
	if s.Address().URL.Host != hbReq.LeaderAddress {
		return response.SmartError(fmt.Errorf("Attempt to initiate heartbeat from non-leader"))
	}

	// Get the database record of cluster members.
	var clusterMembers []types.ClusterMember
	err := s.Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		dbClusterMembers, err := cluster.GetCoreClusterMembers(ctx, tx)
		if err != nil {
			return err
		}

		clusterMembers = make([]types.ClusterMember, 0, len(dbClusterMembers))
		for _, clusterMember := range dbClusterMembers {
			apiClusterMember, err := clusterMember.ToAPI()
			if err != nil {
				return err
			}

			clusterMembers = append(clusterMembers, *apiClusterMember)
		}

		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Get dqlite record of cluster members.
	if len(clusterMembers) == 0 || len(hbReq.DqliteRoles) == 0 {
		logger.Info("Skipping heartbeat as the cluster is still initializing")
		return response.EmptySyncResponse
	}

	dqliteMap := map[string]string{}
	for member, role := range hbReq.DqliteRoles {
		dqliteMap[member] = role
	}

	// Update database with dqlite member roles.
	clusterMap := map[string]types.ClusterMember{}
	for _, clusterMember := range clusterMembers {
		role, ok := dqliteMap[clusterMember.Address.String()]

		// If a cluster member is pending and dqlite does not have a record for it yet, then skip it this round.
		if !ok && clusterMember.Role == string(cluster.Pending) {
			logger.Debug("Skipping heartbeat for pending cluster member", logger.Ctx{"address": clusterMember.Address})
			continue
		}

		clusterMember.Role = role
		clusterMap[clusterMember.Address.String()] = clusterMember
	}

	intState, err := internalState.ToInternal(s)
	if err != nil {
		return response.SmartError(err)
	}

	leaderEntry := clusterMap[s.Address().URL.Host]
	heartbeatInterval := time.Duration(intState.InternalDatabase.GetHeartbeatInterval())
	timeSinceLast := time.Since(leaderEntry.LastHeartbeat)
	if timeSinceLast < heartbeatInterval {
		logger.Debugf("Heartbeat was already sent %q ago, skipping heartbeat round", timeSinceLast.String())

		return response.EmptySyncResponse
	}

	logger.Debug("Beginning new heartbeat round", logger.Ctx{"address": s.Address().URL.Host})

	// Update local record of cluster members from the database, including any pending nodes for authentication.
	err = s.Remotes().Replace(s.FileSystem().TrustDir, clusterMembers...)
	if err != nil {
		return response.SmartError(err)
	}

	// Set the time of the last heartbeat to now.
	leaderEntry.LastHeartbeat = time.Now()
	clusterMap[s.Address().URL.Host] = leaderEntry

	// Record the maximum schema version discovered.
	hbInfo := internalTypes.HeartbeatInfo{ClusterMembers: clusterMap}
	for _, node := range clusterMembers {
		if node.SchemaInternalVersion > hbInfo.MaxSchemaInternal {
			hbInfo.MaxSchemaInternal = node.SchemaInternalVersion
		}

		if node.SchemaExternalVersion > hbInfo.MaxSchemaExternal {
			hbInfo.MaxSchemaExternal = node.SchemaExternalVersion
		}
	}

	clusterClients, err := s.Cluster(false)
	if err != nil {
		return response.SmartError(err)
	}

	// Use a lock to handle concurrent access to hbInfo.
	mapLock := sync.RWMutex{}
	// Send heartbeat to non-leader members, updating their local member cache and updating the node.
	// If we sent a heartbeat to this node within double the request timeout, then we can skip the node this round.
	err = clusterClients.Query(ctx, true, func(ctx context.Context, c *client.Client) error {
		addr := c.URL().URL.Host

		mapLock.RLock()
		currentMember, ok := hbInfo.ClusterMembers[addr]
		mapLock.RUnlock()
		if !ok {
			logger.Warnf("Skipping heartbeat cluster member record with address %v due to pending status", addr)
			return nil
		}

		timeSinceLast := time.Since(currentMember.LastHeartbeat)
		if timeSinceLast < time.Duration(intState.InternalDatabase.GetHeartbeatInterval()) {
			logger.Warnf("Skipping heartbeat to %q, one was sent %q ago", currentMember.Name, timeSinceLast.String())
			return nil
		}

		err := intState.InternalDatabase.SendHeartbeat(ctx, &c.Client, hbInfo)
		if err != nil {
			logger.Error("Received error sending heartbeat to cluster member", logger.Ctx{"target": addr, "error": err})
			return nil
		}

		currentMember.LastHeartbeat = time.Now()

		mapLock.Lock()
		hbInfo.ClusterMembers[addr] = currentMember
		mapLock.Unlock()

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Having sent a heartbeat to each valid cluster member, update the database record of members.
	err = s.Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		dbClusterMembers, err := cluster.GetCoreClusterMembers(ctx, tx)
		if err != nil {
			return err
		}

		for _, clusterMember := range dbClusterMembers {
			heartbeatInfo, ok := hbInfo.ClusterMembers[clusterMember.Address]
			if !ok {
				continue
			}

			clusterMember.Heartbeat = heartbeatInfo.LastHeartbeat
			clusterMember.Role = cluster.Role(heartbeatInfo.Role)
			err = cluster.UpdateCoreClusterMember(ctx, tx, clusterMember.Name, clusterMember)
			if err != nil {
				return err
			}
		}

		return cluster.DeleteExpiredCoreTokenRecords(ctx, tx)
	})
	if err != nil {
		return response.SmartError(err)
	}

	hookCtx, hookCancel := context.WithCancel(ctx)
	err = intState.Hooks.OnHeartbeat(hookCtx, s)
	hookCancel()
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}
