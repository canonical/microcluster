package resources

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	dqliteClient "github.com/canonical/go-dqlite/client"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/validate"
	"github.com/gorilla/mux"
	"golang.org/x/sys/unix"

	"github.com/canonical/microcluster/client"
	"github.com/canonical/microcluster/cluster"
	internalClient "github.com/canonical/microcluster/internal/rest/client"
	internalTypes "github.com/canonical/microcluster/internal/rest/types"
	internalState "github.com/canonical/microcluster/internal/state"
	"github.com/canonical/microcluster/internal/trust"
	"github.com/canonical/microcluster/rest"
	"github.com/canonical/microcluster/rest/access"
	"github.com/canonical/microcluster/rest/types"
	"github.com/canonical/microcluster/state"
)

var clusterCmd = rest.Endpoint{
	Path: "cluster",

	Post: rest.EndpointAction{Handler: clusterPost, AllowUntrusted: true},
	Get:  rest.EndpointAction{Handler: clusterGet, AccessHandler: access.AllowAuthenticated},
}

var clusterMemberCmd = rest.Endpoint{
	Path: "cluster/{name}",

	Put:    rest.EndpointAction{Handler: clusterMemberPut, AccessHandler: access.AllowAuthenticated},
	Delete: rest.EndpointAction{Handler: clusterMemberDelete, AccessHandler: access.AllowAuthenticated},
}

func clusterPost(s state.State, r *http.Request) response.Response {
	intState, err := internalState.ToInternal(s)
	if err != nil {
		return response.SmartError(err)
	}

	req := internalTypes.ClusterMember{}

	// Parse the request.
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	ctx, cancel := context.WithTimeout(intState.Context, time.Second*30)
	defer cancel()

	leaderClient, err := s.Database().Leader(ctx)
	if err != nil {
		return response.SmartError(err)
	}

	leaderInfo, err := leaderClient.Leader(ctx)
	if err != nil {
		return response.SmartError(err)
	}

	err = validate.IsHostname(req.Name)
	if err != nil {
		return response.SmartError(fmt.Errorf("Invalid cluster member name %q: %w", req.Name, err))
	}

	// Check if any of the remote's addresses are currently in use.
	existingRemote := s.Remotes().RemoteByAddress(req.Address)
	if existingRemote != nil {
		return response.SmartError(fmt.Errorf("Remote with address %q exists", req.Address.String()))
	}

	// Forward request to leader.
	if leaderInfo.Address != s.Address().URL.Host {
		client, err := s.Leader()
		if err != nil {
			return response.SmartError(err)
		}

		tokenResponse, err := client.AddClusterMember(intState.Context, req)
		if err != nil {
			return response.SmartError(err)
		}

		return response.SyncResponse(true, tokenResponse)
	}

	// Check if the joining node's extensions are compatible with the leader's.
	err = intState.Extensions.IsSameVersion(req.Extensions)
	if err != nil {
		return response.SmartError(err)
	}

	err = s.Database().Transaction(intState.Context, func(ctx context.Context, tx *sql.Tx) error {
		dbClusterMember := cluster.InternalClusterMember{
			Name:           req.Name,
			Address:        req.Address.String(),
			Certificate:    req.Certificate.String(),
			SchemaInternal: req.SchemaInternalVersion,
			SchemaExternal: req.SchemaExternalVersion,
			APIExtensions:  req.Extensions,
			Heartbeat:      time.Time{},
			Role:           cluster.Pending,
		}

		record, err := cluster.GetInternalTokenRecord(ctx, tx, req.Secret)
		if err != nil {
			return err
		}

		_, err = cluster.CreateInternalClusterMember(ctx, tx, dbClusterMember)
		if err != nil {
			return err
		}

		return cluster.DeleteInternalTokenRecord(ctx, tx, record.Name)
	})
	if err != nil {
		return response.SmartError(err)
	}

	remotes := s.Remotes()
	clusterMembers := make([]internalTypes.ClusterMemberLocal, 0, remotes.Count())
	for _, clusterMember := range remotes.RemotesByName() {
		clusterMember := internalTypes.ClusterMemberLocal{
			Name:        clusterMember.Name,
			Address:     clusterMember.Address,
			Certificate: clusterMember.Certificate,
		}

		clusterMembers = append(clusterMembers, clusterMember)
	}

	clusterCert, err := s.ClusterCert().PublicKeyX509()
	if err != nil {
		return response.SmartError(err)
	}

	localRemote := remotes.RemotesByName()[s.Name()]
	tokenResponse := internalTypes.TokenResponse{
		ClusterCert: types.X509Certificate{Certificate: clusterCert},
		ClusterKey:  string(s.ClusterCert().PrivateKey()),

		TrustedMember:  internalTypes.ClusterMemberLocal{Name: s.Name(), Address: localRemote.Address, Certificate: localRemote.Certificate},
		ClusterMembers: clusterMembers,
	}

	newRemote := trust.Remote{
		Location:    trust.Location{Name: req.Name, Address: req.Address},
		Certificate: req.Certificate,
	}

	// Add the cluster member to our local store for authentication.
	err = s.Remotes().Add(s.FileSystem().TrustDir, newRemote)
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponse(true, tokenResponse)
}

