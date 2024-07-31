package resources

import (
	"fmt"
	"net/http"

	"github.com/canonical/lxd/lxd/response"

	internalState "github.com/canonical/microcluster/v2/internal/state"
	"github.com/canonical/microcluster/v2/rest"
	"github.com/canonical/microcluster/v2/rest/access"
	"github.com/canonical/microcluster/v2/state"
)

var readyCmd = rest.Endpoint{
	AllowedBeforeInit: true,
	Path:              "ready",

	Get: rest.EndpointAction{Handler: getWaitReady, AccessHandler: access.AllowAuthenticated},
}

func getWaitReady(state state.State, r *http.Request) response.Response {
	intState, err := internalState.ToInternal(state)
	if err != nil {
		return response.SmartError(err)
	}

	if intState.Context.Err() != nil {
		return response.Unavailable(fmt.Errorf("Daemon is shutting down"))
	}

	select {
	case <-intState.ReadyCh:
	default:
		return response.Unavailable(fmt.Errorf("Daemon is not ready yet"))
	}

	return response.EmptySyncResponse
}
