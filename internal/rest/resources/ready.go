package resources

import (
	"fmt"
	"net/http"

	"github.com/lxc/lxd/lxd/response"

	"github.com/canonical/microcluster/internal/rest"
	"github.com/canonical/microcluster/internal/rest/access"
	"github.com/canonical/microcluster/internal/state"
)

var readyCmd = rest.Endpoint{
	Path: "ready",

	Get: rest.EndpointAction{Handler: getWaitReady, AccessHandler: access.AllowAuthenticated},
}

func getWaitReady(state *state.State, r *http.Request) response.Response {
	if state.Context.Err() != nil {
		return response.Unavailable(fmt.Errorf("Daemon is shutting down"))
	}

	select {
	case <-state.ReadyCh:
	default:
		return response.Unavailable(fmt.Errorf("Daemon is not ready yet"))
	}

	return response.EmptySyncResponse
}
