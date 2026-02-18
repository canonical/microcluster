package resources

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	dqliteClient "github.com/canonical/go-dqlite/v3/client"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/gorilla/mux"
	"golang.org/x/sys/unix"

	"github.com/canonical/microcluster/v3/internal/cluster"
	"github.com/canonical/microcluster/v3/internal/log"
	"github.com/canonical/microcluster/v3/internal/rest/access"
	internalClient "github.com/canonical/microcluster/v3/internal/rest/client"
	internalState "github.com/canonical/microcluster/v3/internal/state"
	"github.com/canonical/microcluster/v3/internal/utils"
	"github.com/canonical/microcluster/v3/microcluster/types"
)

var clusterCmd = types.Endpoint{
	Path:              "cluster",
	AllowedBeforeInit: true,

	Get: types.EndpointAction{Handler: clusterGet, AccessHandler: access.AllowAuthenticated},
}

var clusterInternalCmd = types.Endpoint{
	Path:              "cluster",
	AllowedBeforeInit: true,

	Post: types.EndpointAction{Handler: clusterPost, AllowUntrusted: true},
}

var clusterMemberCmd = types.Endpoint{
	Path: "cluster/{name}",

	Delete: types.EndpointAction{Handler: clusterMemberDelete, AccessHandler: access.AllowAuthenticated},
}

var clusterMemberInternalCmd = types.Endpoint{
	Path: "cluster/{name}",

	Put: types.EndpointAction{Handler: clusterMemberPut, AccessHandler: access.AllowAuthenticated},
}

func clusterPost(s types.State, r *http.Request) types.Response {
	err := s.Database().IsOpen(r.Context())
	if err != nil {
		return types.SmartError(err)
	}

	req := types.ClusterMember{}

	// Parse the request.
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return types.BadRequest(err)
	}

	ctx := r.Context()

	leaderClient, err := s.Database().Leader(ctx)
	if err != nil {
		return types.SmartError(err)
	}

	leaderInfo, err := leaderClient.Leader(ctx)
	if err != nil {
		return types.SmartError(err)
	}

	err = utils.ValidateFQDN(req.Name)
	if err != nil {
		return types.SmartError(fmt.Errorf("Cluster member name %q is not a valid FQDN: %w", req.Name, err))
	}

	// Check if any of the remote's addresses are currently in use.
	existingRemote := s.Truststore().RemoteByAddress(req.Address)
	if existingRemote != nil {
		return types.SmartError(fmt.Errorf("Remote with address %q exists", req.Address.String()))
	}

	// Check cluster membership consistency before allowing joins
	// This ensures core_cluster_members, truststore, and dqlite are all in sync
	intState, err := internalState.ToInternal(s)
	if err != nil {
		return types.SmartError(err)
	}

	err = intState.CheckMembershipConsistency(ctx)
	if err != nil {
		return types.SmartError(err)
	}

	// Forward request to leader.
	if leaderInfo.Address != s.Address().Host {
		client, err := s.Connect().Leader(false)
		if err != nil {
			return types.SmartError(err)
		}

		tokenResponse, err := internalClient.AddClusterMember(ctx, client, req)
		if err != nil {
			return types.SmartError(err)
		}

		return types.SyncResponse(true, tokenResponse)
	}

	// Check if the joining node's extensions are compatible with the leader's.
	err = intState.Extensions.IsSameVersion(req.Extensions)
	if err != nil {
		return types.SmartError(err)
	}

	err = s.Database().Transaction(r.Context(), func(ctx context.Context, tx *sql.Tx) error {
		dbClusterMember := cluster.CoreClusterMember{
			Name:           req.Name,
			Address:        req.Address.String(),
			Certificate:    req.Certificate.String(),
			SchemaInternal: req.SchemaInternalVersion,
			SchemaExternal: req.SchemaExternalVersion,
			APIExtensions:  req.Extensions,
			Heartbeat:      time.Time{},
			Role:           cluster.Pending,
		}

		record, err := cluster.GetCoreTokenRecord(ctx, tx, req.Secret)
		if err != nil {
			return err
		}

		if record.Expired() {
			return fmt.Errorf("Token expired")
		}

		if !slices.Contains(req.Certificate.DNSNames, record.Name) {
			return fmt.Errorf("Joining server certificate SAN does not contain join token name")
		}

		_, err = cluster.CreateCoreClusterMember(ctx, tx, dbClusterMember)
		if err != nil {
			return err
		}

		return cluster.DeleteCoreTokenRecord(ctx, tx, record.Name)
	})
	if err != nil {
		return types.SmartError(err)
	}

	remotes := s.Truststore()
	clusterMembers := make([]types.ClusterMemberLocal, 0, remotes.Count())
	for _, clusterMember := range remotes.RemotesByName() {
		clusterMember := types.ClusterMemberLocal{
			Name:        clusterMember.Name,
			Address:     clusterMember.Address,
			Certificate: clusterMember.Certificate,
		}

		clusterMembers = append(clusterMembers, clusterMember)
	}

	clusterCert, err := s.ClusterCert().PublicKeyX509()
	if err != nil {
		return types.SmartError(err)
	}

	localRemote := remotes.RemotesByName()[s.Name()]
	tokenResponse := types.TokenResponse{
		ClusterCert: types.X509Certificate{Certificate: clusterCert},
		ClusterKey:  string(s.ClusterCert().PrivateKey()),

		TrustedMember:  types.ClusterMemberLocal{Name: s.Name(), Address: localRemote.Address, Certificate: localRemote.Certificate},
		ClusterMembers: clusterMembers,
	}

	newRemote := types.Remote{
		Location:    types.Location{Name: req.Name, Address: req.Address},
		Certificate: req.Certificate,
	}

	// Add the cluster member to our local store for authentication.
	err = s.Truststore().Add(s.FileSystem().TrustDir(), newRemote)
	if err != nil {
		return types.SmartError(err)
	}

	tokenResponse.ClusterAdditionalCerts = make(map[string]types.KeyPair)

	// Load the list of custom certificates from its state directory.
	err = filepath.WalkDir(s.FileSystem().CertificatesDir(), func(path string, d fs.DirEntry, err error) error {
		// Skip directories
		if d.IsDir() {
			return nil
		}

		// Find all .crt files to create a list of custom certificates.
		splittedPath := strings.Split(filepath.Base(path), ".")
		if len(splittedPath) == 2 && splittedPath[1] == "crt" {
			// Load the certificate
			cert, err := shared.KeyPairAndCA(s.FileSystem().CertificatesDir(), splittedPath[0], shared.CertServer, shared.CertOptions{})
			if err != nil {
				return fmt.Errorf("Failed to load certificate for additional server %q: %w", splittedPath[0], err)
			}

			additionalCertificate := types.KeyPair{
				Cert: string(cert.PublicKey()),
				Key:  string(cert.PrivateKey()),
			}

			if cert.CA() != nil {
				additionalCertificate.CA = string(cert.CA().Raw)
			}

			tokenResponse.ClusterAdditionalCerts[splittedPath[0]] = additionalCertificate
		}

		return nil
	})
	if err != nil {
		return types.SmartError(err)
	}

	return types.SyncResponse(true, tokenResponse)
}

