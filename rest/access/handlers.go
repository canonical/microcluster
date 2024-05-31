package access

import (
	"crypto/x509"
	"fmt"
	"net/http"

	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared/logger"

	"github.com/canonical/microcluster/internal/state"
	"github.com/canonical/microcluster/rest/types"
)

// ErrInvalidHost is used to indicate that a request host is invalid.
type ErrInvalidHost struct {
	error
}

// Unwrap implements xerrors.Unwrap for ErrInvalidHost.
func (e ErrInvalidHost) Unwrap() error {
	return e.error
}

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
func Authenticate(state *state.State, r *http.Request, hostAddress string, trustedCerts map[string]x509.Certificate) (bool, error) {
	if r.RemoteAddr == "@" {
		return true, nil
	}

	if state.Address().URL.Host == "" {
		logger.Info("Allowing unauthenticated request to un-initialized system")
		return true, nil
	}

	// Ensure the given host address is valid.
	hostAddrPort, err := types.ParseAddrPort(hostAddress)
	if err != nil {
		return false, fmt.Errorf("Invalid host address %q", hostAddress)
	}

	switch r.Host {
	case hostAddrPort.String():
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
		return false, ErrInvalidHost{error: fmt.Errorf("Invalid request address %q", r.Host)}
	}

	return false, nil
}
