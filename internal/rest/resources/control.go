package resources

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/revert"

	internalClient "github.com/canonical/microcluster/v3/internal/rest/client"
	internalTypes "github.com/canonical/microcluster/v3/internal/rest/types"
	internalState "github.com/canonical/microcluster/v3/internal/state"
	"github.com/canonical/microcluster/v3/internal/trust"
	"github.com/canonical/microcluster/v3/internal/utils"
	"github.com/canonical/microcluster/v3/rest"
	"github.com/canonical/microcluster/v3/rest/access"
	"github.com/canonical/microcluster/v3/rest/types"
	"github.com/canonical/microcluster/v3/state"
)

var controlCmd = rest.Endpoint{
	AllowedBeforeInit: true,

	Post: rest.EndpointAction{Handler: controlPost, AccessHandler: access.AllowAuthenticated},
}

func controlPost(state state.State, r *http.Request) response.Response {
	status := state.Database().Status()
	if status != types.DatabaseNotReady {
		return response.SmartError(fmt.Errorf("Unable to initialize cluster: %s", status))
	}

	req := &internalTypes.Control{}
	// Parse the request.
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	if req.Bootstrap && req.JoinToken != "" {
		return response.SmartError(fmt.Errorf("Invalid options - received join token and bootstrap flag"))
	}

	err = utils.ValidateFQDN(req.Name)
	if err != nil {
		return response.SmartError(fmt.Errorf("Invalid cluster member name %q: %w", req.Name, err))
	}

	intState, err := internalState.ToInternal(state)
	if err != nil {
		return response.SmartError(err)
	}

	daemonConfig := trust.Location{Address: req.Address, Name: req.Name}
	err = intState.SetConfig(daemonConfig)
	if err != nil {
		return response.SmartError(err)
	}

	reverter := revert.New()
	defer reverter.Fail()

	serverCert, err := state.ServerCert().PublicKeyX509()
	if err != nil {
		return response.SmartError(err)
	}

	certNameMatches := shared.ValueInSlice(req.Name, serverCert.DNSNames)
	var joinInfo *internalTypes.TokenResponse
	reverter.Add(func() {
		// When joining, don't attempt to reset the cluster member if we never received authorization from any cluster members.
		// This is because we won't have changed any state yet, so resetting the cluster member won't help, and may have its own side-effects.
		if joinInfo == nil && req.JoinToken != "" && !certNameMatches {
			return
		}

		// Run the pre-remove hook like we do for cluster node removals.
		err := intState.Hooks.PreRemove(r.Context(), state, true)
		if err != nil {
			logger.Error("Failed to run pre-remove hook on initialization error", logger.Ctx{"error": err})
		}

		reExec, err := resetClusterMember(r.Context(), state, true)
		if err != nil {
			logger.Error("Failed to reset cluster member on bootstrap error", logger.Ctx{"error": err})
			return
		}

		// Re-exec the daemon to clear any remaining state.
		go reExec()

		// Only send a request to delete the cluster member record if we are joining an existing cluster.
		if joinInfo == nil || req.JoinToken == "" {
			return
		}

		url := api.NewURL().Scheme("https").Host(joinInfo.TrustedMember.Address.String())
		cert, err := shared.GetRemoteCertificate(url.String(), "")
		if err != nil {
			return
		}

		client, err := internalClient.New(*url, state.ServerCert(), cert, false)
		if err != nil {
			return
		}

		// Use `force=1` to ensure the node is fully removed, in case its listener hasn't been set up.
		err = client.DeleteClusterMember(context.Background(), req.Name, true)
		if err != nil {
			logger.Error("Failed to clean up cluster state after join failure", logger.Ctx{"error": err})
		}
	})

	// Replace the server keypair if the cluster member name has changed upon initialization.
	if !certNameMatches {
		err := os.Remove(filepath.Join(state.FileSystem().StateDir, "server.crt"))
		if err != nil {
			return response.SmartError(err)
		}

		err = os.Remove(filepath.Join(state.FileSystem().StateDir, "server.key"))
		if err != nil {
			return response.SmartError(err)
		}

		// Generate a new keypair with the new subject name.
		_, err = shared.KeyPairAndCA(state.FileSystem().StateDir, string(types.ServerCertificateName), shared.CertServer, shared.CertOptions{AddHosts: true, CommonName: req.Name})
		if err != nil {
			return response.SmartError(err)
		}

		err = intState.ReloadCert(types.ServerCertificateName)
		if err != nil {
			return response.SmartError(err)
		}
	}

	if req.JoinToken != "" {
		joinInfo, err = joinWithToken(state, r, req)
		if err != nil {
			return response.SmartError(err)
		}

		reverter.Success()

		return response.EmptySyncResponse
	}

	err = intState.StartAPI(r.Context(), req.Bootstrap, req.InitConfig)
	if err != nil {
		return response.SmartError(err)
	}

	reverter.Success()

	return response.EmptySyncResponse
}

