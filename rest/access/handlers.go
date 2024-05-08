package access

import (
	"crypto/x509"
	"fmt"
	"net/http"

	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared/logger"

	"github.com/canonical/microcluster/internal/state"
)

// AllowAuthenticated is an AccessHandler which allows all requests.
// This function doesn't do anything itself, except return the EmptySyncResponse that allows the request to
// proceed. However in order to access any API route you must be authenticated, unless the handler's AllowUntrusted
// property is set to true or you are an admin.
func AllowAuthenticated(state *state.State, r *http.Request) response.Response {
	return response.EmptySyncResponse
}

// Authenticate ensures the request certificates are trusted against the given set of trusted certificates.
// - Requests over the unix socket are always allowed.
// - HTTP requests require the TLS Peer certificate to match an entry in the supplied map of certificates.
func Authenticate(state *state.State, r *http.Request, trustedCerts map[string]x509.Certificate) (bool, error) {
	if r.RemoteAddr == "@" {
		return true, nil
	}

	if state.Address().URL.Host == "" {
		logger.Info("Allowing unauthenticated request to un-initialized system")
		return true, nil
	}

	switch r.Host {
	case state.Address().URL.Host:
		if r.TLS != nil {
			for _, cert := range r.TLS.PeerCertificates {
				trusted, fingerprint := util.CheckTrustState(*cert, trustedCerts, nil, false)
				if trusted {
					logger.Debugf("Trusting HTTP request to %q from %q with fingerprint %q", r.URL.String(), r.RemoteAddr, fingerprint)

					return trusted, nil
				}
			}
		}
	default:
		return false, fmt.Errorf("Invalid request address %q", r.Host)
	}

	return false, nil
}
