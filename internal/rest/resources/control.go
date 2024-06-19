package resources

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/revert"
	"github.com/canonical/lxd/shared/validate"

	"github.com/canonical/microcluster/internal/rest/client"
	internalTypes "github.com/canonical/microcluster/internal/rest/types"
	internalState "github.com/canonical/microcluster/internal/state"
	"github.com/canonical/microcluster/internal/trust"
	"github.com/canonical/microcluster/rest"
	"github.com/canonical/microcluster/rest/access"
	"github.com/canonical/microcluster/rest/types"
	"github.com/canonical/microcluster/state"
)

var controlCmd = rest.Endpoint{
	AllowedBeforeInit: true,

	Post: rest.EndpointAction{Handler: controlPost, AccessHandler: access.AllowAuthenticated},
}

func controlPost(state state.State, r *http.Request) response.Response {
	req := &internalTypes.Control{}
	// Parse the request.
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	if req.Bootstrap && req.JoinToken != "" {
		return response.SmartError(fmt.Errorf("Invalid options - received join token and bootstrap flag"))
	}

	err = validate.IsHostname(req.Name)
	if err != nil {
		return response.SmartError(fmt.Errorf("Invalid cluster member name %q: %w", req.Name, err))
	}

	intState, err := internalState.ToInternal(state)
	if err != nil {
		return response.SmartError(err)
	}

	if req.JoinToken != "" {
		return joinWithToken(state, r, req)
	}

	daemonConfig := &trust.Location{Address: req.Address, Name: req.Name}
	err = intState.StartAPI(req.Bootstrap, req.InitConfig, daemonConfig)
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

func joinWithToken(state state.State, r *http.Request, req *internalTypes.Control) response.Response {
	token, err := internalTypes.DecodeToken(req.JoinToken)
	if err != nil {
		return response.SmartError(err)
	}

	intState, err := internalState.ToInternal(state)
	if err != nil {
		return response.SmartError(err)
	}

	serverCert, err := state.ServerCert().PublicKeyX509()
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed to parse server certificate when bootstrapping API: %w", err))
	}

	// Add the local node to the list of clusterMembers.
	daemonConfig := &trust.Location{Address: req.Address, Name: req.Name}
	localClusterMember := trust.Remote{
		Location:    *daemonConfig,
		Certificate: types.X509Certificate{Certificate: serverCert},
	}

	// Prepare the cluster for the incoming dqlite request by creating a database entry.
	internalVersion, externalVersion := state.Database().Schema().Version()
	newClusterMember := internalTypes.ClusterMember{
		ClusterMemberLocal: internalTypes.ClusterMemberLocal{
			Name:        localClusterMember.Name,
			Address:     localClusterMember.Address,
			Certificate: localClusterMember.Certificate,
		},
		SchemaInternalVersion: internalVersion,
		SchemaExternalVersion: externalVersion,
		Secret:                token.Secret,
		Extensions:            intState.Extensions,
	}

	// Get a client to the target address.
	var lastErr error
	var joinInfo *internalTypes.TokenResponse
	for _, addr := range token.JoinAddresses {
		url := api.NewURL().Scheme("https").Host(addr.String())

		cert, err := shared.GetRemoteCertificate(url.String(), "")
		if err != nil {
			return response.SmartError(fmt.Errorf("Failed to get certificate of cluster member %q: %w", url.URL.Host, err))
		}

		fingerprint := shared.CertFingerprint(cert)
		if fingerprint != token.Fingerprint {
			return response.SmartError(fmt.Errorf("Cluster certificate token does not match that of cluster member %q", url.URL.Host))
		}

		d, err := client.New(*url, state.ServerCert(), cert, false)
		if err != nil {
			return response.SmartError(err)
		}

		joinInfo, err = d.AddClusterMember(context.Background(), newClusterMember)
		if err == nil {
			break
		}

		logger.Error("Unable to complete cluster join request", logger.Ctx{"address": addr.String(), "error": err})
		lastErr = err
	}

	if joinInfo == nil {
		return response.SmartError(fmt.Errorf("%d join attempts were unsuccessful. Last error: %w", len(token.JoinAddresses), lastErr))
	}

	reverter := revert.New()
	defer reverter.Fail()

	// If at some point we fail to join the cluster, instruct whoever authorized us to remove us from the cluster.
	// This should also reset our database and listeners.
	reverter.Add(func() {
		url := api.NewURL().Scheme("https").Host(joinInfo.TrustedMember.Address.String())
		cert, err := shared.GetRemoteCertificate(url.String(), "")
		if err != nil {
			return
		}

		client, err := client.New(*url, state.ServerCert(), cert, false)
		if err != nil {
			return
		}

		reExec, err := resetClusterMember(r.Context(), state, true)
		if err != nil {
			return
		}

		// Re-exec the daemon to clear any remaining state.
		go reExec()

		// Use `force=1` to ensure the node is fully removed, in case its listener hasn't been set up.
		err = client.DeleteClusterMember(context.Background(), req.Name, true)
		if err != nil {
			logger.Error("Failed to clean up cluster state after join failure", logger.Ctx{"error": err})
		}
	})

	err = util.WriteCert(state.FileSystem().StateDir, "cluster", []byte(joinInfo.ClusterCert.String()), []byte(joinInfo.ClusterKey), nil)
	if err != nil {
		return response.SmartError(err)
	}

	joinAddrs := types.AddrPorts{}
	clusterMembers := make([]trust.Remote, 0, len(joinInfo.ClusterMembers))
	for _, clusterMember := range joinInfo.ClusterMembers {
		remote := trust.Remote{
			Location:    trust.Location{Name: clusterMember.Name, Address: clusterMember.Address},
			Certificate: clusterMember.Certificate,
		}

		joinAddrs = append(joinAddrs, clusterMember.Address)
		clusterMembers = append(clusterMembers, remote)
	}

	clusterMembers = append(clusterMembers, localClusterMember)
	err = state.Remotes().Add(state.FileSystem().TrustDir, clusterMembers...)
	if err != nil {
		return response.SmartError(err)
	}

	// Start the HTTPS listeners and join Dqlite.
	err = intState.StartAPI(false, req.InitConfig, daemonConfig, joinAddrs.Strings()...)
	if err != nil {
		return response.SmartError(err)
	}

	reverter.Success()

	return response.EmptySyncResponse
}
