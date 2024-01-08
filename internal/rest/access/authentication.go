package access

import (
	"context"
	"crypto/x509"
	"fmt"
	"net/http"
	"strings"

	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared/logger"

	"github.com/canonical/microcluster/internal/rest/client"
	"github.com/canonical/microcluster/internal/state"
	"github.com/canonical/microcluster/internal/trust"
)

// TrustedRequest holds data pertaining to what level of trust we have for the request.
type TrustedRequest struct {
	Trusted bool
}

// SetRequestAuthentication sets the trusted status for the request. A trusted request will be treated as having come from a trusted system.
func SetRequestAuthentication(r *http.Request, trusted bool) *http.Request {
	r = r.WithContext(context.WithValue(r.Context(), any(request.CtxAccess), TrustedRequest{Trusted: trusted}))

	return r
}

// Authenticate ensures the request certificates are trusted before proceeding.
// - Requests over the unix socket are always allowed.
// - HTTP requests require our cluster cert, or remote certs.
func Authenticate(state *state.State, r *http.Request) (bool, error) {
	if r.RemoteAddr == "@" {
		return true, nil
	}

	if state.Address().URL.Host == "" {
		logger.Info("Allowing unauthenticated request to un-initialized system")
		return true, nil
	}

	var trustedCerts map[string]x509.Certificate
	switch r.Host {
	case state.Address().URL.Host:
		trustedCerts = state.Remotes().CertificatesNative()
	default:
		return false, fmt.Errorf("Invalid request address %q", r.Host)
	}

	if r.TLS != nil {
		for _, cert := range r.TLS.PeerCertificates {
			trusted, fingerprint := util.CheckTrustState(*cert, trustedCerts, nil, false)
			if trusted {
				clusterRemote := state.Remotes(trust.Cluster).RemoteByCertificateFingerprint(fingerprint)
				nonClusterRemote := state.Remotes(trust.NonCluster).RemoteByCertificateFingerprint(fingerprint)
				if clusterRemote == nil && nonClusterRemote == nil {
					// The cert fingerprint can no longer be matched back against what is in the truststore (e.g. file
					// was deleted), so we are no longer trusted.
					return false, nil
				}

				return true, nil
			}
		}
	}

	return false, nil
}
