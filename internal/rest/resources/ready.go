package resources

import (
	"fmt"
	"net/http"

	"github.com/canonical/microcluster/v4/internal/rest/access"
	internalState "github.com/canonical/microcluster/v4/internal/state"
	"github.com/canonical/microcluster/v4/microcluster/types"
)

var readyCmd = types.Endpoint{
	// Allow waiting for the daemon to become ready even if not yet initialized.
	AllowedBeforeInit: true,
	Path:              "ready",

	Get: types.EndpointAction{Handler: getWaitReady, AccessHandler: access.AllowAuthenticated},
}

func getWaitReady(state types.State, r *http.Request) types.Response {
	intState, err := internalState.ToInternal(state)
	if err != nil {
		return types.SmartError(err)
	}

	if intState.Context.Err() != nil {
		return types.Unavailable(fmt.Errorf("Daemon is shutting down"))
	}

	select {
	case <-intState.ReadyCh:
	default:
		return types.Unavailable(fmt.Errorf("Daemon is not ready yet"))
	}

	return types.EmptySyncResponse
}
