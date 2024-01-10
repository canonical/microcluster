package access

import (
	"fmt"
	"net/http"

	"github.com/canonical/lxd/lxd/response"

	"github.com/canonical/microcluster/internal/rest/access"
	"github.com/canonical/microcluster/internal/rest/client"
	"github.com/canonical/microcluster/internal/state"
)

// AllowAuthenticated is an AccessHandler which allows all requests.
// This function doesn't do anything itself, except return the EmptySyncResponse that allows the request to
// proceed. However in order to access any API route you must be authenticated, unless the handler's AllowUntrusted
// property is set to true or you are an admin.
func AllowAuthenticated(state *state.State, r *http.Request) response.Response {
	return response.EmptySyncResponse
}

// RestrictNotification is an AccessHandler which requires authentication for requests with the cluster notification header set.
func RestrictNotification(state *state.State, r *http.Request) response.Response {
	if client.IsForwardedRequest(r) {
		trusted, err := access.Authenticate(state, r)
		if err != nil {
			return response.BadRequest(err)
		}

		if !trusted {
			return response.Forbidden(fmt.Errorf("Notification is not trusted"))
		}
	}

	return response.EmptySyncResponse
}

// Strictly allow dqlite cluster members to access an endpoint.
func AllowClusterMembers(state *state.State, r *http.Request) response.Response {
	return response.EmptySyncResponse
}

// Strictly allow non-cluster members to access an endpoint.
func AllowNonClusterMembers(state *state.State, r *http.Request) response.Response {
	return response.EmptySyncResponse
}