func clusterGet(s state.State, r *http.Request) response.Response {
	intState, err := internalState.ToInternal(s)
	if err != nil {
		return response.SmartError(err)
	}

	var apiClusterMembers []internalTypes.ClusterMember
	err = s.Database().Transaction(intState.Context, func(ctx context.Context, tx *sql.Tx) error {
		clusterMembers, err := cluster.GetInternalClusterMembers(ctx, tx)
		if err != nil {
			return err
		}

		apiClusterMembers = make([]internalTypes.ClusterMember, 0, len(clusterMembers))
		for _, clusterMember := range clusterMembers {
			apiClusterMember, err := clusterMember.ToAPI()
			if err != nil {
				return err
			}

			apiClusterMembers = append(apiClusterMembers, *apiClusterMember)
		}

		return nil
	})
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed to get cluster members: %w", err))
	}

	clusterCert, err := s.ClusterCert().PublicKeyX509()
	if err != nil {
		return response.SmartError(err)
	}

	// Send a small request to each node to ensure they are reachable.
	for i, clusterMember := range apiClusterMembers {
		addr := api.NewURL().Scheme("https").Host(clusterMember.Address.String())
		d, err := internalClient.New(*addr, s.ServerCert(), clusterCert, false)
		if err != nil {
			return response.SmartError(fmt.Errorf("Failed to create HTTPS client for cluster member with address %q: %w", addr.String(), err))
		}

		err = d.CheckReady(intState.Context)
		if err == nil {
			apiClusterMembers[i].Status = internalTypes.MemberOnline
		} else {
			logger.Warnf("Failed to get status of cluster member with address %q: %v", addr.String(), err)
		}
	}

	return response.SyncResponse(true, apiClusterMembers)
}

// clusterDisableMu is used to prevent the daemon process from being replaced/stopped during removal from the
// cluster until such time as the request that initiated the removal has finished. This allows for self removal
// from the cluster when not the leader.
var clusterDisableMu sync.Mutex

func clusterMemberPut(s state.State, r *http.Request) response.Response {
	force := r.URL.Query().Get("force") == "1"
	reExec, err := resetClusterMember(r.Context(), s, force)
	if err != nil {
		return response.SmartError(err)
	}

	go reExec()

	return response.ManualResponse(func(w http.ResponseWriter) error {
		err := response.EmptySyncResponse.Render(w)
		if err != nil {
			return err
		}

		// Send the response before replacing the LXD daemon process.
		f, ok := w.(http.Flusher)
		if !ok {
			return fmt.Errorf("ResponseWriter is not type http.Flusher")
		}

		f.Flush()
		return nil
	})
}

// resetClusterMember clears the daemon state, closing the database and stopping all listeners.
// Returns a function that can be used to re-exec the daemon, forcibly reloading its state.
func resetClusterMember(ctx context.Context, s state.State, force bool) (reExec func(), err error) {
	err = s.Database().Stop()
	if err != nil && !force {
		return nil, fmt.Errorf("Failed shutting down database: %w", err)
	}

	err = internalState.StopListeners()
	if err != nil && !force {
		return nil, fmt.Errorf("Failed shutting down listeners: %w", err)
	}

	err = os.RemoveAll(s.FileSystem().StateDir)
	if err != nil && !force {
		return nil, fmt.Errorf("Failed to remove the s directory: %w", err)
	}

	reExec = func() {
		<-ctx.Done() // Wait until request has finished.

		// Wait until we can acquire the lock. This way if another request is holding the lock we won't
		// replace/stop the LXD daemon until that request has finished.
		clusterDisableMu.Lock()
		defer clusterDisableMu.Unlock()
		execPath, err := os.Readlink("/proc/self/exe")
		if err != nil {
			execPath = "bad-exec-path"
		}

		// The execPath from /proc/self/exe can end with " (deleted)" if the lxd binary has been removed/changed
		// since the lxd process was started, strip this so that we only return a valid path.
		logger.Info("Restarting daemon following removal from cluster")
		execPath = strings.TrimSuffix(execPath, " (deleted)")
		err = unix.Exec(execPath, os.Args, os.Environ())
		if err != nil {
			logger.Error("Failed restarting daemon", logger.Ctx{"err": err})
		}
	}

	return reExec, nil
}

