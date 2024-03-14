package resources

import (
	"fmt"
	"net/http"

	"github.com/canonical/lxd/lxd/response"

	"github.com/canonical/microcluster/internal/state"
	"github.com/canonical/microcluster/rest"
	"github.com/canonical/microcluster/rest/access"
)

var readyCmd = rest.Endpoint{
	AllowedBeforeInit: true,
	Path:              "ready",

	// swagger:operation GET /cluster/1.0/ready ready ready_get
	//
	//  Waits for the server to start
	//
	//	Waits until the server has reported that it is finished setting up and is ready to receive requests.
	//
	//	---
	//	produces:
	//	  - application/json
	//	responses:
	//	  "200":
	//	    $ref: "#/responses/EmptySyncResponse"
	//	  "400":
	//	    $ref: "#/responses/BadRequest"
	//	  "403":
	//	    $ref: "#/responses/Forbidden"
	//	  "500":
	//	    $ref: "#/responses/InternalServerError"
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
