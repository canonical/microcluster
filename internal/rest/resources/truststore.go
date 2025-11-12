package resources

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/mux"

	"github.com/canonical/microcluster/v3/internal/log"
	"github.com/canonical/microcluster/v3/internal/rest/access"
	internalClient "github.com/canonical/microcluster/v3/internal/rest/client"
	internalState "github.com/canonical/microcluster/v3/internal/state"
	"github.com/canonical/microcluster/v3/internal/trust"
	"github.com/canonical/microcluster/v3/microcluster/rest"
	"github.com/canonical/microcluster/v3/microcluster/rest/response"
	"github.com/canonical/microcluster/v3/microcluster/types"
)

var trustCmd = rest.Endpoint{
	Path:              "truststore",
	AllowedBeforeInit: true,

	Post: rest.EndpointAction{Handler: trustPost, AccessHandler: access.AllowAuthenticated},
}

var trustEntryCmd = rest.Endpoint{
	Path:              "truststore/{name}",
	AllowedBeforeInit: true,

	Delete: rest.EndpointAction{Handler: trustDelete, AccessHandler: access.AllowAuthenticated},
}

func trustPost(s types.State, r *http.Request) response.Response {
	req := types.ClusterMemberLocal{}

	// Parse the request.
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	newRemote := trust.Remote{
		Location:    trust.Location{Name: req.Name, Address: req.Address},
		Certificate: req.Certificate,
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	if !types.IsNotification(r) {
		clients, err := s.Connect().Cluster(true)
		if err != nil {
			return response.SmartError(err)
		}

		logger, err := log.LoggerFromContext(ctx)
		if err != nil {
			return response.InternalError(err)
		}

		successCount := 0
		attemptCount := 0
		var counterMu sync.Mutex

		// Try to add the truststore entry to all other nodes in the cluster.
		// We don't fail the entire operation if some nodes are unreachable.
		err = clients.Query(ctx, true, func(ctx context.Context, c types.Client) error {
			// No need to send a request to ourselves, or to the node we are adding.
			if s.Address().Host == c.URL().Host || req.Address.String() == c.URL().Host {
				return nil
			}

			counterMu.Lock()
			attemptCount++
			counterMu.Unlock()

			err := internalClient.AddTrustStoreEntry(ctx, c, req)
			if err != nil {
				// log error but continue with other nodes
				logger.Warn("Failed adding truststore entry to node", slog.String("node", c.URL().Host), slog.String("error", err.Error()))
				return nil
			}

			counterMu.Lock()
			successCount++
			counterMu.Unlock()
			return nil
		})
		if err != nil {
			return response.SmartError(err)
		}

		// Only fail if we attempted to propagate to other nodes but all failed
		if attemptCount > 0 && successCount == 0 {
			return response.SmartError(fmt.Errorf("Failed adding truststore entry to any cluster node"))
		}
	}

	intState, err := internalState.ToInternal(s)
	if err != nil {
		return response.SmartError(err)
	}

	// At this point, the node has joined dqlite so we can add a local record for it if we haven't already from a heartbeat (or if we are the leader).
	remotes := intState.InternalRemotes()
	_, ok := remotes.RemotesByName()[newRemote.Name]
	if !ok {
		err = remotes.Add(s.FileSystem().TrustDir(), newRemote)
		if err != nil {
			return response.SmartError(fmt.Errorf("Failed adding local record of newly joined node %q: %w", req.Name, err))
		}
	}

	return response.EmptySyncResponse
}

func trustDelete(s types.State, r *http.Request) response.Response {
	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	intState, err := internalState.ToInternal(s)
	if err != nil {
		return response.SmartError(err)
	}

	remotesMap := intState.InternalRemotes().RemotesByName()
	nodeToRemove, ok := remotesMap[name]
	if !ok {
		return response.SmartError(fmt.Errorf("No truststore entry found for node with name %q", name))
	}

	if !types.IsNotification(r) {
		clients, err := s.Connect().Cluster(true)
		if err != nil {
			return response.SmartError(err)
		}

		err = clients.Query(ctx, true, func(ctx context.Context, c types.Client) error {
			// No need to send a request to ourselves, or to the node we are adding.
			if s.Address().Host == c.URL().Host || nodeToRemove.URL().URL.Host == c.URL().Host {
				return nil
			}

			return internalClient.DeleteTrustStoreEntry(ctx, c, name)
		})
		if err != nil {
			return response.SmartError(err)
		}
	}

	remotes := intState.InternalRemotes()
	remotesMap = remotes.RemotesByName()
	delete(remotesMap, name)

	newRemotes := make([]types.ClusterMember, 0, len(remotesMap))
	for _, remote := range remotesMap {
		newRemote := types.ClusterMember{
			ClusterMemberLocal: types.ClusterMemberLocal{
				Name:        remote.Name,
				Address:     remote.Address,
				Certificate: remote.Certificate,
			},
		}

		newRemotes = append(newRemotes, newRemote)
	}

	err = remotes.Replace(s.FileSystem().TrustDir(), newRemotes...)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed to remove truststore entry for node with name %q: %w", name, err))
	}

	return response.EmptySyncResponse
}