// clusterMemberDelete Removes a cluster member from dqlite and re-execs its daemon.
func clusterMemberDelete(s state.State, r *http.Request) response.Response {
	force := r.URL.Query().Get("force") == "1"
	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	intState, err := internalState.ToInternal(s)
	if err != nil {
		return response.SmartError(err)
	}

	allRemotes := s.Remotes().RemotesByName()
	remote, ok := allRemotes[name]
	if !ok {
		return response.SmartError(fmt.Errorf("No remote exists with the given name %q", name))
	}

	ctx, cancel := context.WithTimeout(intState.Context, time.Second*30)
	defer cancel()

	leader, err := s.Database().Leader(ctx)
	if err != nil {
		return response.SmartError(err)
	}

	leaderInfo, err := leader.Leader(ctx)
	if err != nil {
		return response.SmartError(err)
	}

	// If we are not the leader, just forward the request.
	if leaderInfo.Address != s.Address().URL.Host {
		if allRemotes[name].Address.String() == s.Address().URL.Host {
			// If the member being removed is ourselves and we are not the leader, then lock the
			// clusterPutDisableMu before we forward the request to the leader, so that when the leader
			// goes on to request clusterPutDisable back to ourselves it won't be actioned until we
			// have returned this request back to the original client.
			clusterDisableMu.Lock()
			logger.Info("Acquired cluster self removal lock", logger.Ctx{"member": name})

			go func() {
				<-r.Context().Done() // Wait until request is finished.

				logger.Info("Releasing cluster self removal lock", logger.Ctx{"member": name})
				clusterDisableMu.Unlock()
			}()
		}

		client, err := s.Leader()
		if err != nil {
			return response.SmartError(err)
		}

		err = client.DeleteClusterMember(intState.Context, name, force)
		if err != nil {
			return response.SmartError(err)
		}

		return response.ManualResponse(func(w http.ResponseWriter) error {
			err := response.EmptySyncResponse.Render(w)
			if err != nil {
				return err
			}

			// Send the response before replacing the LXD daemon process.
			f, ok := w.(http.Flusher)
			if !ok {
				return fmt.Errorf("ResponseWriter is not type http.Flusher")
			}

			f.Flush()
			return nil
		})
	}

	info, err := leader.Cluster(intState.Context)
	if err != nil {
		return response.SmartError(err)
	}

	index := -1
	for i, node := range info {
		if node.Address == remote.Address.String() {
			index = i
			break
		}
	}

	// If we can't find the node in dqlite, that means it failed to fully initialize. It still might have a record in our database so continue along anyway.
	if index < 0 {
		logger.Errorf("No dqlite record exists for %q, deleting from internal record instead", remote.Name)
	}

	var clusterMembers []cluster.InternalClusterMember
	err = s.Database().Transaction(r.Context(), func(ctx context.Context, tx *sql.Tx) error {
		var err error
		clusterMembers, err = cluster.GetInternalClusterMembers(ctx, tx)

		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	numPending := 0
	for _, clusterMember := range clusterMembers {
		if clusterMember.Role == cluster.Pending {
			numPending++
		}
	}

	if len(clusterMembers)-numPending < 1 {
		return response.SmartError(fmt.Errorf("Cannot remove cluster members, there are no remaining non-pending members"))
	}

	if len(info) < 2 {
		return response.SmartError(fmt.Errorf("Cannot leave a cluster with %d members", len(info)))
	}

	// If we are removing the leader of a 2-node cluster, ensure the remaining node is a voter.
	if len(info) == 2 && allRemotes[name].Address.String() == leaderInfo.Address {
		for _, node := range info {
			if node.Address != leaderInfo.Address && node.Role != dqliteClient.Voter {
				err = leader.Assign(ctx, node.ID, dqliteClient.Voter)
				if err != nil {
					return response.SmartError(err)
				}
			}
		}
	}

	// Refresh members information since we may have changed roles.
	info, err = leader.Cluster(intState.Context)
	if err != nil {
		return response.SmartError(err)
	}

	// If we are the leader and removing ourselves, reassign the leader role and perform the removal from there.
	if allRemotes[name].Address.String() == leaderInfo.Address {
		otherNodes := []uint64{}
		for _, node := range info {
			if node.Address != allRemotes[name].Address.String() && node.Role == dqliteClient.Voter {
				otherNodes = append(otherNodes, node.ID)
			}
		}

		if len(otherNodes) == 0 {
			return response.SmartError(fmt.Errorf("Found no voters to transfer leadership to"))
		}

		randomID := otherNodes[rand.Intn(len(otherNodes))]
		err = leader.Transfer(ctx, randomID)
		if err != nil {
			return response.SmartError(err)
		}

		client, err := s.Leader()
		if err != nil {
			return response.SmartError(err)
		}

		clusterDisableMu.Lock()
		logger.Info("Acquired cluster self removal lock", logger.Ctx{"member": name})

		go func() {
			<-r.Context().Done() // Wait until request is finished.

			logger.Info("Releasing cluster self removal lock", logger.Ctx{"member": name})
			clusterDisableMu.Unlock()
		}()

		err = client.DeleteClusterMember(intState.Context, name, force)
		if err != nil {
			return response.SmartError(err)
		}

		return response.ManualResponse(func(w http.ResponseWriter) error {
			err := response.EmptySyncResponse.Render(w)
			if err != nil {
				return err
			}

			// Send the response before replacing the LXD daemon process.
			f, ok := w.(http.Flusher)
			if !ok {
				return fmt.Errorf("ResponseWriter is not type http.Flusher")
			}

			f.Flush()
			return nil
		})
	}

	publicKey, err := s.ClusterCert().PublicKeyX509()
	if err != nil {
		return response.SmartError(err)
	}

	// Set the forwarded flag so that the the system to be removed knows the removal is in progress.
	c, err := internalClient.New(remote.URL(), s.ServerCert(), publicKey, true)
	if err != nil {
		return response.SmartError(err)
	}

	// Tell the cluster member to run its PreRemove hook and return.
	err = internalClient.RunPreRemoveHook(ctx, c.UseTarget(name), internalTypes.HookRemoveMemberOptions{Force: force})
	if err != nil && !force {
		return response.SmartError(err)
	}

	// Remove the cluster member from the database.
	err = s.Database().Transaction(intState.Context, func(ctx context.Context, tx *sql.Tx) error {
		return cluster.DeleteInternalClusterMember(ctx, tx, remote.Address.String())
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Remove the node from dqlite, if it has a record there.
	if index >= 0 {
		err = leader.Remove(intState.Context, info[index].ID)
		if err != nil {
			return response.SmartError(err)
		}
	}

	localClient, err := internalClient.New(s.FileSystem().ControlSocket(), nil, nil, false)
	if err != nil {
		return response.SmartError(err)
	}

	err = internalClient.DeleteTrustStoreEntry(ctx, localClient, name)
	if err != nil && !force {
		return response.SmartError(err)
	}

	c, err = internalClient.New(remote.URL(), s.ServerCert(), publicKey, false)
	if err != nil {
		return response.SmartError(err)
	}

	err = c.ResetClusterMember(intState.Context, name, force)
	if err != nil && !force {
		return response.SmartError(err)
	}

	cluster, err := s.Cluster(false)
	if err != nil {
		return response.SmartError(err)
	}

	// Run the PostRemove hook locally.
	err = intState.Hooks.PostRemove(s, force)
	if err != nil {
		return response.SmartError(err)
	}

	// Run the PostRemove hook on all other members.
	remotes := s.Remotes()
	err = cluster.Query(intState.Context, true, func(ctx context.Context, c *client.Client) error {
		c.SetClusterNotification()
		addrPort, err := types.ParseAddrPort(c.URL().URL.Host)
		if err != nil {
			return err
		}

		remote := remotes.RemoteByAddress(addrPort)
		if remote == nil {
			return fmt.Errorf("No remote found at address %q run the post-remove hook", c.URL().URL.Host)
		}

		return internalClient.RunPostRemoveHook(ctx, c.Client.UseTarget(remote.Name), internalTypes.HookRemoveMemberOptions{Force: force})
	})
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}
