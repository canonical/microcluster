package access

import (
	"net/http"

	"github.com/lxc/lxd/lxd/response"

	"github.com/canonical/microcluster/internal/state"
)

// TrustedRequest holds data pertaining to what level of trust we have for the request.
type TrustedRequest struct {
	Trusted bool
}

// AllowAuthenticated is an AccessHandler which allows all requests.
// This function doesn't do anything itself, except return the EmptySyncResponse that allows the request to
// proceed. However in order to access any API route you must be authenticated, unless the handler's AllowUntrusted
// property is set to true or you are an admin.
func AllowAuthenticated(state *state.State, r *http.Request) response.Response {
	return response.EmptySyncResponse
}