func joinWithToken(state state.State, r *http.Request, req *internalTypes.Control) (*internalTypes.TokenResponse, error) {
	token, err := internalTypes.DecodeToken(req.JoinToken)
	if err != nil {
		return nil, err
	}

	serverCert, err := state.ServerCert().PublicKeyX509()
	if err != nil {
		return nil, fmt.Errorf("Failed to parse server certificate when bootstrapping API: %w", err)
	}

	intState, err := internalState.ToInternal(state)
	if err != nil {
		return nil, err
	}

	// Add the local node to the list of clusterMembers.
	daemonConfig := &trust.Location{Address: req.Address, Name: req.Name}
	localClusterMember := trust.Remote{
		Location:    *daemonConfig,
		Certificate: types.X509Certificate{Certificate: serverCert},
	}

	// Prepare the cluster for the incoming dqlite request by creating a database entry.
	internalVersion, externalVersion, _ := state.Database().SchemaVersion()
	newClusterMember := types.ClusterMember{
		ClusterMemberLocal: types.ClusterMemberLocal{
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
			logger.Warn("Failed to get certificate of cluster member", logger.Ctx{"address": url.String(), "error": err})
			continue
		}

		fingerprint := shared.CertFingerprint(cert)
		if fingerprint != token.Fingerprint {
			logger.Warn("Cluster certificate token does not match that of cluster member", logger.Ctx{"address": url.String(), "fingerprint": fingerprint, "expected": token.Fingerprint})
			continue
		}

		d, err := internalClient.New(*url, state.ServerCert(), cert, false)
		if err != nil {
			return nil, err
		}

		joinInfo, err = internalClient.AddClusterMember(context.Background(), d, newClusterMember)
		if err == nil {
			break
		}

		logger.Error("Unable to complete cluster join request", logger.Ctx{"address": addr.String(), "error": err})
		lastErr = err
	}

	if joinInfo == nil {
		return nil, fmt.Errorf("%d join attempts were unsuccessful. Last error: %w", len(token.JoinAddresses), lastErr)
	}

	// Set up cluster certificate.
	err = util.WriteCert(state.FileSystem().StateDir, string(types.ClusterCertificateName), []byte(joinInfo.ClusterCert.String()), []byte(joinInfo.ClusterKey), nil)
	if err != nil {
		return nil, err
	}

	// Setup any additional certificates.
	for name, cert := range joinInfo.ClusterAdditionalCerts {
		// Only write the CA if present.
		var ca []byte
		if cert.CA != "" {
			ca = []byte(cert.CA)
		}

		err := util.WriteCert(state.FileSystem().CertificatesDir, name, []byte(cert.Cert), []byte(cert.Key), ca)
		if err != nil {
			return nil, err
		}
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
		return nil, err
	}

	// Start the HTTPS listeners and join Dqlite.
	err = intState.StartAPI(r.Context(), false, req.InitConfig, joinAddrs.Strings()...)
	if err != nil {
		return nil, err
	}

	return joinInfo, nil
}
