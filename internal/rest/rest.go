package rest

import (
	"context"
	"crypto/x509"
	"fmt"
	"net/http"
	"path/filepath"

	"github.com/gorilla/mux"
	"github.com/lxc/lxd/lxd/rbac"
	"github.com/lxc/lxd/lxd/request"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared/logger"

	"github.com/canonical/microcluster/internal/rest/access"
	"github.com/canonical/microcluster/internal/state"
)

// EndpointAlias represents an alias URL of and Endpoint in our API.
type EndpointAlias struct {
	Name string // Name for this alias.
	Path string // Path pattern for this alias.
}

// EndpointAction represents an action on an API endpoint.
type EndpointAction struct {
	Handler        func(state *state.State, r *http.Request) response.Response
	AccessHandler  func(state *state.State, r *http.Request) response.Response
	AllowUntrusted bool
}

// Endpoint represents a URL in our API.
type Endpoint struct {
	Name    string          // Name for this endpoint.
	Path    string          // Path pattern for this endpoint.
	Aliases []EndpointAlias // Any aliases for this endpoint.
	Get     EndpointAction
	Put     EndpointAction
	Post    EndpointAction
	Delete  EndpointAction
	Patch   EndpointAction

	AllowedDuringShutdown bool // Whether we should return Unavailable Error (503) if daemon is shutting down.
}

func handleAPIRequest(action EndpointAction, state *state.State, w http.ResponseWriter, r *http.Request) response.Response {
	trusted := r.Context().Value(request.CtxAccess)
	if trusted == nil {
		return response.Forbidden(nil)
	}

	trustedReq, ok := trusted.(access.TrustedRequest)
	if !ok {
		return response.Forbidden(nil)
	}

	if !trustedReq.Trusted && !action.AllowUntrusted {
		return response.Forbidden(nil)
	}

	if action.Handler == nil {
		return response.NotImplemented(nil)
	}

	if action.AccessHandler != nil {
		// Defer access control to custom handler.
		accessResp := action.AccessHandler(state, r)
		if accessResp != response.EmptySyncResponse {
			return accessResp
		}
	} else if !action.AllowUntrusted {
		// Require admin privileges.
		if !rbac.UserIsAdmin(r) {
			return response.Forbidden(nil)
		}
	}

	return action.Handler(state, r)
}

func handleDatabaseRequest(action EndpointAction, state *state.State, w http.ResponseWriter, r *http.Request) response.Response {
	trusted := r.Context().Value(request.CtxAccess)
	if trusted == nil {
		return response.Forbidden(nil)
	}

	trustedReq, ok := trusted.(access.TrustedRequest)
	if !ok {
		return response.Forbidden(nil)
	}

	if !trustedReq.Trusted {
		return response.Forbidden(nil)
	}

	if action.Handler == nil {
		return response.NotImplemented(nil)
	}

	// If the request is a POST, then it is likely from the dqlite dial function, so hijack the connection.
	if r.Method == "POST" {
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			return response.InternalError(fmt.Errorf("Webserver does not support hijacking"))
		}

		conn, _, err := hijacker.Hijack()
		if err != nil {
			return response.InternalError(fmt.Errorf("Failed to hijack connection: %w", err))
		}

		state.Database.Accept(conn)
	}

	return action.Handler(state, r)
}

// HandleEndpoint adds the endpoint to the mux router. A function variable is used to implement common logic
// before calling the endpoint action handler associated with the request method, if it exists.
func HandleEndpoint(state *state.State, mux *mux.Router, version string, e Endpoint) {
	url := "/" + version
	if e.Path != "" {
		url = filepath.Join(url, e.Path)
	}

	route := mux.HandleFunc(url, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Actually process the request.
		var resp response.Response

		// Return Unavailable Error (503) if daemon is shutting down, except for endpoints with AllowedDuringShutdown.
		if state.Context.Err() == context.Canceled && !e.AllowedDuringShutdown {
			err := response.Unavailable(fmt.Errorf("Daemon is shutting down")).Render(w)
			if err != nil {
				logger.Error("Failed to write HTTP response", logger.Ctx{"url": r.URL, "err": err})
			}

			return
		}

		// If the request is a database request, the connection should be hijacked.
		handleRequest := handleAPIRequest
		if e.Path == "database" {
			handleRequest = handleDatabaseRequest
		}

		trusted, err := authenticate(state, r)
		if err != nil {
			resp = response.Forbidden(fmt.Errorf("Failed to authenticate request: %w", err))
		} else {
			r = r.WithContext(context.WithValue(r.Context(), any(request.CtxAccess), access.TrustedRequest{Trusted: trusted}))

			switch r.Method {
			case "GET":
				resp = handleRequest(e.Get, state, w, r)
			case "PUT":
				resp = handleRequest(e.Put, state, w, r)
			case "POST":
				resp = handleRequest(e.Post, state, w, r)
			case "DELETE":
				resp = handleRequest(e.Delete, state, w, r)
			case "PATCH":
				resp = handleRequest(e.Patch, state, w, r)
			default:
				resp = response.NotFound(fmt.Errorf("Method '%s' not found", r.Method))
			}
		}

		// Handle errors.
		if e.Path != "database" {
			err := resp.Render(w)
			if err != nil {
				err := response.InternalError(err).Render(w)
				if err != nil {
					logger.Error("Failed writing error for HTTP response", logger.Ctx{"url": url, "error": err})
				}
			}
		}
	})

	// If the endpoint has a canonical name then record it so it can be used to build URLS
	// and accessed in the context of the request by the handler function.
	if e.Name != "" {
		route.Name(e.Name)
	}
}

// authenticate ensures the request certificates are trusted before proceeding.
// - Requests over the unix socket are always allowed.
// - HTTP requests require our cluster cert, or remote certs.
func authenticate(state *state.State, r *http.Request) (bool, error) {
	if r.RemoteAddr == "@" {
		return true, nil
	}

	var trustedCerts map[string]x509.Certificate
	switch r.Host {
	case state.Address.URL.Host:
		trustedCerts = state.Remotes().CertificatesNative()
	default:
		return false, fmt.Errorf("Invalid request address %q", r.Host)
	}

	if r.TLS != nil {
		for _, cert := range r.TLS.PeerCertificates {
			trusted, fingerprint := util.CheckTrustState(*cert, trustedCerts, nil, false)
			if trusted {
				remote := state.Remotes().RemoteByCertificateFingerprint(fingerprint)
				if remote == nil {
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
