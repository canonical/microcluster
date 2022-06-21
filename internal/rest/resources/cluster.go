package resources

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/canonical/microcluster/internal/logger"
	"github.com/canonical/microcluster/internal/rest"
	"github.com/canonical/microcluster/internal/rest/access"
	"github.com/canonical/microcluster/internal/rest/client"
	"github.com/canonical/microcluster/internal/rest/types"
	"github.com/canonical/microcluster/internal/state"
	"github.com/canonical/microcluster/internal/trust"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

var clusterCmd = rest.Endpoint{
	Path: "cluster",

	Post: rest.EndpointAction{Handler: clusterPost, AllowUntrusted: true},
	Get:  rest.EndpointAction{Handler: clusterGet, AccessHandler: access.AllowAuthenticated},
}

func clusterPost(state *state.State, r *http.Request) response.Response {
	req := types.ClusterMemberPost{}

	// Parse the request.
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	if !client.IsForwardedRequest(r) {
		cluster, err := state.Cluster(r)
		if err != nil {
			return response.SmartError(err)
		}

		err = cluster.Query(state.Context, true, func(ctx context.Context, c *client.Client) error {
			return c.AddClusterMember(ctx, req)
		})
		if err != nil {
			return response.SmartError(err)
		}
	}

	// Check if any of the remote's addresses are currently in use.
	existingRemote := state.Remotes().RemoteByAddress(req.Address)
	if existingRemote != nil {
		return response.SmartError(fmt.Errorf("Remote with address %q exists", req.Address.String()))
	}

	// Add a trust store entry for the new member.
	// This will trigger a database update, unless another node has beaten us to it.
	err = state.Remotes().Add(state.OS.TrustDir, trust.Remote{Name: req.Name, Address: req.Address, Certificate: req.Certificate})
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

func clusterGet(state *state.State, r *http.Request) response.Response {
	members, err := state.Database.Cluster()
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed to get cluster members: %w", err))
	}

	apiClusterMembers := make([]types.ClusterMember, len(members))
	for i, member := range members {
		// Create an entry for any nodes without truststore yaml files.
		addrPort, err := types.ParseAddrPort(member.Address)
		if err != nil {
			return response.SmartError(err)
		}

		currentClusterMember := state.Remotes().RemoteByAddress(addrPort)
		if currentClusterMember == nil {
			addrPort, err := types.ParseAddrPort(member.Address)
			if err != nil {
				return response.SmartError(err)
			}

			apiClusterMembers[i] = types.ClusterMember{
				Name:        "",
				Address:     addrPort,
				Role:        member.Role.String(),
				Certificate: types.X509Certificate{},
				Status:      types.MemberNotTrusted,
			}

			continue
		}

		clusterMember := types.ClusterMember{
			Name:        currentClusterMember.Name,
			Address:     currentClusterMember.Address,
			Role:        member.Role.String(),
			Certificate: currentClusterMember.Certificate,
			Status:      types.MemberUnreachable,
		}

		if member.Address == state.Address.URL.Host {
			clusterMember.Status = types.MemberOnline
			apiClusterMembers[i] = clusterMember

			continue
		}

		clusterMemberCert, err := state.ClusterCert().PublicKeyX509()
		if err != nil {
			return response.SmartError(err)
		}

		addr := currentClusterMember.URL()
		d, err := client.New(addr, state.ServerCert(), clusterMemberCert, false)
		if err != nil {
			return response.SmartError(fmt.Errorf("Failed to create HTTPS client for cluster member with address %q: %w", addr.String(), err))
		}

		err = d.QueryStruct(state.Context, "GET", client.InternalEndpoint, api.NewURL().Path("ready"), nil, nil)
		if err == nil {
			clusterMember.Status = types.MemberOnline
			break
		} else {
			logger.Warnf("Failed to get status of cluster member with address %q: %v", addr.String(), err)
		}

		apiClusterMembers[i] = clusterMember
	}

	nodeAddrs := make([]string, len(members))
	for i, member := range members {
		nodeAddrs[i] = member.Address
	}

	// Create entries for nodes with truststore files that are not actually in dqlite.
	for _, clusterMember := range state.Remotes() {
		if !shared.StringInSlice(clusterMember.Address.String(), nodeAddrs) {
			missingClusterMember := types.ClusterMember{
				Name:        clusterMember.Name,
				Address:     clusterMember.Address,
				Certificate: clusterMember.Certificate,
				Status:      types.MemberNotFound,
			}

			apiClusterMembers = append(apiClusterMembers, missingClusterMember)
		}
	}

	return response.SyncResponse(true, apiClusterMembers)
}