func clusterGet(s types.State, r *http.Request) types.Response {
	status := s.Database().Status()

	// If the database is not in a ready or waiting state, we can't be sure it's available for use.
	if status != types.DatabaseReady && status != types.DatabaseWaiting {
		return types.SmartError(api.StatusErrorf(http.StatusServiceUnavailable, "%s", string(status)))
	}

	var apiClusterMembers []types.ClusterMember
	err := s.Database().Transaction(r.Context(), func(ctx context.Context, tx *sql.Tx) error {
		var err error
		var clusterMembers []cluster.CoreClusterMember
		var awaitingUpgrade map[string]bool
		if status == types.DatabaseReady {
			clusterMembers, err = cluster.GetCoreClusterMembers(ctx, tx)
		} else {
			schemaInternal, schemaExternal, apiExtensions := s.Database().SchemaVersion()
			clusterMembers, awaitingUpgrade, err = cluster.GetUpgradingClusterMembers(ctx, tx, schemaInternal, schemaExternal, apiExtensions)
		}

		if err != nil {
			return err
		}

		apiClusterMembers = make([]types.ClusterMember, 0, len(clusterMembers))
		for _, clusterMember := range clusterMembers {
			apiClusterMember, err := clusterMember.ToAPI()
			if err != nil {
				return err
			}

			// Assign an upgrade status if the cluster member is awaiting an upgrade.
			if awaitingUpgrade != nil {
				if awaitingUpgrade[apiClusterMember.Name] {
					apiClusterMember.Status = types.MemberNeedsUpgrade
				} else {
					apiClusterMember.Status = types.MemberUpgrading
				}
			}

			apiClusterMembers = append(apiClusterMembers, *apiClusterMember)
		}

		return nil
	})
	if err != nil {
		return types.SmartError(fmt.Errorf("Failed to get cluster members: %w", err))
	}

	// Send a small request to each node to ensure they are reachable if the database is fully online.
	if status == types.DatabaseReady {
		clusterCert, err := s.ClusterCert().PublicKeyX509()
		if err != nil {
			return types.SmartError(err)
		}

		for i, clusterMember := range apiClusterMembers {
			addr := &api.NewURL().Scheme("https").Host(clusterMember.Address.String()).URL
			d, err := internalClient.New(addr, s.ServerCert(), clusterCert, false)
			if err != nil {
				return types.SmartError(fmt.Errorf("Failed to create HTTPS client for cluster member with address %q: %w", addr.String(), err))
			}

			err = internalClient.CheckReady(r.Context(), d)
			if err == nil {
				apiClusterMembers[i].Status = types.MemberOnline
			} else {
				logger, logErr := log.LoggerFromContext(r.Context())
				if logErr != nil {
					return types.InternalError(err)
				}

				logger.Warn(fmt.Sprintf("Failed to get status of cluster member with address %q: %v", addr.String(), err))
			}
		}
	}

	return types.SyncResponse(true, apiClusterMembers)
}

