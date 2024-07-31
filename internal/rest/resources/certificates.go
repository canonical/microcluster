package resources

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/shared/logger"
	"github.com/gorilla/mux"

	"github.com/canonical/microcluster/v3/client"
	internalState "github.com/canonical/microcluster/v3/internal/state"
	"github.com/canonical/microcluster/v3/rest"
	"github.com/canonical/microcluster/v3/rest/access"
	"github.com/canonical/microcluster/v3/rest/types"
	"github.com/canonical/microcluster/v3/state"
)

var clusterCertificatesCmd = rest.Endpoint{
	AllowedBeforeInit: true,
	Path:              "cluster/certificates/{name}",

	Put: rest.EndpointAction{Handler: clusterCertificatesPut, AccessHandler: access.AllowAuthenticated},
}

func clusterCertificatesPut(s state.State, r *http.Request) response.Response {
	certificateName, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	req := types.KeyPair{}

	// Parse the request.
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	err = s.Database().IsOpen(r.Context())
	if err != nil {
		logger.Warn(fmt.Sprintf("Database is offline, only updating local %q certificate", certificateName), logger.Ctx{"error": err})
	}

	// Forward the request to all other nodes if we are the first.
	if !client.IsNotification(r) && err == nil {
		cluster, err := s.Cluster(true)
		if err != nil {
			return response.SmartError(err)
		}

		err = cluster.Query(r.Context(), true, func(ctx context.Context, c *client.Client) error {
			return c.UpdateCertificate(ctx, types.CertificateName(certificateName), req)
		})
		if err != nil {
			return response.SmartError(fmt.Errorf("Failed to update %q certificate on peers: %w", certificateName, err))
		}
	}

	certBlock, _ := pem.Decode([]byte(req.Cert))
	if certBlock == nil {
		return response.BadRequest(fmt.Errorf("Certificate must be base64 encoded PEM certificate"))
	}

	keyBlock, _ := pem.Decode([]byte(req.Key))
	if keyBlock == nil {
		return response.BadRequest(fmt.Errorf("Private key must be base64 encoded PEM key"))
	}

	// Validate the certificate's name.
	if strings.Contains(certificateName, "/") || strings.Contains(certificateName, "\\") || strings.Contains(certificateName, "..") {
		return response.BadRequest(fmt.Errorf("Certificate name cannot be a path"))
	}

	var certificateDir string
	if certificateName == string(types.ClusterCertificateName) {
		certificateDir = s.FileSystem().StateDir
	} else {
		certificateDir = s.FileSystem().CertificatesDir

		// Check if an additional listener exists for that name.
		// We cannot query the daemon's config of the additional listeners as
		// they might not yet be confiugred on every cluster member.
		found := false
		for _, name := range s.ExtensionServers() {
			if name == certificateName {
				found = true
			}
		}

		if !found {
			return response.BadRequest(fmt.Errorf("No matching additional server found for %q", certificateName))
		}
	}

	// If a CA was specified, validate that as well.
	if req.CA != "" {
		caBlock, _ := pem.Decode([]byte(req.CA))
		if caBlock == nil {
			return response.BadRequest(fmt.Errorf("CA must be base64 encoded PEM key"))
		}

		err = os.WriteFile(filepath.Join(certificateDir, fmt.Sprintf("%s.ca", certificateName)), []byte(req.CA), 0664)
		if err != nil {
			return response.SmartError(err)
		}
	}

	// Write the keypair to the state directory.
	err = os.WriteFile(filepath.Join(certificateDir, fmt.Sprintf("%s.crt", certificateName)), []byte(req.Cert), 0664)
	if err != nil {
		return response.SmartError(err)
	}

	err = os.WriteFile(filepath.Join(certificateDir, fmt.Sprintf("%s.key", certificateName)), []byte(req.Key), 0600)
	if err != nil {
		return response.SmartError(err)
	}

	intState, err := internalState.ToInternal(s)
	if err != nil {
		return response.SmartError(err)
	}

	// Load the new cert from the state directory on this node.
	err = intState.ReloadCert(types.CertificateName(certificateName))
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}
