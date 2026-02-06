package resources

import (
	"fmt"
	"net/http"

	"github.com/canonical/microcluster/v3/internal/rest/access"
	internalState "github.com/canonical/microcluster/v3/internal/state"
	"github.com/canonical/microcluster/v3/microcluster/rest"
	"github.com/canonical/microcluster/v3/microcluster/rest/response"
	"github.com/canonical/microcluster/v3/microcluster/types"
)

var readyCmd = rest.Endpoint{
	AllowedBeforeInit: true,
	Path:              "ready",

	Get: rest.EndpointAction{Handler: getWaitReady, AccessHandler: access.AllowAuthenticated},
}

func getWaitReady(state types.State, r *http.Request) response.Response {
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