// clusterDisableMu is used to prevent the daemon process from being replaced/stopped during removal from the
// cluster until such time as the request that initiated the removal has finished. This allows for self removal
// from the cluster when not the leader.
var clusterDisableMu sync.Mutex

func clusterMemberPut(s types.State, r *http.Request) types.Response {
	force := r.URL.Query().Get("force") == "1"
	reExec, err := resetClusterMember(r.Context(), s, force)
	if err != nil {
		return types.SmartError(err)
	}

	go reExec()

	return types.ManualResponse(func(w http.ResponseWriter) error {
		err := types.EmptySyncResponse.Render(w, r)
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
func resetClusterMember(ctx context.Context, s types.State, force bool) (reExec func(), err error) {
	intState, err := internalState.ToInternal(s)
	if err != nil {
		return nil, err
	}

	logger, err := log.LoggerFromContext(ctx)
	if err != nil {
		return nil, err
	}

	reExec = func() {
		<-ctx.Done() // Wait until request has finished.

		// NOTE(claudiub): In the case we fail to bootstrap / join the cluster, or if we remove the node
		// from the cluster, we'll be resetting the node's cluster membership. This includes closing the
		// HTTPS and unix socket servers we have open.
		// However, we cannot gracefully shutdown the servers, as there's at least one connection that is
		// still open: the bootstrap / join request. Forcing the connection to close before we're able
		// to write the request response will result in the client getting an EOF error, and no information
		// regarding the failure.
		// Gracefully shutting down the servers in a goroutine will address this issue: while this action
		// happens, we'll be able to write the HTTP response and then close the connection, finally
		// allowing the servers to gracefully shutdown, and the clients to be happy.
		// As the daemon gets re-executed the returned exit function can be ignored as it used to signal
		// a complete shutdown.
		_, err := intState.Stop()
		if err != nil && !force {
			logger.Error("Failed shutting down", slog.String("error", err.Error()))
		}

		err = os.RemoveAll(s.FileSystem().StateDir())
		if err != nil && !force {
			logger.Error("Failed to remove the state directory", slog.String("error", err.Error()))
		}

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
			logger.Error("Failed restarting daemon", slog.String("error", err.Error()))
		}
	}

	return reExec, nil
}

// clusterMemberDelete Removes a cluster member from dqlite and re-execs its daemon.
func clusterMemberDelete(s types.State, r *http.Request) types.Response {
	force := r.URL.Query().Get("force") == "1"
	addr := r.URL.Query().Get("address")
	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return types.SmartError(err)
	}

	ctx := r.Context()

	logger, err := log.LoggerFromContext(ctx)
	if err != nil {
		return types.InternalError(err)
	}

	allRemotes := s.Truststore().RemotesByName()
	remote, remotePresent := allRemotes[name]

	// Determine the address to use for dqlite removal:
	// - If remote exists in truststore and no address provided, use the truststore address.
	// - If remote missing and no address provided, require explicit address.
	// - If address provided, it must match the truststore address (if remote exists) or be valid (if not).
	if remotePresent && addr == "" {
		addr = remote.Address.String()
	} else if !remotePresent && addr == "" {
		// If the remote is not present in the truststore and no address is provided, we cannot proceed.
		return types.SmartError(fmt.Errorf("Cluster member %q not found in truststore; please provide a node address", name))
	} else if remotePresent && addr != "" && remote.Address.String() != addr {
		// Reject if provided address doesn't match the truststore address for this remote name.
		return types.SmartError(fmt.Errorf("Provided address %q does not match the address %q of the remote with name %q", addr, remote.Address.String(), name))
	} else if !remotePresent && addr != "" {
		// Remote missing from truststore; validate the fallback address format.
		addrPort, err := types.ParseAddrPort(addr)
		if err != nil {
			return types.SmartError(fmt.Errorf("Invalid address %q: %w", addr, err))
		}

		// Ensure the fallback address isn't claimed by another remote in the truststore.
		existingRemote := s.Truststore().RemoteByAddress(addrPort)
		if existingRemote != nil {
			return types.SmartError(fmt.Errorf("Address %q is already used by remote %q (address %q); address is only a fallback for %q when it is missing from the truststore", addr, existingRemote.Name, existingRemote.Address.String(), name))
		}

		logger.Warn("Cluster member not found in truststore; proceeding with provided fallback address", slog.String("member", name), slog.String("address", addr))
	}

	// Check cluster membership consistency before allowing removals (unless forced)
	// This ensures core_cluster_members, truststore, and dqlite are all in sync
	if !force {
		intState, err := internalState.ToInternal(s)
		if err != nil {
			return types.SmartError(err)
		}

		err = intState.CheckMembershipConsistency(ctx)
		if err != nil {
			return types.SmartError(err)
		}
	}

	leader, err := s.Database().Leader(ctx)
	if err != nil {
		return types.SmartError(err)
	}

	leaderInfo, err := leader.Leader(ctx)
	if err != nil {
		return types.SmartError(err)
	}

	// If we are not the leader, just forward the request.
	if leaderInfo.Address != s.Address().Host {
		if addr == s.Address().Host {
			// If the member being removed is ourselves and we are not the leader, then lock the
			// clusterPutDisableMu before we forward the request to the leader, so that when the leader
			// goes on to request clusterPutDisable back to ourselves it won't be actioned until we
			// have returned this request back to the original client.
			clusterDisableMu.Lock()
			logger.Info("Acquired cluster self removal lock", slog.String("member", name))

			go func() {
				<-r.Context().Done() // Wait until request is finished.

				logger.Info("Releasing cluster self removal lock", slog.String("member", name))
				clusterDisableMu.Unlock()
			}()
		}

		client, err := s.Connect().Leader(false)
		if err != nil {
			return types.SmartError(err)
		}

		err = internalClient.DeleteClusterMember(ctx, client, name, addr, force)
		if err != nil {
			return types.SmartError(err)
		}

		return types.ManualResponse(func(w http.ResponseWriter) error {
			err := types.EmptySyncResponse.Render(w, r)
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

	info, err := leader.Cluster(ctx)
	if err != nil {
		return types.SmartError(err)
	}

	index := -1
	for i, node := range info {
		if node.Address == addr {
			index = i
			break
		}
	}

	// If we can't find the node in dqlite, that means it failed to fully initialize. It still might have a record in our database so continue along anyway.
	if index < 0 {
		logger.Error("No dqlite record exists for the member", slog.String("member", name))
	}

	var clusterMembers []cluster.CoreClusterMember
	err = s.Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		clusterMembers, err = cluster.GetCoreClusterMembers(ctx, tx)

		return err
	})
	if err != nil {
		return types.SmartError(err)
	}

	// Check if member exists in the database.
	memberInDB := false
	for _, m := range clusterMembers {
		if m.Address == addr {
			memberInDB = true
			break
		}
	}

	// If member not found in dqlite and not in database, return error.
	if index < 0 && !memberInDB {
		return types.SmartError(fmt.Errorf("Cluster member %q with address %q not found in dqlite or database", name, addr))
	}

	numPending := 0
	for _, clusterMember := range clusterMembers {
		if clusterMember.Role == cluster.Pending {
			numPending++
		}
	}

	if len(clusterMembers)-numPending < 1 {
		return types.SmartError(fmt.Errorf("Cannot remove cluster members, there are no remaining non-pending members"))
	}

	if len(info) < 2 {
		return types.SmartError(fmt.Errorf("Cannot leave a cluster with %d members", len(info)))
	}

	// If we are removing the leader of a 2-node cluster, ensure the remaining node is a voter.
	if len(info) == 2 && addr == leaderInfo.Address {
		for _, node := range info {
			if node.Address != leaderInfo.Address && node.Role != dqliteClient.Voter {
				err = leader.Assign(ctx, node.ID, dqliteClient.Voter)
				if err != nil {
					return types.SmartError(err)
				}
			}
		}
	}

	// Refresh members information since we may have changed roles.
	info, err = leader.Cluster(ctx)
	if err != nil {
		return types.SmartError(err)
	}

	// If we are the leader and removing ourselves, reassign the leader role and perform the removal from there.
	if remotePresent && addr == leaderInfo.Address {
		otherNodes := []uint64{}
		for _, node := range info {
			if node.Address != addr && node.Role == dqliteClient.Voter {
				otherNodes = append(otherNodes, node.ID)
			}
		}

		if len(otherNodes) == 0 {
			return types.SmartError(fmt.Errorf("Found no voters to transfer leadership to"))
		}

		randomID := otherNodes[rand.Intn(len(otherNodes))]
		err = leader.Transfer(ctx, randomID)
		if err != nil {
			return types.SmartError(err)
		}

		client, err := s.Connect().Leader(false)
		if err != nil {
			return types.SmartError(err)
		}

		logger, logErr := log.LoggerFromContext(r.Context())
		if logErr != nil {
			return types.InternalError(err)
		}

		clusterDisableMu.Lock()
		logger.Info("Acquired cluster self removal lock", slog.String("member", name))

		go func() {
			<-r.Context().Done() // Wait until request is finished.

			logger.Info("Releasing cluster self removal lock", slog.String("member", name))
			clusterDisableMu.Unlock()
		}()

		err = internalClient.DeleteClusterMember(ctx, client, name, addr, force)
		if err != nil {
			return types.SmartError(err)
		}

		return types.ManualResponse(func(w http.ResponseWriter) error {
			err := types.EmptySyncResponse.Render(w, r)
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
		return types.SmartError(err)
	}

	var memberURL *url.URL
	if !remotePresent {
		memberURL, err = url.Parse("https://" + addr)
		if err != nil {
			return types.SmartError(fmt.Errorf("invalid address %q: %w", addr, err))
		}
	} else {
		memberURL = remote.URL()
	}

	// Tell the cluster member to run its PreRemove hook and return.
	// Set the forwarded flag so that the system to be removed knows the removal is in progress.
	c, err := internalClient.New(memberURL, s.ServerCert(), publicKey, true)
	if err != nil {
		if !force {
			return types.SmartError(err)
		}

		logger.Warn("Failed creating client for remote PreRemove (forcing)", slog.String("error", err.Error()))
	} else {
		err = internalClient.RunPreRemoveHook(ctx, c.UseTarget(name), types.HookRemoveMemberOptions{Force: force})
		if err != nil && !force {
			return types.SmartError(err)
		}
	}

	// Remove the cluster member from the database using its address if available; otherwise
	// return an error indicating that no address was provided or found.
	err = s.Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		return cluster.DeleteCoreClusterMember(ctx, tx, addr)
	})

	if err != nil && !force {
		return types.SmartError(err)
	}

	// Remove the node from dqlite, if it has a record there.
	if index >= 0 {
		err = leader.Remove(ctx, info[index].ID)
		if err != nil {
			return types.SmartError(err)
		}
	}

	u := api.NewURL()
	u.URL = *s.FileSystem().ControlSocket()

	localClient, err := s.Connect().Member(&u.URL, false, nil)
	if err != nil {
		return types.SmartError(err)
	}

	err = internalClient.DeleteTrustStoreEntry(ctx, localClient, name)
	if err != nil && !force {
		return types.SmartError(err)
	}

	client, err := s.Connect().Member(memberURL, false, publicKey)
	if err != nil {
		if !force {
			return types.SmartError(err)
		}

		logger.Warn("Failed connecting to cluster member to perform a node reset", slog.String("error", err.Error()), slog.Bool("force", force))
	} else {
		err = internalClient.ResetClusterMember(ctx, client, name, force)
		if err != nil && !force {
			return types.SmartError(err)
		}
	}

	intState, err := internalState.ToInternal(s)
	if err != nil {
		return types.SmartError(err)
	}

	// Run the PostRemove hook locally.
	hookCtx, hookCancel := context.WithCancel(ctx)
	err = intState.Hooks.PostRemove(hookCtx, s, force)
	hookCancel()
	if err != nil {
		return types.SmartError(err)
	}

	clients, err := s.Connect().Cluster(false)
	if err != nil {
		return types.SmartError(err)
	}

	// Run the PostRemove hook on all other members.
	remotes := s.Truststore()
	err = clients.Query(ctx, true, func(ctx context.Context, c types.Client) error {
		c.SetClusterNotification()
		addrPort, err := types.ParseAddrPort(c.URL().Host)
		if err != nil {
			return err
		}

		remote := remotes.RemoteByAddress(addrPort)
		if remote == nil {
			return fmt.Errorf("No remote found at address %q to run the post-remove hook", c.URL().Host)
		}

		return internalClient.RunPostRemoveHook(ctx, c.UseTarget(remote.Name), types.HookRemoveMemberOptions{Force: force})
	})
	if err != nil {
		return types.SmartError(err)
	}

	return types.EmptySyncResponse
}
