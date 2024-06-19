package rest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"

	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/gorilla/mux"

	"github.com/canonical/microcluster/cluster"
	internalAccess "github.com/canonical/microcluster/internal/rest/access"
	"github.com/canonical/microcluster/internal/rest/client"
	internalState "github.com/canonical/microcluster/internal/state"
	"github.com/canonical/microcluster/rest"
	"github.com/canonical/microcluster/rest/access"
	"github.com/canonical/microcluster/state"
)

func handleAPIRequest(action rest.EndpointAction, state state.State, w http.ResponseWriter, r *http.Request) response.Response {
	if action.Handler == nil {
		return response.NotImplemented(nil)
	}

	// If allow untrusted is not set, the request must be authenticated via core authentication (e.g. certificate in truststore).
	if !action.AllowUntrusted {
		resp := access.AllowAuthenticated(state, r)
		if resp != response.EmptySyncResponse {
			return resp
		}
	}

	// Run the custom access handler if set.
	if action.AccessHandler != nil {
		resp := action.AccessHandler(state, r)
		if resp != response.EmptySyncResponse {
			return resp
		}
	}

	if action.ProxyTarget {
		return proxyTarget(action, state, r)
	}

	return action.Handler(state, r)
}

func proxyTarget(action rest.EndpointAction, s state.State, r *http.Request) response.Response {
	if r.URL == nil {
		return action.Handler(s, r)
	}

	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		logger.Warnf("Failed to parse query string %q: %v", r.URL.RawQuery, err)
	}

	var target string
	if values != nil {
		target = values.Get("target")
	}

	if target == "" || target == s.Name() {
		return action.Handler(s, r)
	}

	var targetURL *api.URL
	err = s.Database().Transaction(r.Context(), func(ctx context.Context, tx *sql.Tx) error {
		clusterMember, err := cluster.GetInternalClusterMember(ctx, tx, target)
		if err != nil {
			return fmt.Errorf("Failed to get cluster member for request target name %q: %w", target, err)
		}

		targetURL = api.NewURL().Scheme("https").Host(clusterMember.Address).Path(r.URL.Path)

		return nil
	})
	if err != nil {
		return response.BadRequest(err)
	}

	clusterCert, err := s.ClusterCert().PublicKeyX509()
	if err != nil {
		return response.InternalError(fmt.Errorf("Failed to parse cluster certificate for request: %w", err))
	}

	client, err := client.New(*targetURL, s.ServerCert(), clusterCert, false)
	if err != nil {
		return response.InternalError(fmt.Errorf("Failed to get a client for the target %q at address %q: %w", target, targetURL.String(), err))
	}

	// Update request URL.
	r.RequestURI = ""
	r.URL.Scheme = targetURL.URL.Scheme
	r.URL.Host = targetURL.URL.Host
	r.Host = targetURL.URL.Host

	logger.Info("Forwarding request to specified target", logger.Ctx{"source": s.Name(), "target": target})
	resp, err := client.MakeRequest(r)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed to send request to target %q: %w", target, err))
	}

	return response.SyncResponse(true, resp.Metadata)
}

func handleDatabaseRequest(action rest.EndpointAction, state state.State, w http.ResponseWriter, r *http.Request) response.Response {
	trusted := r.Context().Value(request.CtxAccess)
	if trusted == nil {
		return response.Forbidden(nil)
	}

	trustedReq, ok := trusted.(internalAccess.TrustedRequest)
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

		state.Database().Accept(conn)
	}

	return action.Handler(state, r)
}

// HandleEndpoint adds the endpoint to the mux router. A function variable is used to implement common logic
// before calling the endpoint action handler associated with the request method, if it exists.
func HandleEndpoint(state state.State, mux *mux.Router, version string, e rest.Endpoint) {
	url := "/" + version
	if e.Path != "" {
		url = filepath.Join(url, e.Path)
	}

	route := mux.HandleFunc(url, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Actually process the request.
		var resp response.Response

		intState, err := internalState.ToInternal(state)
		if err != nil {
			err := response.BadRequest(err).Render(w)
			if err != nil {
				logger.Error("Failed to write HTTP response", logger.Ctx{"url": r.URL, "err": err})
			}

			return
		}

		// Return Unavailable Error (503) if daemon is shutting down, except for endpoints with AllowedDuringShutdown.
		if intState.Context.Err() == context.Canceled && !e.AllowedDuringShutdown {
			err := response.Unavailable(fmt.Errorf("Daemon is shutting down")).Render(w)
			if err != nil {
				logger.Error("Failed to write HTTP response", logger.Ctx{"url": r.URL, "err": err})
			}

			return
		}

		if !e.AllowedBeforeInit {
			if !state.Database().IsOpen() {
				err := response.Unavailable(fmt.Errorf("Daemon not yet initialized")).Render(w)
				if err != nil {
					logger.Error("Failed to write HTTP response", logger.Ctx{"url": r.URL, "err": err})
				}

				return
			}
		}

		// If the request is a database request, the connection should be hijacked.
		handleRequest := handleAPIRequest
		if e.Path == "database" {
			handleRequest = handleDatabaseRequest
		}

		trusted, err := access.Authenticate(state, r, state.Address().URL.Host, state.Remotes().CertificatesNative())
		if err != nil && !errors.As(err, &access.ErrInvalidHost{}) {
			resp = response.Forbidden(fmt.Errorf("Failed to authenticate request: %w", err))
		} else {
			r = internalAccess.SetRequestAuthentication(r, trusted)

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
