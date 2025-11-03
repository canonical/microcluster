package access

import (
	"context"
	"crypto/subtle"
	"crypto/x509"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/canonical/lxd/shared/api"

	"github.com/canonical/microcluster/v3/internal/endpoints"
	"github.com/canonical/microcluster/v3/internal/log"
	"github.com/canonical/microcluster/v3/internal/rest/client"
	"github.com/canonical/microcluster/v3/internal/state"
	internalState "github.com/canonical/microcluster/v3/internal/state"
	"github.com/canonical/microcluster/v3/microcluster/rest/response"
	"github.com/canonical/microcluster/v3/microcluster/types"
)

// TrustedRequest holds data pertaining to what level of trust we have for the request.
type TrustedRequest struct {
	Trusted bool
}

// ErrInvalidHost is used to indicate that a request host is invalid.
type ErrInvalidHost struct {
	error
}

// Unwrap implements xerrors.Unwrap for ErrInvalidHost.
func (e ErrInvalidHost) Unwrap() error {
	return e.error
}

// AllowAuthenticated checks if the request is trusted by extracting access.TrustedRequest from the request context.
// This handler is used as an access handler by default if AllowUntrusted is false on a rest.EndpointAction.
func AllowAuthenticated(state state.State, r *http.Request) (bool, response.Response) {
	trusted := r.Context().Value(client.CtxAccess)
	if trusted == nil {
		return false, response.Forbidden(nil)
	}

	trustedReq, ok := trusted.(TrustedRequest)
	if !ok {
		return false, response.Forbidden(nil)
	}

	if !trustedReq.Trusted {
		return false, response.Forbidden(nil)
	}

	return true, nil
}

// certificateInDate returns an error if the current time is before the certificates "not before", or after the
// certificates "not after".
func certificateInDate(cert x509.Certificate) error {
	now := time.Now()
	if now.Before(cert.NotBefore) {
		return api.StatusErrorf(http.StatusUnauthorized, "Certificate is not yet valid")
	}

	if now.After(cert.NotAfter) {
		return api.StatusErrorf(http.StatusUnauthorized, "Certificate has expired")
	}

	return nil
}

// checkMutualTLS checks whether the given certificate is valid and is present in the given trustedCerts map.
// Returns true if the certificate is trusted, and the fingerprint of the certificate.
func checkMutualTLS(ctx context.Context, cert x509.Certificate, trustedCerts map[string]x509.Certificate) (bool, string) {
	err := certificateInDate(cert)
	if err != nil {
		return false, ""
	}

	logger, err := log.LoggerFromContext(ctx)
	if err != nil {
		// We failed to get the logger so we can't log the error.
		return false, ""
	}

	// Check whether client certificate is in the map of trusted certs.
	for fingerprint, v := range trustedCerts {
		if subtle.ConstantTimeCompare(cert.Raw, v.Raw) == 1 {
			logger.Debug("Matched trusted cert", slog.String("fingerprint", fingerprint), slog.String("subject", v.Subject.String()))
			return true, fingerprint
		}
	}

	return false, ""
}

// Authenticate ensures the request certificates are trusted against the given set of trusted certificates.
// - Requests over the unix socket are always allowed.
// - HTTP requests require the TLS Peer certificate to match an entry in the supplied map of certificates.
func Authenticate(state state.State, r *http.Request, hostAddress string, trustedCerts map[string]x509.Certificate) (bool, error) {
	if r.RemoteAddr == "@" {
		return true, nil
	}

	intState, err := internalState.ToInternal(state)
	if err != nil {
		return false, err
	}

	logger, err := log.LoggerFromContext(r.Context())
	if err != nil {
		return false, err
	}

	// Check if it's the core API listener and if it is using the server.crt.
	// This indicates that the daemon is in a pre-init state and is listening on the PreInitListenAddress.
	endpoint := intState.Endpoints.Get(endpoints.EndpointsCore)
	network, ok := endpoint.(*endpoints.Network)
	if ok {
		if state.ServerCert().Fingerprint() == network.TLS().Fingerprint() {
			logger.Info("Allowing unauthenticated request to un-initialized system")
			return true, nil
		}
	}

	// Ensure the given host address is valid.
	hostAddrPort, err := types.ParseAddrPort(hostAddress)
	if err != nil {
		return false, fmt.Errorf("Invalid host address %q", hostAddress)
	}

	switch r.Host {
	case hostAddrPort.WithZone("").String():
		if r.TLS != nil {
			for _, cert := range r.TLS.PeerCertificates {
				trusted, fingerprint := checkMutualTLS(r.Context(), *cert, trustedCerts)
				if trusted {
					logger.Debug("Authenticated request", slog.String("origin", r.RemoteAddr), slog.String("destination", r.URL.String()), slog.String("fingerprint", fingerprint))
					return trusted, nil
				}
			}
		}

	default:
		return false, ErrInvalidHost{error: fmt.Errorf("Invalid request address %q", r.Host)}
	}

	return false, nil
}

// SetRequestAuthentication sets the trusted status for the request. A trusted request will be treated as having come from a trusted system.
func SetRequestAuthentication(r *http.Request, trusted bool) *http.Request {
	r = r.WithContext(context.WithValue(r.Context(), any(client.CtxAccess), TrustedRequest{Trusted: trusted}))

	return r
}
